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
		ToSql()
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

	// Query to get total views, views today, views this week, and last viewed time.
	// Using raw SQL here for the aggregate functions with FILTER clauses.
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

// GetTopViewed retrieves the top viewed products.
func (r *AnalyticsRepository) GetTopViewed(ctx context.Context, limit int) ([]*domain.TopProductStats, error) {
	db, err := r.getDB(ctx)
	if err != nil {
		return nil, fmt.Errorf(dbUnavailableErrMsg, err)
	}

	// Query to get top viewed products with their view counts.
	query := `
		SELECT product_id, COUNT(*) as total_views
		FROM product_views
		GROUP BY product_id
		ORDER BY total_views DESC
		LIMIT $1
	`

	rows, err := db.Query(ctx, query, limit)
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
