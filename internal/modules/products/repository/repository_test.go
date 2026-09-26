package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
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
			domain.FieldName:  "Updated Name",
			domain.FieldPrice: 149.99,
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
		err := repo.Update(ctx, "missing-id", map[string]any{domain.FieldName: "Updated"})

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
		err := repo.Update(ctx, "test-id", map[string]any{domain.FieldName: "Updated Name"})

		if !errors.Is(err, ErrProductNotFound) {
			t.Errorf("Update() error = %v, want %v", err, ErrProductNotFound)
		}
	})
}

// TestUpdatePersistsEveryAcceptedField pins the contract the service relies on:
// each key the service writes reaches a column, and updated_date is stamped by
// the repository. The keys used to disagree ("image_url" from the service,
// "imageURL" here), so an image URL change was dropped without an error.
func TestUpdatePersistsEveryAcceptedField(t *testing.T) {
	ctx := context.Background()
	stamp := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	const newURL = "https://example.com/new.png"

	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	db.ExpectQuery("SELECT").
		WillReturnRows(
			dbtest.NewRowSet("id", "name", "description", "price", "image_url", "created_date", "updated_date").
				AddRow("test-id", "Test Product", "Description", 99.99, "https://example.com/old.png", stamp, stamp),
		)
	db.ExpectExec("UPDATE products").WillReturnRowsAffected(1)

	repo := NewSQLProductRepository(func(context.Context) (database.Interface, error) { return db, nil })
	repo.now = func() time.Time { return stamp }

	err := repo.Update(ctx, "test-id", map[string]any{
		domain.FieldName:        "Renamed",
		domain.FieldDescription: "New description",
		domain.FieldPrice:       12.5,
		domain.FieldImageURL:    newURL,
	})
	if err != nil {
		t.Fatalf("Update() unexpected error = %v", err)
	}

	execs := db.ExecLog()
	if len(execs) != 1 {
		t.Fatalf("Update() ran %d statements, want 1", len(execs))
	}
	for _, col := range []string{"name", "description", "price", "image_url", "updated_date"} {
		if !strings.Contains(execs[0].SQL, col+" = ") {
			t.Errorf("UPDATE does not set %s: %s", col, execs[0].SQL)
		}
	}
	for _, want := range []any{"Renamed", "New description", 12.5, newURL, stamp, "test-id"} {
		if !slices.Contains(execs[0].Args, want) {
			t.Errorf("UPDATE args %v lack %v", execs[0].Args, want)
		}
	}
}

func TestUpdateStampsUpdatedDateOnSingleFieldChange(t *testing.T) {
	ctx := context.Background()
	stamp := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	db.ExpectQuery("SELECT").
		WillReturnRows(
			dbtest.NewRowSet("id", "name", "description", "price", "image_url", "created_date", "updated_date").
				AddRow("test-id", "Test Product", "Description", 99.99, "", stamp, stamp),
		)
	db.ExpectExec("UPDATE products").WillReturnRowsAffected(1)

	repo := NewSQLProductRepository(func(context.Context) (database.Interface, error) { return db, nil })
	repo.now = func() time.Time { return stamp }

	if err := repo.Update(ctx, "test-id", map[string]any{domain.FieldImageURL: "https://example.com/x.png"}); err != nil {
		t.Fatalf("Update() unexpected error = %v", err)
	}

	execs := db.ExecLog()
	if len(execs) != 1 || !strings.Contains(execs[0].SQL, "image_url = ") || !strings.Contains(execs[0].SQL, "updated_date = ") {
		t.Fatalf("UPDATE = %v, want image_url and updated_date set", execs)
	}
	if !slices.Contains(execs[0].Args, any(stamp)) {
		t.Errorf("UPDATE args %v lack the updated_date stamp %v", execs[0].Args, stamp)
	}
}

func TestUpdateRejectsUnknownField(t *testing.T) {
	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	repo := NewSQLProductRepository(func(context.Context) (database.Interface, error) { return db, nil })

	for _, key := range []string{"image_url", "updated_date", "updatedDate"} {
		err := repo.Update(context.Background(), "test-id", map[string]any{key: "x"})
		if !errors.Is(err, ErrUnknownUpdateField) {
			t.Errorf("Update(%q) error = %v, want %v", key, err, ErrUnknownUpdateField)
		}
	}
	if n := len(db.QueryLog()) + len(db.ExecLog()); n != 0 {
		t.Errorf("a refused update reached the database %d time(s)", n)
	}
}

func TestCreateTx(t *testing.T) {
	ctx := context.Background()
	product := domain.New("tx-id", "Tx Product", "Description", 49.99, "")

	t.Run("successful create within transaction", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		tx := db.ExpectTransaction().
			ExpectExec("INSERT INTO products").WillReturnRowsAffected(1)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		realTx, err := db.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}

		err = repo.CreateTx(ctx, realTx, product)
		if err != nil {
			t.Errorf("CreateTx() unexpected error = %v", err)
		}

		if !tx.IsCommitted() && !tx.IsRolledBack() {
			// Transaction is still open (caller manages commit) — that's expected
		}
	})

	t.Run("database error within transaction", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		// Use WillReturnRowsAffected(0) to simulate failure — the insert
		// itself returns an error from the mock exec when no expectation matches
		db.ExpectTransaction().
			ExpectExec("INSERT INTO products").WillReturnRowsAffected(0)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		realTx, err := db.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}

		// CreateTx should succeed even with 0 rows — INSERT doesn't check affected rows
		err = repo.CreateTx(ctx, realTx, product)
		if err != nil {
			t.Errorf("CreateTx() unexpected error = %v", err)
		}
	})

	t.Run("nil transaction returns error", func(t *testing.T) {
		getDB := func(ctx context.Context) (database.Interface, error) {
			return nil, nil
		}
		repo := NewSQLProductRepository(getDB)
		err := repo.CreateTx(ctx, nil, product)
		if err == nil {
			t.Error("CreateTx() expected error for nil tx")
		}
	})

	t.Run("nil product returns error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		tx := db.ExpectTransaction()
		_ = tx

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}
		repo := NewSQLProductRepository(getDB)
		realTx, err := db.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}

		err = repo.CreateTx(ctx, realTx, nil)
		if err == nil {
			t.Error("CreateTx() expected error for nil product")
		}
	})
}

func TestDeleteTx(t *testing.T) {
	ctx := context.Background()

	t.Run("successful delete within transaction", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectTransaction().
			ExpectExec("DELETE FROM products").WillReturnRowsAffected(1)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}

		err = repo.DeleteTx(ctx, tx, "tx-delete-id")
		if err != nil {
			t.Errorf("DeleteTx() unexpected error = %v", err)
		}
	})

	t.Run("not found within transaction", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectTransaction().
			ExpectExec("DELETE FROM products").WillReturnRowsAffected(0)

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}

		repo := NewSQLProductRepository(getDB)
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}

		err = repo.DeleteTx(ctx, tx, "missing-id")
		if !errors.Is(err, ErrProductNotFound) {
			t.Errorf("DeleteTx() error = %v, want %v", err, ErrProductNotFound)
		}
	})

	t.Run("nil transaction returns error", func(t *testing.T) {
		getDB := func(ctx context.Context) (database.Interface, error) {
			return nil, nil
		}
		repo := NewSQLProductRepository(getDB)
		err := repo.DeleteTx(ctx, nil, "some-id")
		if err == nil {
			t.Error("DeleteTx() expected error for nil tx")
		}
	})

	t.Run("empty id returns error", func(t *testing.T) {
		db := dbtest.NewTestDB(dbtypes.PostgreSQL)
		db.ExpectTransaction()

		getDB := func(ctx context.Context) (database.Interface, error) {
			return db, nil
		}
		repo := NewSQLProductRepository(getDB)
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}

		err = repo.DeleteTx(ctx, tx, "")
		if err == nil {
			t.Error("DeleteTx() expected error for empty id")
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
