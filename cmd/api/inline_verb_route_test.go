package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/database"
	dbtest "github.com/gaborage/go-bricks/database/testing"
	dbtypes "github.com/gaborage/go-bricks/database/types"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The activity module's GET /products/:id\:activity is an inline verb (go-bricks
// v0.73.0, C73.10). It sits beside the products module's GET /products/:id, a
// pair the server refused at startup as a duplicate on v0.72.0. This test
// registers both modules' real routes on a real go-bricks server, which admits
// routes the way the one app.Run builds does, starts it, and checks which handler
// each request reaches.
//
// The products handler runs against a fake database that knows no product, so
// reaching it answers 404, and its query log shows the id it was given.
func TestInlineVerbRouteBootsBesideProductByID(t *testing.T) {
	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	db.ExpectQuery("FROM products").WillReturnError(sql.ErrNoRows)

	deps := newModuleTestDeps(t)
	deps.DB = func(context.Context) (database.Interface, error) { return db, nil }

	cfg := *deps.Config
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0 // the OS picks a free port
	srv := server.New(&cfg, deps.Logger)
	hr := server.NewHandlerRegistry(&cfg)

	// In main's order: products registers before activity.
	productsModule, activityModule := products.NewModule(), activity.NewModule()
	for _, mod := range []interface {
		Init(*app.ModuleDeps) error
		RegisterRoutes(*server.HandlerRegistry, server.RouteRegistrar)
	}{productsModule, activityModule} {
		require.NoError(t, mod.Init(deps))
		mod.RegisterRoutes(hr, srv.ModuleGroup())
	}
	require.Empty(t, srv.RouteConflicts(), "the inline verb must be its own route beside GET /products/:id")

	base := startServer(t, srv) + "/api/v1"

	t.Run("the verb reaches the activity slice with the id before the colon", func(t *testing.T) {
		status, body := get(t, base+"/products/abc:activity")
		require.Equal(t, http.StatusOK, status, string(body))
		var resp struct {
			Data struct {
				ProductID string           `json:"productId"`
				Delivered int64            `json:"delivered"`
				Counts    map[string]int64 `json:"counts"`
				Recent    []any            `json:"recent"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(body, &resp))
		assert.Equal(t, "abc", resp.Data.ProductID)
		assert.Zero(t, resp.Data.Delivered, "an unseen id is an empty slice, not a 404")
		assert.NotNil(t, resp.Data.Counts)
		assert.NotNil(t, resp.Data.Recent)
	})

	t.Run("a bare id still reaches products", func(t *testing.T) {
		status, body := get(t, base+"/products/abc")
		assert.Equal(t, http.StatusNotFound, status, string(body))
		assert.Equal(t, "abc", lastQueryArg(t, db))
	})

	t.Run("a percent-encoded colon is not a verb", func(t *testing.T) {
		status, body := get(t, base+"/products/abc%3Aactivity")
		assert.Equal(t, http.StatusNotFound, status, string(body))
		assert.Equal(t, "abc%3Aactivity", lastQueryArg(t, db))
	})

	t.Run("the static segment still wins over both", func(t *testing.T) {
		queries := len(db.QueryLog())
		status, body := get(t, base+"/products/activity")
		require.Equal(t, http.StatusOK, status, string(body))
		var resp struct {
			Data struct {
				SuperStream string `json:"superStream"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(body, &resp))
		assert.Equal(t, "product-activity", resp.Data.SuperStream)
		assert.Len(t, db.QueryLog(), queries, "the snapshot must not reach the products handler")
	})

	// The verb needs a value before it. With none, the whole segment is the
	// parameter value of GET /products/:id.
	t.Run("a verb with no id before it reaches products", func(t *testing.T) {
		status, body := get(t, base+"/products/:activity")
		assert.Equal(t, http.StatusNotFound, status, string(body))
		assert.Equal(t, ":activity", lastQueryArg(t, db))
	})

	// The id's validate tag bounds it at 64 characters: one more is refused
	// before the projection is read.
	t.Run("an id over the max=64 bound fails validation", func(t *testing.T) {
		status, body := get(t, base+"/products/"+strings.Repeat("a", 64)+":activity")
		assert.Equal(t, http.StatusOK, status, string(body))

		status, body = get(t, base+"/products/"+strings.Repeat("a", 65)+":activity")
		assert.Equal(t, http.StatusBadRequest, status, string(body))
	})
}

// startServer runs srv until the test ends and returns its base URL.
func startServer(t *testing.T, srv *server.Server) string {
	t.Helper()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	select {
	case <-srv.ReadyCh():
	case err := <-errCh:
		t.Fatalf("server did not start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not bind within 10s")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, srv.Shutdown(ctx))
		<-errCh
	})
	return "http://" + srv.BoundAddr().String()
}

// get sends one GET and returns the status and the body.
func get(t *testing.T, url string) (status int, body []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// lastQueryArg returns the one argument of the newest query the fake database saw:
// the id the products repository looked up.
func lastQueryArg(t *testing.T, db *dbtest.TestDB) any {
	t.Helper()

	log := db.QueryLog()
	require.NotEmpty(t, log, "the request never reached the products repository")
	last := log[len(log)-1]
	require.Len(t, last.Args, 1)
	return last.Args[0]
}
