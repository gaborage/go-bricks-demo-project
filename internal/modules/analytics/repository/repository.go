// Package repository provides data access for the analytics module using a named database.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/domain"
	"github.com/gaborage/go-bricks/database"
	"github.com/google/uuid"
)

const (
	dbUnavailableErrMsg = "failed to get analytics database connection: %w"

	// productViewsTable is the analytics fact table, named here for the builder
	// query in buildTopViewedQuery. The other two spellings of it stay where they
	// are on purpose: GetViewStats is one hand-written statement whose FROM reads
	// with the rest of the SQL, and RecordView takes the name from
	// entity.TableName(), which the domain entity owns.
	productViewsTable = "product_views"
	// totalViewsAlias is the projected COUNT(*) alias that ORDER BY sorts on.
	totalViewsAlias = "total_views"
)

// Repository defines the interface for analytics data access.
type Repository interface {
	RecordView(ctx context.Context, view *domain.ProductView) error
	GetViewStats(ctx context.Context, productID string) (*domain.ViewStats, error)
	GetTopViewed(ctx context.Context, limit int) ([]*domain.TopProductStats, error)
}

// AnalyticsRepository implements analytics data access using a named database.
// This demonstrates the go-bricks named databases feature by connecting to
// a separate "analytics" database instead of the default application database.
type AnalyticsRepository struct {
	// getDB retrieves the analytics database connection via DBByName.
	// This function is initialized in the module with deps.DBByName(ctx, "analytics").
	getDB func(context.Context) (database.Interface, error)
}

// NewAnalyticsRepository creates a new analytics repository.
// The getDB function should wrap deps.DBByName(ctx, "analytics") to access the named database.
func NewAnalyticsRepository(getDB func(context.Context) (database.Interface, error)) *AnalyticsRepository {
	return &AnalyticsRepository{
		getDB: getDB,
	}
}

// RecordView inserts a new product view event into the analytics database.
func (r *AnalyticsRepository) RecordView(ctx context.Context, view *domain.ProductView) error {
	db, err := r.getDB(ctx)
	if err != nil {
		return fmt.Errorf(dbUnavailableErrMsg, err)
	}

	// Generate UUID for the view event
	view.ID = uuid.New().String()
	entity := view.ToEntity()

	qb := database.NewQueryBuilder(database.PostgreSQL)
	query, args, err := qb.Insert(entity.TableName()).
		Columns("id", "product_id", "viewed_at", "user_agent", "ip_address", "session_id", "referrer").
		Values(entity.ID, entity.ProductID, entity.ViewedAt, entity.UserAgent, entity.IPAddress, entity.SessionID, entity.Referrer).
		ToSQL()
	if err != nil {
		return fmt.Errorf("failed to build insert query: %w", err)
	}

	_, err = db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert product view: %w", err)
	}

	return nil
}

// GetViewStats retrieves aggregated view statistics for a product.
func (r *AnalyticsRepository) GetViewStats(ctx context.Context, productID string) (*domain.ViewStats, error) {
	db, err := r.getDB(ctx)
	if err != nil {
		return nil, fmt.Errorf(dbUnavailableErrMsg, err)
	}

	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startOfWeek := startOfDay.AddDate(0, 0, -int(now.Weekday()))

	// Stays hand-written on purpose: the COUNT(*) FILTER (WHERE viewed_at >= $2)
	// clauses each bind a parameter, and the builder's expression hatch
	// (qb.Expr / qb.MustExpr) carries SQL text only — it has no argument list to
	// attach $2/$3 to. Compare GetTopViewed below, whose aggregate binds nothing
	// and therefore does go through the builder. The placeholders here are still
	// real bind parameters, never interpolated values.
	query := `
		SELECT
			COUNT(*) as total_views,
			COUNT(*) FILTER (WHERE viewed_at >= $2) as views_today,
			COUNT(*) FILTER (WHERE viewed_at >= $3) as views_this_week,
			MAX(viewed_at) as last_viewed_at
		FROM product_views
		WHERE product_id = $1
	`

	var stats domain.ViewStats
	var lastViewedAt *time.Time

	row := db.QueryRow(ctx, query, productID, startOfDay, startOfWeek)
	err = row.Scan(&stats.TotalViews, &stats.ViewsToday, &stats.ViewsThisWeek, &lastViewedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to query view stats: %w", err)
	}

	stats.ProductID = productID
	if lastViewedAt != nil {
		stats.LastViewedAt = *lastViewedAt
	}

	return &stats, nil
}

// buildTopViewedQuery renders the top-viewed aggregate on the type-safe query
// builder: GROUP BY + aliased COUNT(*) + ORDER BY on that alias + LIMIT.
//
// COUNT(*) goes through the declared expression hatch (qb.MustExpr) because, as
// of go-bricks v0.60.0 (ADR-082), every identifier door is validated and a bare
// "COUNT(*)" in Select is rejected at ToSQL time. Both arguments are compile-time
// constants, which is exactly the static-initialization use MustExpr is for.
// "total_views DESC" needs no hatch of its own — a bounded ASC/DESC direction is
// part of the identifier grammar.
//
// Split out from GetTopViewed so a unit test can assert the rendered SQL without
// a database, catching that runtime-rejection class at test time.
//
// limit must be positive. Builder.Limit takes a uint64, so a negative int would
// wrap to ~1.8e19, and a zero is dropped as "unset" — emitting no LIMIT clause
// at all and turning a bounded top-N into an unbounded scan of the fact table.
// Both are refused here rather than clamped, so neither can reach the database.
func buildTopViewedQuery(limit int) (query string, args []any, err error) {
	if limit < 1 {
		return "", nil, fmt.Errorf("top viewed limit must be positive, got %d", limit)
	}

	qb := database.NewQueryBuilder(database.PostgreSQL)
	return qb.Select("product_id", qb.MustExpr("COUNT(*)", totalViewsAlias)).
		From(productViewsTable).
		GroupBy("product_id").
		OrderBy(totalViewsAlias + " DESC").
		Limit(uint64(limit)).
		ToSQL()
}

// GetTopViewed retrieves the top viewed products.
func (r *AnalyticsRepository) GetTopViewed(ctx context.Context, limit int) ([]*domain.TopProductStats, error) {
	// A non-positive limit asks for no rows, so answer it before touching the
	// database — resolving a connection for a query that will never run is
	// wasted work and would surface a DB-unavailable error for a request whose
	// answer is already known. The two halves are not equivalent to what came
	// before: limit 0 is preserved exactly — the old `LIMIT $1` bound with 0
	// returned zero rows — while a NEGATIVE limit used to reach postgres and
	// fail ("LIMIT must not be negative") and now returns empty instead. That
	// widening is deliberate: asking for fewer than no rows gets the same
	// nothing, and neither value can reach the database as an unlimited query.
	if limit <= 0 {
		return nil, nil
	}

	db, err := r.getDB(ctx)
	if err != nil {
		return nil, fmt.Errorf(dbUnavailableErrMsg, err)
	}

	query, args, err := buildTopViewedQuery(limit)
	if err != nil {
		return nil, fmt.Errorf("failed to build top viewed query: %w", err)
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query top viewed products: %w", err)
	}
	defer rows.Close()

	var results []*domain.TopProductStats
	for rows.Next() {
		var stat domain.TopProductStats
		if err := rows.Scan(&stat.ProductID, &stat.TotalViews); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, &stat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}
