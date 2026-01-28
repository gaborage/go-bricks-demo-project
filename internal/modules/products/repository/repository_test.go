package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks/database"
	dbtest "github.com/gaborage/go-bricks/database/testing"
	dbtypes "github.com/gaborage/go-bricks/database/types"
)

func TestCreate(t *testing.T) {
	ctx := context.Background()
	product := domain.New("test-id", "Test Product", "Description", 99.99, "https://example.com/image.jpg")

	t.Run("successful create", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectExec("INSERT INTO products").WillReturnRowsAffected(1)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Create(ctx, product)

		if err != nil {
			t.Errorf("Create() unexpected error = %v", err)
		}
		dbtest.AssertExecExecuted(t, db, "INSERT")
	})

	t.Run("database error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectExec("INSERT INTO products").WillReturnError(errors.New("database error"))

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Create(ctx, product)

		if err == nil {
			t.Error("Create() expected error, got nil")
		}
	})
}

func TestGetByID(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("successful get", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT").
			WillReturnRows(
				dbtest.NewRowSet("id", "name", "description", "price", "image_url", "created_date", "updated_date").
					AddRow("test-id", "Test Product", "Description", 99.99, "https://example.com/image.jpg", now, now),
			)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		product, err := repo.GetByID(ctx, "test-id")

		if err != nil {
			t.Errorf("GetByID() unexpected error = %v", err)
			return
		}
		if product.Name != "Test Product" {
			t.Errorf("GetByID() name = %v, want %v", product.Name, "Test Product")
		}
		if product.Price != 99.99 {
			t.Errorf("GetByID() price = %v, want %v", product.Price, 99.99)
		}
		dbtest.AssertQueryExecuted(t, db, "SELECT")
	})

	t.Run("product not found", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT").WillReturnError(sql.ErrNoRows)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		_, err := repo.GetByID(ctx, "missing-id")

		if !errors.Is(err, ErrProductNotFound) {
			t.Errorf("GetByID() error = %v, want %v", err, ErrProductNotFound)
		}
	})

	t.Run("database error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT").WillReturnError(errors.New("database error"))

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		_, err := repo.GetByID(ctx, "test-id")

		if err == nil {
			t.Error("GetByID() expected error, got nil")
		}
	})
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("successful update", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		// First call: GetByID check (SELECT)
		db.ExpectQuery("SELECT").
			WillReturnRows(
				dbtest.NewRowSet("id", "name", "description", "price", "image_url", "created_date", "updated_date").
					AddRow("test-id", "Test Product", "Description", 99.99, "https://example.com/image.jpg", now, now),
			)
		// Second call: UPDATE
		db.ExpectExec("UPDATE products").WillReturnRowsAffected(1)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Update(ctx, "test-id", map[string]any{
			"name":  "Updated Name",
			"price": 149.99,
		})

		if err != nil {
			t.Errorf("Update() unexpected error = %v", err)
		}
		dbtest.AssertExecExecuted(t, db, "UPDATE")
	})

	t.Run("product not found on get", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT").WillReturnError(sql.ErrNoRows)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Update(ctx, "missing-id", map[string]any{"name": "Updated"})

		if !errors.Is(err, ErrProductNotFound) {
			t.Errorf("Update() error = %v, want %v", err, ErrProductNotFound)
		}
	})

	t.Run("no rows affected", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectQuery("SELECT").
			WillReturnRows(
				dbtest.NewRowSet("id", "name", "description", "price", "image_url", "created_date", "updated_date").
					AddRow("test-id", "Test Product", "Description", 99.99, "https://example.com/image.jpg", now, now),
			)
		db.ExpectExec("UPDATE products").WillReturnRowsAffected(0)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Update(ctx, "test-id", map[string]any{"name": "Updated Name"})

		if !errors.Is(err, ErrProductNotFound) {
			t.Errorf("Update() error = %v, want %v", err, ErrProductNotFound)
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("successful delete", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectExec("DELETE FROM products").WillReturnRowsAffected(1)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Delete(ctx, "test-id")

		if err != nil {
			t.Errorf("Delete() unexpected error = %v", err)
		}
		dbtest.AssertExecExecuted(t, db, "DELETE")
	})

	t.Run("product not found", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectExec("DELETE FROM products").WillReturnRowsAffected(0)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Delete(ctx, "missing-id")

		if !errors.Is(err, ErrProductNotFound) {
			t.Errorf("Delete() error = %v, want %v", err, ErrProductNotFound)
		}
	})

	t.Run("database error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectExec("DELETE FROM products").WillReturnError(errors.New("database error"))

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		err := repo.Delete(ctx, "test-id")

		if err == nil {
			t.Error("Delete() expected error, got nil")
		}
	})
}
