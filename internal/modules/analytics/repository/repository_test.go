package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/domain"
	"github.com/gaborage/go-bricks/database"
	dbtest "github.com/gaborage/go-bricks/database/testing"
	dbtypes "github.com/gaborage/go-bricks/database/types"
)

const testProductID = "product-123"

func newView() *domain.ProductView {
	return domain.NewProductView(testProductID, "Mozilla/5.0", "127.0.0.1", "session-abc", "https://example.com")
}

// dbReturning is a getDB that always resolves to db.
func dbReturning(db database.Interface) func(context.Context) (database.Interface, error) {
	return func(context.Context) (database.Interface, error) {
		return db, nil
	}
}

// dbUnavailable is a getDB whose connection cannot be resolved.
func dbUnavailable(context.Context) (database.Interface, error) {
	return nil, errors.New("analytics database down")
}

func TestRecordView(t *testing.T) {
	ctx := context.Background()

	t.Run("successful insert", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectExec("INSERT INTO product_views").WillReturnRowsAffected(1)

		view := newView()
		if err := NewAnalyticsRepository(dbReturning(db)).RecordView(ctx, view); err != nil {
			t.Fatalf("RecordView() unexpected error = %v", err)
		}
		if view.ID == "" {
			t.Error("RecordView() left view.ID empty, want a generated UUID")
		}
		dbtest.AssertExecExecuted(t, db, "INSERT INTO product_views")
	})

	t.Run("database unavailable", func(t *testing.T) {
		if err := NewAnalyticsRepository(dbUnavailable).RecordView(ctx, newView()); err == nil {
			t.Error("RecordView() expected error, got nil")
		}
	})

	t.Run("insert error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectExec("INSERT INTO product_views").WillReturnError(errors.New("insert failed"))

		if err := NewAnalyticsRepository(dbReturning(db)).RecordView(ctx, newView()); err == nil {
			t.Error("RecordView() expected error, got nil")
		}
	})
}

func TestGetViewStats(t *testing.T) {
	ctx := context.Background()
	statsColumns := []string{"total_views", "views_today", "views_this_week", "last_viewed_at"}

	t.Run("successful query with last viewed time", func(t *testing.T) {
		lastViewed := time.Now().UTC()
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("FROM product_views").WillReturnRows(
			dbtest.NewRowSet(statsColumns...).AddRow(int64(42), int64(5), int64(12), lastViewed),
		)

		stats, err := NewAnalyticsRepository(dbReturning(db)).GetViewStats(ctx, testProductID)
		if err != nil {
			t.Fatalf("GetViewStats() unexpected error = %v", err)
		}
		if stats.ProductID != testProductID || stats.TotalViews != 42 || stats.ViewsToday != 5 || stats.ViewsThisWeek != 12 {
			t.Errorf("GetViewStats() = %+v, want %s with 42/5/12 views", *stats, testProductID)
		}
		if !stats.LastViewedAt.Equal(lastViewed) {
			t.Errorf("LastViewedAt = %v, want %v", stats.LastViewedAt, lastViewed)
		}
		dbtest.AssertQueryExecuted(t, db, "WHERE product_id = $1")
	})

	// MAX(viewed_at) is NULL for a product nobody has viewed yet.
	t.Run("no views yet leaves last viewed at zero", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("FROM product_views").WillReturnRows(
			dbtest.NewRowSet(statsColumns...).AddRow(int64(0), int64(0), int64(0), nil),
		)

		stats, err := NewAnalyticsRepository(dbReturning(db)).GetViewStats(ctx, testProductID)
		if err != nil {
			t.Fatalf("GetViewStats() unexpected error = %v", err)
		}
		if stats.TotalViews != 0 {
			t.Errorf("TotalViews = %d, want 0", stats.TotalViews)
		}
		if !stats.LastViewedAt.IsZero() {
			t.Errorf("LastViewedAt = %v, want zero value", stats.LastViewedAt)
		}
	})

	t.Run("database unavailable", func(t *testing.T) {
		if _, err := NewAnalyticsRepository(dbUnavailable).GetViewStats(ctx, testProductID); err == nil {
			t.Error("GetViewStats() expected error, got nil")
		}
	})

	t.Run("query error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("FROM product_views").WillReturnError(errors.New("query failed"))

		if _, err := NewAnalyticsRepository(dbReturning(db)).GetViewStats(ctx, testProductID); err == nil {
			t.Error("GetViewStats() expected error, got nil")
		}
	})
}

// TestBuildTopViewedQuery pins the SQL the type-safe builder renders for the
// top-viewed aggregate.
//
// The point of asserting the ToSQL output (and not only the endpoint response)
// is the ADR-082 rejection class introduced in go-bricks v0.60.0: an identifier
// that is really an expression — a bare "COUNT(*)" in Select — is refused at
// ToSQL time, at runtime, with `go build` still green. A compile-clean binary is
// therefore not evidence the query works. This test is.
func TestBuildTopViewedQuery(t *testing.T) {
	const wantSQL = "SELECT product_id, COUNT(*) AS total_views " +
		"FROM product_views " +
		"GROUP BY product_id " +
		"ORDER BY total_views DESC " +
		"LIMIT 10"

	query, args, err := buildTopViewedQuery(10)
	if err != nil {
		t.Fatalf("buildTopViewedQuery() unexpected error = %v", err)
	}
	if query != wantSQL {
		t.Errorf("buildTopViewedQuery() SQL mismatch\n got: %s\nwant: %s", query, wantSQL)
	}
	// Limit renders as a literal, so the statement binds nothing. Every value
	// that does vary by caller elsewhere in this repository is a bind parameter.
	if len(args) != 0 {
		t.Errorf("buildTopViewedQuery() args = %#v, want none", args)
	}
}

// TestBuildTopViewedQueryLimit covers the LIMIT guard. Builder.Limit takes a
// uint64: a negative int would wrap to ~1.8e19, and a zero is dropped as "unset",
// rendering no LIMIT clause at all — an unbounded scan of the fact table where a
// bounded top-N was asked for. Neither may reach the database.
func TestBuildTopViewedQueryLimit(t *testing.T) {
	t.Run("positive limits render a bounded clause", func(t *testing.T) {
		tests := []struct {
			name     string
			limit    int
			wantTail string
		}{
			{name: "smallest limit", limit: 1, wantTail: "LIMIT 1"},
			{name: "typical limit", limit: 25, wantTail: "LIMIT 25"},
			{name: "maximum service limit", limit: 100, wantTail: "LIMIT 100"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				query, _, err := buildTopViewedQuery(tt.limit)
				if err != nil {
					t.Fatalf("buildTopViewedQuery(%d) unexpected error = %v", tt.limit, err)
				}
				if !strings.HasSuffix(query, tt.wantTail) {
					t.Errorf("buildTopViewedQuery(%d) = %q, want suffix %q", tt.limit, query, tt.wantTail)
				}
			})
		}
	})

	t.Run("non-positive limits are refused", func(t *testing.T) {
		for _, limit := range []int{0, -1} {
			query, _, err := buildTopViewedQuery(limit)
			if err == nil {
				t.Errorf("buildTopViewedQuery(%d) = %q, want error", limit, query)
			}
			if query != "" {
				t.Errorf("buildTopViewedQuery(%d) returned SQL %q, want empty", limit, query)
			}
		}
	})
}

func TestGetTopViewed(t *testing.T) {
	ctx := context.Background()

	t.Run("successful query", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT product_id").
			WillReturnRows(
				dbtest.NewRowSet("product_id", "total_views").
					AddRow("product-a", int64(42)).
					AddRow("product-b", int64(7)),
			)

		repo := NewAnalyticsRepository(func(context.Context) (database.Interface, error) {
			return db, nil
		})

		stats, err := repo.GetTopViewed(ctx, 10)
		if err != nil {
			t.Fatalf("GetTopViewed() unexpected error = %v", err)
		}
		if len(stats) != 2 {
			t.Fatalf("GetTopViewed() returned %d rows, want 2", len(stats))
		}
		if stats[0].ProductID != "product-a" || stats[0].TotalViews != 42 {
			t.Errorf("GetTopViewed() first row = %+v, want product-a/42", *stats[0])
		}
		if stats[1].ProductID != "product-b" || stats[1].TotalViews != 7 {
			t.Errorf("GetTopViewed() second row = %+v, want product-b/7", *stats[1])
		}

		// The statement that actually reached the driver is the built one.
		dbtest.AssertQueryExecuted(t, db, "GROUP BY product_id")
		dbtest.AssertQueryExecuted(t, db, "ORDER BY total_views DESC")
	})

	t.Run("empty result set", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT product_id").WillReturnRows(dbtest.NewRowSet("product_id", "total_views"))

		stats, err := NewAnalyticsRepository(dbReturning(db)).GetTopViewed(ctx, 10)
		if err != nil {
			t.Fatalf("GetTopViewed() unexpected error = %v", err)
		}
		if len(stats) != 0 {
			t.Errorf("GetTopViewed() returned %d rows, want 0", len(stats))
		}
	})

	// The previous hand-written `LIMIT $1` bound with 0 returned no rows. Keep
	// that, and prove the short-circuit answers before the database is touched
	// at all: getDB here fails AND counts its invocations, so the subtest breaks
	// both if a statement is built and if a connection is even resolved.
	t.Run("non-positive limit returns no rows without touching the database", func(t *testing.T) {
		for _, limit := range []int{0, -1} {
			getDBCalls := 0
			repo := NewAnalyticsRepository(func(context.Context) (database.Interface, error) {
				getDBCalls++
				return nil, errors.New("getDB must not be called for a non-positive limit")
			})

			stats, err := repo.GetTopViewed(ctx, limit)
			if err != nil {
				t.Errorf("GetTopViewed(%d) unexpected error = %v", limit, err)
			}
			if len(stats) != 0 {
				t.Errorf("GetTopViewed(%d) returned %d rows, want 0", limit, len(stats))
			}
			if getDBCalls != 0 {
				t.Errorf("GetTopViewed(%d) resolved a database connection %d times, want 0", limit, getDBCalls)
			}
		}
	})

	t.Run("database unavailable", func(t *testing.T) {
		repo := NewAnalyticsRepository(func(context.Context) (database.Interface, error) {
			return nil, errors.New("analytics database down")
		})

		if _, err := repo.GetTopViewed(ctx, 10); err == nil {
			t.Error("GetTopViewed() expected error, got nil")
		}
	})

	t.Run("query error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT product_id").WillReturnError(errors.New("query failed"))

		repo := NewAnalyticsRepository(func(context.Context) (database.Interface, error) {
			return db, nil
		})

		if _, err := repo.GetTopViewed(ctx, 10); err == nil {
			t.Error("GetTopViewed() expected error, got nil")
		}
	})
}
