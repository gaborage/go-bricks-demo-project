package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks/database"
	dbtypes "github.com/gaborage/go-bricks/database/types"
)

var (
	ErrProductNotFound = errors.New("product not found")

	// ErrUnknownUpdateField rejects an Update key that maps to no column. A
	// misspelled key used to be skipped silently, so the write reported success
	// while the value was never stored.
	ErrUnknownUpdateField = errors.New("unknown product update field")
)

// Repository defines the interface for product data access
type Repository interface {
	Create(ctx context.Context, product *domain.Product) error
	GetByID(ctx context.Context, id string) (*domain.Product, error)
	List(ctx context.Context, limit, offset int) ([]*domain.Product, int, error)
	Update(ctx context.Context, id string, updates map[string]any) error
	Delete(ctx context.Context, id string) error

	// Transaction-aware variants for use with the transactional outbox pattern.
	// These accept a dbtypes.Tx so the caller can atomically commit business data
	// and outbox events in the same database transaction.
	CreateTx(ctx context.Context, tx dbtypes.Tx, product *domain.Product) error
	DeleteTx(ctx context.Context, tx dbtypes.Tx, id string) error
}

const (
	dbUnavailableErrMsg = "failed to get database connection: %w"
)

type ProductRepository struct {
	getDB func(context.Context) (database.Interface, error)
	cols  dbtypes.Columns  // Cached column metadata for type-safe queries
	now   func() time.Time // Clock for updated_date; tests pin it
}

func NewSQLProductRepository(getDB func(context.Context) (database.Interface, error)) *ProductRepository {
	qb := database.NewQueryBuilder(database.PostgreSQL)
	return &ProductRepository{
		getDB: getDB,
		cols:  qb.Columns(&domain.ProductEntity{}), // Cache once at construction
		now:   func() time.Time { return time.Now().UTC() },
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
	query, args, err := qb.InsertStruct(entity.TableName(), entity).ToSQL()
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
	// SECURITY: Manual SQL review completed - constant COUNT(*) aggregate, no caller input
	countQuery, countArgs, err := qb.Select(qb.MustExpr("COUNT(*)")).
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

// Update performs a partial update on a product using type-safe column mapping.
// updates is keyed by the domain.Field* names; any other key fails with
// ErrUnknownUpdateField before a statement runs. Every update also stamps
// updated_date, so callers never pass it.
func (r *ProductRepository) Update(ctx context.Context, id string, updates map[string]any) error {
	// Build the statement first: a bad key is refused before any round trip.
	query, args, err := r.buildUpdate(id, updates)
	if err != nil {
		return err
	}

	db, err := r.getDB(ctx)
	if err != nil {
		return fmt.Errorf(dbUnavailableErrMsg, err)
	}

	// Check if product exists
	_, err = r.GetByID(ctx, id)
	if err != nil {
		return err
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

// buildUpdate renders the partial UPDATE for Update. Keys are sorted so the same
// input always yields the same SQL.
func (r *ProductRepository) buildUpdate(id string, updates map[string]any) (query string, args []any, err error) {
	if len(updates) == 0 {
		return "", nil, fmt.Errorf("no valid fields to update")
	}

	// Map the update keys (the fields' JSON names) to type-safe database column names
	fieldToColumn := map[string]string{
		domain.FieldName:        r.cols.Col("Name"),
		domain.FieldDescription: r.cols.Col("Description"),
		domain.FieldPrice:       r.cols.Col("Price"),
		domain.FieldImageURL:    r.cols.Col("ImageURL"),
	}

	qb := database.NewQueryBuilder(database.PostgreSQL)
	f := qb.Filter()
	updateBuilder := qb.Update("products")

	for _, key := range slices.Sorted(maps.Keys(updates)) {
		colName, ok := fieldToColumn[key]
		if !ok {
			return "", nil, fmt.Errorf("%w: %q", ErrUnknownUpdateField, key)
		}
		updateBuilder = updateBuilder.Set(colName, updates[key])
	}
	updateBuilder = updateBuilder.Set(r.cols.Col("UpdatedDate"), r.now())

	query, args, err = updateBuilder.
		Where(f.Eq(r.cols.Col("ID"), id)).
		ToSQL()
	if err != nil {
		return "", nil, fmt.Errorf("failed to build update query: %w", err)
	}
	return query, args, nil
}

// Delete removes a product from the database using type-safe column reference
func (r *ProductRepository) Delete(ctx context.Context, id string) error {
	db, err := r.getDB(ctx)
	if err != nil {
		return fmt.Errorf(dbUnavailableErrMsg, err)
	}

	return r.execDelete(ctx, db, id)
}

// CreateTx inserts a new product within an existing transaction.
// Use this with the transactional outbox pattern so the insert and
// outbox event are committed atomically.
func (r *ProductRepository) CreateTx(ctx context.Context, tx dbtypes.Tx, product *domain.Product) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	if product == nil {
		return fmt.Errorf("product is required")
	}

	entity := domain.ToProductEntity(product)

	qb := database.NewQueryBuilder(database.PostgreSQL)
	query, args, err := qb.InsertStruct(entity.TableName(), entity).ToSQL()
	if err != nil {
		return fmt.Errorf("failed to build insert query: %w", err)
	}

	_, err = tx.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert product: %w", err)
	}

	return nil
}

// DeleteTx removes a product within an existing transaction.
// Use this with the transactional outbox pattern so the delete and
// outbox event are committed atomically.
func (r *ProductRepository) DeleteTx(ctx context.Context, tx dbtypes.Tx, id string) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	if id == "" {
		return fmt.Errorf("id is required")
	}
	return r.execDeleteOn(ctx, tx, id)
}

// execDelete runs a DELETE on the given executor (db or tx).
func (r *ProductRepository) execDelete(ctx context.Context, executor dbtypes.Querier, id string) error {
	return r.execDeleteOn(ctx, executor, id)
}

// execDeleteOn builds and executes a DELETE query against any executor.
func (r *ProductRepository) execDeleteOn(ctx context.Context, executor interface {
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
}, id string) error {
	qb := database.NewQueryBuilder(database.PostgreSQL)
	f := qb.Filter()
	query, args, err := qb.Delete("products").
		Where(f.Eq(r.cols.Col("ID"), id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("failed to build delete query: %w", err)
	}

	result, err := executor.Exec(ctx, query, args...)
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
