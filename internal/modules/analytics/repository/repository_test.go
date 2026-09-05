package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks/database"
	dbtest "github.com/gaborage/go-bricks/database/testing"
	dbtypes "github.com/gaborage/go-bricks/database/types"
)

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

	// The previous hand-written `LIMIT $1` bound with 0 returned no rows. Keep
	// that, and prove no statement reaches the driver — the builder would have
	// dropped a zero LIMIT and scanned the whole table.
	t.Run("non-positive limit returns no rows without querying", func(t *testing.T) {
		for _, limit := range []int{0, -1} {
			db := dbtest.NewTestDB(dbtypes.PostgreSQL)
			repo := NewAnalyticsRepository(func(context.Context) (database.Interface, error) {
				return db, nil
			})

			stats, err := repo.GetTopViewed(ctx, limit)
			if err != nil {
				t.Errorf("GetTopViewed(%d) unexpected error = %v", limit, err)
			}
			if len(stats) != 0 {
				t.Errorf("GetTopViewed(%d) returned %d rows, want 0", limit, len(stats))
			}
			if got := len(db.QueryLog()); got != 0 {
				t.Errorf("GetTopViewed(%d) executed %d queries, want 0", limit, got)
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
