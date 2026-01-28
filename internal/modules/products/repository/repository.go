package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks/database"
	dbtypes "github.com/gaborage/go-bricks/database/types"
)

var (
	ErrProductNotFound = errors.New("product not found")
)

// Repository defines the interface for product data access
type Repository interface {
	Create(ctx context.Context, product *domain.Product) error
	GetByID(ctx context.Context, id string) (*domain.Product, error)
	List(ctx context.Context, limit, offset int) ([]*domain.Product, int, error)
	Update(ctx context.Context, id string, updates map[string]any) error
	Delete(ctx context.Context, id string) error
}

const (
	dbUnavailableErrMsg = "failed to get database connection: %w"
)

type ProductRepository struct {
	getDB func(context.Context) (database.Interface, error)
	cols  dbtypes.Columns // Cached column metadata for type-safe queries
}

func NewSQLProductRepository(getDB func(context.Context) (database.Interface, error)) *ProductRepository {
	qb := database.NewQueryBuilder(database.PostgreSQL)
	return &ProductRepository{
		getDB: getDB,
		cols:  qb.Columns(&domain.ProductEntity{}), // Cache once at construction
	}
}

// Create inserts a new product into the database using type-safe InsertStruct
func (r *ProductRepository) Create(ctx context.Context, product *domain.Product) error {
	db, err := r.getDB(ctx)
	if err != nil {
		return fmt.Errorf(dbUnavailableErrMsg, err)
	}

	entity := domain.ToProductEntity(product)

	// Use InsertStruct for type-safe, vendor-aware INSERT generation
	qb := database.NewQueryBuilder(database.PostgreSQL)
	query, args, err := qb.InsertStruct(entity.TableName(), entity).ToSql()
	if err != nil {
		return fmt.Errorf("failed to build insert query: %w", err)
	}

	_, err = db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert product: %w", err)
	}

	return nil
}

// GetByID retrieves a product by its ID using type-safe column references
func (r *ProductRepository) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	db, err := r.getDB(ctx)
	if err != nil {
		return nil, fmt.Errorf(dbUnavailableErrMsg, err)
	}

	qb := database.NewQueryBuilder(database.PostgreSQL)
	f := qb.Filter()

	// Use cols.All() for type-safe column selection and cols.Col() for filter
	query, args, err := qb.Select(r.cols.All()).
		From("products").
		Where(f.Eq(r.cols.Col("ID"), id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("failed to build select query: %w", err)
	}

	var entity domain.ProductEntity
	row := db.QueryRow(ctx, query, args...)
	err = row.Scan(
		&entity.ID,
		&entity.Name,
		&entity.Description,
		&entity.Price,
		&entity.ImageURL,
		&entity.CreatedDate,
		&entity.UpdatedDate,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("failed to scan product: %w", err)
	}

	return domain.ToProduct(&entity), nil
}

// List retrieves a paginated list of products with total count using type-safe columns
func (r *ProductRepository) List(ctx context.Context, limit, offset int) ([]*domain.Product, int, error) {
	db, err := r.getDB(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf(dbUnavailableErrMsg, err)
	}

	qb := database.NewQueryBuilder(database.PostgreSQL)

	// First, get total count
	countQuery, countArgs, err := qb.Select("COUNT(*)").
		From("products").
		ToSQL()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to build count query: %w", err)
	}

	var total int
	countRow := db.QueryRow(ctx, countQuery, countArgs...)
	if err := countRow.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to get total count: %w", err)
	}

	// Use cols.All() for type-safe column selection and cols.Col() for ordering
	query, args, err := qb.Select(r.cols.All()).
		From("products").
		OrderBy(r.cols.Col("CreatedDate") + " DESC").
		Limit(uint64(limit)).
		Offset(uint64(offset)).
		ToSQL()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to build list query: %w", err)
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query products: %w", err)
	}
	defer rows.Close()

	var entities []*domain.ProductEntity
	for rows.Next() {
		var entity domain.ProductEntity
		err := rows.Scan(
			&entity.ID,
			&entity.Name,
			&entity.Description,
			&entity.Price,
			&entity.ImageURL,
			&entity.CreatedDate,
			&entity.UpdatedDate,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan product: %w", err)
		}
		entities = append(entities, &entity)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating products: %w", err)
	}

	products := domain.ToProductList(entities)
	return products, total, nil
}

// Update performs a partial update on a product using type-safe column mapping
func (r *ProductRepository) Update(ctx context.Context, id string, updates map[string]any) error {
	db, err := r.getDB(ctx)
	if err != nil {
		return fmt.Errorf(dbUnavailableErrMsg, err)
	}

	// Check if product exists
	_, err = r.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Map JSON field names to type-safe database column names via cols.Col()
	fieldToColumn := map[string]string{
		"name":         r.cols.Col("Name"),
		"description":  r.cols.Col("Description"),
		"price":        r.cols.Col("Price"),
		"image_url":    r.cols.Col("ImageURL"),
		"updated_date": r.cols.Col("UpdatedDate"),
	}

	qb := database.NewQueryBuilder(database.PostgreSQL)
	f := qb.Filter()
	updateBuilder := qb.Update("products")

	// Add each field to update using type-safe column names
	for key, value := range updates {
		if colName, ok := fieldToColumn[key]; ok {
			updateBuilder = updateBuilder.Set(colName, value)
		}
	}

	query, args, err := updateBuilder.
		Where(f.Eq(r.cols.Col("ID"), id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("failed to build update query: %w", err)
	}

	result, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update product: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrProductNotFound
	}

	return nil
}

// Delete removes a product from the database using type-safe column reference
func (r *ProductRepository) Delete(ctx context.Context, id string) error {
	db, err := r.getDB(ctx)
	if err != nil {
		return fmt.Errorf(dbUnavailableErrMsg, err)
	}

	qb := database.NewQueryBuilder(database.PostgreSQL)
	f := qb.Filter()
	query, args, err := qb.Delete("products").
		Where(f.Eq(r.cols.Col("ID"), id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("failed to build delete query: %w", err)
	}

	result, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete product: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrProductNotFound
	}

	return nil
}
