package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks/database/types"
)

// mockDB implements database.Interface for testing
type mockDB struct {
	queryRowFunc func(ctx context.Context, query string, args ...any) types.Row
	queryFunc    func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	execFunc     func(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (m *mockDB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, query, args...)
	}
	return nil, errors.New("queryFunc not implemented")
}

func (m *mockDB) QueryRow(ctx context.Context, query string, args ...any) types.Row {
	if m.queryRowFunc != nil {
		return m.queryRowFunc(ctx, query, args...)
	}
	return &mockRow{err: errors.New("queryRowFunc not implemented")}
}

func (m *mockDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, query, args...)
	}
	return nil, errors.New("execFunc not implemented")
}

func (m *mockDB) Prepare(ctx context.Context, query string) (types.Statement, error) {
	return nil, errors.New("not implemented")
}

func (m *mockDB) Begin(ctx context.Context) (types.Tx, error) {
	return nil, errors.New("not implemented")
}

func (m *mockDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (types.Tx, error) {
	return nil, errors.New("not implemented")
}

func (m *mockDB) Health(ctx context.Context) error {
	return nil
}

func (m *mockDB) Stats() (map[string]any, error) {
	return nil, nil
}

func (m *mockDB) Close() error {
	return nil
}

func (m *mockDB) DatabaseType() string {
	return "mock"
}

func (m *mockDB) GetMigrationTable() string {
	return "migrations"
}

func (m *mockDB) CreateMigrationTable(ctx context.Context) error {
	return nil
}

// mockRow implements types.Row for testing
type mockRow struct {
	scanFunc func(dest ...any) error
	err      error
}

func (m *mockRow) Scan(dest ...any) error {
	if m.scanFunc != nil {
		return m.scanFunc(dest...)
	}
	return m.err
}

func (m *mockRow) Err() error {
	return m.err
}

// mockResult implements sql.Result for testing
type mockResult struct {
	rowsAffected int64
	lastInsertID int64
}

func (m *mockResult) LastInsertId() (int64, error) {
	return m.lastInsertID, nil
}

func (m *mockResult) RowsAffected() (int64, error) {
	return m.rowsAffected, nil
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	product := domain.New("test-id", "Test Product", "Description", 99.99, "https://example.com/image.jpg")

	tests := []struct {
		name    string
		product *domain.Product
		execErr error
		wantErr bool
	}{
		{
			name:    "successful create",
			product: product,
			execErr: nil,
			wantErr: false,
		},
		{
			name:    "database error",
			product: product,
			execErr: errors.New("database error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDB{
				execFunc: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					return &mockResult{rowsAffected: 1}, tt.execErr
				},
			}

			getDB := func(ctx context.Context) (types.Interface, error) {
				return mock, nil
			}

			repo := NewSQLProductRepository(getDB)
			err := repo.Create(ctx, tt.product)

			if (err != nil) != tt.wantErr {
				t.Errorf("Create() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetByID(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	tests := []struct {
		name      string
		id        string
		scanFunc  func(dest ...any) error
		wantErr   error
		wantName  string
		wantPrice float64
	}{
		{
			name: "successful get",
			id:   "test-id",
			scanFunc: func(dest ...any) error {
				*dest[0].(*string) = "test-id"
				*dest[1].(*string) = "Test Product"
				*dest[2].(*string) = "Description"
				*dest[3].(*float64) = 99.99
				*dest[4].(*string) = "https://example.com/image.jpg"
				*dest[5].(*time.Time) = now
				*dest[6].(*time.Time) = now
				return nil
			},
			wantErr:   nil,
			wantName:  "Test Product",
			wantPrice: 99.99,
		},
		{
			name: "product not found",
			id:   "missing-id",
			scanFunc: func(dest ...any) error {
				return sql.ErrNoRows
			},
			wantErr: ErrProductNotFound,
		},
		{
			name: "database error",
			id:   "test-id",
			scanFunc: func(dest ...any) error {
				return errors.New("database error")
			},
			wantErr: errors.New("database error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDB{
				queryRowFunc: func(ctx context.Context, query string, args ...any) types.Row {
					return &mockRow{scanFunc: tt.scanFunc}
				},
			}

			getDB := func(ctx context.Context) (types.Interface, error) {
				return mock, nil
			}

			repo := NewSQLProductRepository(getDB)
			product, err := repo.GetByID(ctx, tt.id)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("GetByID() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.wantErr == ErrProductNotFound && err != ErrProductNotFound {
					t.Errorf("GetByID() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("GetByID() unexpected error = %v", err)
				return
			}

			if product.Name != tt.wantName {
				t.Errorf("GetByID() name = %v, want %v", product.Name, tt.wantName)
			}
			if product.Price != tt.wantPrice {
				t.Errorf("GetByID() price = %v, want %v", product.Price, tt.wantPrice)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	tests := []struct {
		name         string
		id           string
		updates      map[string]any
		getByIDErr   error
		execErr      error
		rowsAffected int64
		wantErr      error
	}{
		{
			name: "successful update",
			id:   "test-id",
			updates: map[string]any{
				"name":  "Updated Name",
				"price": 149.99,
			},
			getByIDErr:   nil,
			execErr:      nil,
			rowsAffected: 1,
			wantErr:      nil,
		},
		{
			name:       "product not found on get",
			id:         "missing-id",
			updates:    map[string]any{"name": "Updated"},
			getByIDErr: sql.ErrNoRows,
			wantErr:    ErrProductNotFound,
		},
		{
			name: "no rows affected",
			id:   "test-id",
			updates: map[string]any{
				"name": "Updated Name",
			},
			getByIDErr:   nil,
			execErr:      nil,
			rowsAffected: 0,
			wantErr:      ErrProductNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDB{
				queryRowFunc: func(ctx context.Context, query string, args ...any) types.Row {
					if tt.getByIDErr != nil {
						return &mockRow{err: tt.getByIDErr}
					}
					return &mockRow{
						scanFunc: func(dest ...any) error {
							*dest[0].(*string) = "test-id"
							*dest[1].(*string) = "Test Product"
							*dest[2].(*string) = "Description"
							*dest[3].(*float64) = 99.99
							*dest[4].(*string) = "https://example.com/image.jpg"
							*dest[5].(*time.Time) = now
							*dest[6].(*time.Time) = now
							return nil
						},
					}
				},
				execFunc: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					return &mockResult{rowsAffected: tt.rowsAffected}, tt.execErr
				},
			}

			getDB := func(ctx context.Context) (types.Interface, error) {
				return mock, nil
			}

			repo := NewSQLProductRepository(getDB)
			err := repo.Update(ctx, tt.id, tt.updates)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("Update() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("Update() unexpected error = %v", err)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		id           string
		execErr      error
		rowsAffected int64
		wantErr      error
	}{
		{
			name:         "successful delete",
			id:           "test-id",
			execErr:      nil,
			rowsAffected: 1,
			wantErr:      nil,
		},
		{
			name:         "product not found",
			id:           "missing-id",
			execErr:      nil,
			rowsAffected: 0,
			wantErr:      ErrProductNotFound,
		},
		{
			name:         "database error",
			id:           "test-id",
			execErr:      errors.New("database error"),
			rowsAffected: 0,
			wantErr:      errors.New("database error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDB{
				execFunc: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					return &mockResult{rowsAffected: tt.rowsAffected}, tt.execErr
				},
			}

			getDB := func(ctx context.Context) (types.Interface, error) {
				return mock, nil
			}

			repo := NewSQLProductRepository(getDB)
			err := repo.Delete(ctx, tt.id)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("Delete() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.wantErr == ErrProductNotFound && err != ErrProductNotFound {
					t.Errorf("Delete() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("Delete() unexpected error = %v", err)
			}
		})
	}
}
