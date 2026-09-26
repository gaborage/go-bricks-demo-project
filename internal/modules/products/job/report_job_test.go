package job

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/database"
	dbtest "github.com/gaborage/go-bricks/database/testing"
	dbtypes "github.com/gaborage/go-bricks/database/types"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/scheduler"
)

const (
	tryLockSQL = "pg_try_advisory_lock"
	unlockSQL  = "pg_advisory_unlock"
)

// fakeJobContext is the minimal scheduler.JobContext the job reads: a context,
// a logger and the database. The framework builds the real one per run.
type fakeJobContext struct {
	context.Context
	db database.Interface
}

var _ scheduler.JobContext = (*fakeJobContext)(nil)

func (f *fakeJobContext) JobID() string                   { return "test-job" }
func (f *fakeJobContext) TriggerType() string             { return "manual" }
func (f *fakeJobContext) Logger() logger.Logger           { return logger.New("error", false) }
func (f *fakeJobContext) DB() database.Interface          { return f.db }
func (f *fakeJobContext) Messaging() messaging.AMQPClient { return nil }
func (f *fakeJobContext) Config() *config.Config          { return nil }

func newJobContext(ctx context.Context, db database.Interface) *fakeJobContext {
	return &fakeJobContext{Context: ctx, db: db}
}

// lockRow is the single boolean row pg_try_advisory_lock / pg_advisory_unlock return.
func lockRow(column string, value bool) *dbtest.RowSet {
	return dbtest.NewRowSet(column).AddRow(value)
}

// assertSessionCalls checks the pinned session saw exactly these statements, in
// order, each bound to ReportLockKey — and that the pool saw nothing, because a
// lock or unlock that leaked to the pool could land on another backend.
func assertSessionCalls(t *testing.T, db *dbtest.TestDB, sess *dbtest.TestSession, want ...string) {
	t.Helper()
	calls := sess.QueryLog()
	if len(calls) != len(want) {
		t.Fatalf("session statements = %d (%v), want %d (%v)", len(calls), calls, len(want), want)
	}
	for i, call := range calls {
		if !strings.Contains(call.SQL, want[i]) {
			t.Errorf("session statement %d = %q, want it to call %s", i+1, call.SQL, want[i])
		}
		if len(call.Args) != 1 || call.Args[0] != ReportLockKey {
			t.Errorf("session statement %d args = %v, want [%d]", i+1, call.Args, ReportLockKey)
		}
	}
	if pool := db.QueryLog(); len(pool) != 0 {
		t.Errorf("pool statements = %v, want none: the lock must stay on the pinned session", pool)
	}
	if pool := db.ExecLog(); len(pool) != 0 {
		t.Errorf("pool execs = %v, want none: the lock must stay on the pinned session", pool)
	}
}

func TestReportJobRunsAndReleasesWhenLockAcquired(t *testing.T) {
	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	sess := db.ExpectSession().
		ExpectQuery(tryLockSQL).WillReturnRows(lockRow(tryLockSQL, true)).
		ExpectQuery(unlockSQL).WillReturnRows(lockRow(unlockSQL, true))

	err := (&ReportJob{}).Execute(newJobContext(context.Background(), db))

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	assertSessionCalls(t, db, sess, tryLockSQL, unlockSQL)
	dbtest.AssertSessionClosed(t, sess)
}

func TestReportJobSkipsWhenAnotherReplicaHoldsTheLock(t *testing.T) {
	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	sess := db.ExpectSession().
		ExpectQuery(tryLockSQL).WillReturnRows(lockRow(tryLockSQL, false))

	// A canceled context plus a Hold: if the skip path reached the report, the
	// hold would return the context error instead of nil.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&ReportJob{Hold: time.Hour}).Execute(newJobContext(ctx, db))

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil: losing the lock is a skip, not a failure", err)
	}
	// No unlock: this session never held the lock, and unlocking someone else's
	// is not possible anyway — it would only raise a server WARNING.
	assertSessionCalls(t, db, sess, tryLockSQL)
	dbtest.AssertSessionClosed(t, sess)
}

// unlockCtxRecorder wraps the fake so a test can see the context the unlock ran
// under; dbtest.TestSession itself ignores contexts.
type unlockCtxRecorder struct {
	*dbtest.TestDB
	unlockCtxErr error
	unlockSeen   bool
}

func (d *unlockCtxRecorder) Session(ctx context.Context) (database.Session, error) {
	sess, err := d.TestDB.Session(ctx)
	if err != nil {
		return nil, err
	}
	return &unlockCtxSession{Session: sess, rec: d}, nil
}

type unlockCtxSession struct {
	database.Session
	rec *unlockCtxRecorder
}

func (s *unlockCtxSession) QueryRow(ctx context.Context, query string, args ...any) dbtypes.Row {
	if strings.Contains(query, unlockSQL) {
		s.rec.unlockSeen = true
		s.rec.unlockCtxErr = ctx.Err()
	}
	return s.Session.QueryRow(ctx, query, args...)
}

func TestReportJobReleasesLockWhenInterrupted(t *testing.T) {
	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	sess := db.ExpectSession().
		ExpectQuery(tryLockSQL).WillReturnRows(lockRow(tryLockSQL, true)).
		ExpectQuery(unlockSQL).WillReturnRows(lockRow(unlockSQL, true))
	rec := &unlockCtxRecorder{TestDB: db}

	// Shutdown cancels the job context mid-report. The unlock still has to run,
	// and on a context that is NOT canceled: a real driver refuses a statement
	// on a canceled context without touching the connection, so the lock would
	// stay held on a connection Close hands back to the pool.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&ReportJob{Hold: time.Hour}).Execute(newJobContext(ctx, rec))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want it to wrap context.Canceled", err)
	}
	if !rec.unlockSeen || rec.unlockCtxErr != nil {
		t.Fatalf("unlock seen = %v with ctx error %v, want it run on a live context", rec.unlockSeen, rec.unlockCtxErr)
	}
	assertSessionCalls(t, db, sess, tryLockSQL, unlockSQL)
	dbtest.AssertSessionClosed(t, sess)
}

func TestReportJobFailsWhenTryLockErrors(t *testing.T) {
	errBackend := errors.New("backend gone")
	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	sess := db.ExpectSession().
		ExpectQuery(tryLockSQL).WillReturnError(errBackend)

	err := (&ReportJob{}).Execute(newJobContext(context.Background(), db))

	if !errors.Is(err, errBackend) {
		t.Fatalf("Execute() error = %v, want it to wrap %v", err, errBackend)
	}
	assertSessionCalls(t, db, sess, tryLockSQL)
	dbtest.AssertSessionClosed(t, sess)
}

func TestReportJobSurfacesReleaseFailures(t *testing.T) {
	errBackend := errors.New("backend gone")

	tests := []struct {
		name    string
		unlock  func(*dbtest.TestSession) *dbtest.TestSession
		wantErr func(error) bool
	}{
		{
			name: "unlock statement fails",
			unlock: func(s *dbtest.TestSession) *dbtest.TestSession {
				return s.ExpectQuery(unlockSQL).WillReturnError(errBackend)
			},
			wantErr: func(err error) bool { return errors.Is(err, errBackend) },
		},
		{
			name: "lock was not held at release",
			unlock: func(s *dbtest.TestSession) *dbtest.TestSession {
				return s.ExpectQuery(unlockSQL).WillReturnRows(lockRow(unlockSQL, false))
			},
			wantErr: func(err error) bool { return err != nil && strings.Contains(err.Error(), "not held") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbtest.NewTestDB(dbtypes.PostgreSQL)
			sess := tt.unlock(db.ExpectSession().
				ExpectQuery(tryLockSQL).WillReturnRows(lockRow(tryLockSQL, true)))

			err := (&ReportJob{}).Execute(newJobContext(context.Background(), db))

			if !tt.wantErr(err) {
				t.Fatalf("Execute() error = %v, want the release failure", err)
			}
			assertSessionCalls(t, db, sess, tryLockSQL, unlockSQL)
			dbtest.AssertSessionClosed(t, sess)
		})
	}
}

func TestReportJobFailsWithoutASession(t *testing.T) {
	// No ExpectSession: the strict fake refuses Session(), as a pool that cannot
	// hand out a connection would.
	db := dbtest.NewTestDB(dbtypes.PostgreSQL)

	err := (&ReportJob{}).Execute(newJobContext(context.Background(), db))

	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Fatalf("Execute() error = %v, want an open-session failure", err)
	}
}

func TestReportJobFailsWithoutDatabase(t *testing.T) {
	err := (&ReportJob{}).Execute(newJobContext(context.Background(), nil))

	if err == nil || !strings.Contains(err.Error(), "database not available") {
		t.Fatalf("Execute() error = %v, want a database-not-available failure", err)
	}
}
