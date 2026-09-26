package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/scheduler"
)

// ReportLockKey is the PostgreSQL advisory-lock key that elects the one replica
// allowed to generate the report. Advisory-lock keys are a single bigint
// namespace per database, shared by every client of that database, so the value
// only has to be unique there; it is otherwise arbitrary. 0x52505254 is ASCII
// "RPRT". It fits in 32 bits, so pg_locks shows it as classid 0,
// objid 1380995668, objsubid 1 — which is how scripts/advisory-lock-demo.sh
// finds the holder.
const ReportLockKey int64 = 0x52505254

// unlockTimeout bounds the release statement. It runs on a context detached from
// the job's cancellation, so a shutdown mid-report still unlocks.
const unlockTimeout = 5 * time.Second

// ReportJob generates the daily product report on at most one replica at a time.
//
// The scheduler only stops the SAME job overlapping inside one process; every
// replica still ticks on its own. A session-level PostgreSQL advisory lock is
// what elects a single runner across replicas: the job tries the lock, runs the
// report when it gets it, and logs a skip when another replica holds it. The
// lock is non-blocking on purpose — a replica that loses the race skips this
// tick instead of queueing a second report behind the first.
type ReportJob struct {
	// Hold keeps the lock held for this long after the report is generated,
	// standing in for the render and upload a real report spends time on. Zero
	// adds nothing. The module reads it from custom.products.report.hold, which
	// only scripts/advisory-lock-demo.sh sets, so a second replica's tick visibly
	// lands while the lock is held.
	Hold time.Duration
}

// Execute implements scheduler.Job
func (j *ReportJob) Execute(ctx scheduler.JobContext) (err error) {
	log := ctx.Logger().WithFields(map[string]any{
		"jobID":   ctx.JobID(),
		"trigger": ctx.TriggerType(),
		"lockKey": ReportLockKey,
	})

	db := ctx.DB()
	if db == nil {
		return errors.New("report job: database not available")
	}

	// A session-level advisory lock belongs to ONE backend connection. Taken
	// through the pool, the unlock could land on a different connection and
	// release nothing while both statements still succeed. Session (go-bricks
	// v0.65.0, ADR-112) pins one connection for the lock, the work and the unlock.
	sess, err := db.Session(ctx)
	if err != nil {
		return fmt.Errorf("report job: open session: %w", err)
	}
	// Deferred first, so it runs last: after the unlock below. An open Session
	// holds one of the pool's connections until it is closed.
	defer func() {
		if closeErr := sess.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("report job: close session: %w", closeErr)
		}
	}()

	var acquired bool
	if err = sess.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", ReportLockKey).Scan(&acquired); err != nil {
		return fmt.Errorf("report job: try advisory lock: %w", err)
	}
	if !acquired {
		log.Info().Msg("Report job skipped: another replica holds the lock")
		return nil
	}

	// Registered as soon as the lock is held, so no later return can skip it.
	// Close hands the connection back to the pool WITHOUT ending the backend, so
	// a lock left held would ride along on a pooled connection until it died.
	// Runs on any other backend would skip the report, and a run that reuses
	// that backend would re-acquire it: PostgreSQL stacks session advisory
	// locks, so that run's single unlock leaves the stale lock held.
	defer func() {
		if unlockErr := releaseReportLock(ctx, sess); unlockErr != nil {
			if err == nil {
				err = unlockErr
			}
			return
		}
		log.Info().Msg("Report job lock released")
	}()

	log.Info().Msg("Report job lock acquired")
	return j.generate(ctx)
}

// generate is the report itself. It runs only on the replica holding
// ReportLockKey.
func (j *ReportJob) generate(ctx scheduler.JobContext) error {
	ctx.Logger().Info().
		Str("jobID", ctx.JobID()).
		Msg("Generating daily report")

	// Query product's database and generate report...
	// generate txt report file with all products data
	// upload report to storage service (sftp, s3, etc.) -> make it dynamic via interface storage.Upload(destinationPath, fileContents)

	if j.Hold <= 0 {
		return nil
	}
	timer := time.NewTimer(j.Hold)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("report job: interrupted while holding the lock: %w", ctx.Err())
	}
}

// releaseReportLock unlocks ReportLockKey on the session that took it. The
// statement runs on a bounded context that ignores the job's cancellation, so
// the lock is released even when the job was interrupted.
func releaseReportLock(ctx context.Context, sess database.Session) error {
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
	defer cancel()

	var released bool
	if err := sess.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1)", ReportLockKey).Scan(&released); err != nil {
		return fmt.Errorf("report job: release advisory lock: %w", err)
	}
	// false means this session did not hold the lock (PostgreSQL also raises a
	// WARNING). It cannot happen on a pinned session that just acquired it, so it
	// is reported rather than ignored.
	if !released {
		return errors.New("report job: advisory lock was not held at release")
	}
	return nil
}
