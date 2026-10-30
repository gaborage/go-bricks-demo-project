package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
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

// productUUID is a well-formed product id that names no product in the fake
// database. A request that reaches the products repository with it answers 404.
const productUUID = "3f1c2a9e-5b7d-4c1e-9a2f-6d8e0b4c7a11"

// productsHarness is a real go-bricks server running the products and activity
// modules' real routes. The products module talks to a fake database that knows
// no product, so a request that reaches the repository answers 404 and leaves
// the id it looked up in the query log. dbCalls counts every time a handler
// asked for the database at all.
type productsHarness struct {
	base    string
	db      *dbtest.TestDB
	dbCalls *atomic.Int32
}

func newProductsHarness(t *testing.T) productsHarness {
	t.Helper()

	db := dbtest.NewTestDB(dbtypes.PostgreSQL)
	db.ExpectQuery("FROM products").WillReturnError(sql.ErrNoRows)
	dbCalls := &atomic.Int32{}

	deps := newModuleTestDeps(t)
	deps.DB = func(context.Context) (database.Interface, error) {
		dbCalls.Add(1)
		return db, nil
	}

	cfg := *deps.Config
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0 // the OS picks a free port
	srv := server.New(&cfg, deps.Logger)
	hr := server.NewHandlerRegistry(&cfg)

	// In main's order: products registers before activity.
	for _, mod := range []interface {
		Init(*app.ModuleDeps) error
		RegisterRoutes(*server.HandlerRegistry, server.RouteRegistrar)
	}{products.NewModule(), activity.NewModule()} {
		require.NoError(t, mod.Init(deps))
		mod.RegisterRoutes(hr, srv.ModuleGroup())
	}
	require.Empty(t, srv.RouteConflicts(), "the inline verb must be its own route beside GET /products/:id")

	return productsHarness{base: startServer(t, srv) + "/api/v1", db: db, dbCalls: dbCalls}
}

// The activity module's GET /products/:id\:activity is an inline verb (go-bricks
// v0.73.0, C73.10). It sits beside the products module's GET /products/:id, a
// pair the server refused at startup as a duplicate on v0.72.0. This test boots
// both on a real server, which admits routes the way the one app.Run builds
// does, and checks which handler each request reaches.
func TestInlineVerbRouteBootsBesideProductByID(t *testing.T) {
	h := newProductsHarness(t)

	t.Run("the verb reaches the activity slice with the id before the colon", func(t *testing.T) {
		status, body := get(t, h.base+"/products/"+productUUID+":activity")
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
		assert.Equal(t, productUUID, resp.Data.ProductID)
		assert.Zero(t, resp.Data.Delivered, "an unseen id is an empty slice, not a 404")
		assert.NotNil(t, resp.Data.Counts)
		assert.NotNil(t, resp.Data.Recent)
	})

	t.Run("a bare id still reaches products", func(t *testing.T) {
		status, body := get(t, h.base+"/products/"+productUUID)
		assert.Equal(t, http.StatusNotFound, status, string(body))
		assert.Equal(t, productUUID, lastQueryArg(t, h.db))
	})

	t.Run("the static segment still wins over both", func(t *testing.T) {
		calls := h.dbCalls.Load()
		status, body := get(t, h.base+"/products/activity")
		require.Equal(t, http.StatusOK, status, string(body))
		var resp struct {
			Data struct {
				SuperStream string `json:"superStream"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(body, &resp))
		assert.Equal(t, "product-activity", resp.Data.SuperStream)
		assert.Equal(t, calls, h.dbCalls.Load(), "the snapshot must not reach the products handler")
	})

	// Everything below misses the verb route, so GET /products/:id gets the
	// whole segment as its id. That id is no UUID, so the products validator
	// refuses it with 400 before the repository runs. A percent-encoded colon is
	// not a verb, so even a valid UUID before it is refused here: the verb route
	// would have answered 200.
	for _, tc := range []struct{ name, path string }{
		{"not a UUID", "/products/abc"},
		{"a percent-encoded colon is not a verb", "/products/abc%3Aactivity"},
		{"a percent-encoded colon after a UUID is not a verb", "/products/" + productUUID + "%3Aactivity"},
		{"a verb with no id before it", "/products/:activity"},
		{"a misspelt verb", "/products/abc:activty"},
	} {
		t.Run(tc.name+" reaches products and is refused before the database", func(t *testing.T) {
			calls, queries := h.dbCalls.Load(), len(h.db.QueryLog())
			status, body := get(t, h.base+tc.path)
			assert.Equal(t, http.StatusBadRequest, status, string(body))
			assert.Equal(t, calls, h.dbCalls.Load(), "a malformed id must not reach the database")
			assert.Len(t, h.db.QueryLog(), queries, "a malformed id must not reach a query")
		})
	}

	// The verb route's id is bounded at 64 characters: one more is refused
	// before the projection is read.
	t.Run("an id over the max=64 bound fails validation", func(t *testing.T) {
		status, body := get(t, h.base+"/products/"+strings.Repeat("a", 64)+":activity")
		assert.Equal(t, http.StatusOK, status, string(body))

		status, body = get(t, h.base+"/products/"+strings.Repeat("a", 65)+":activity")
		assert.Equal(t, http.StatusBadRequest, status, string(body))
	})
}

// A product id is validated as a UUID at the HTTP boundary, on every route that
// takes one. Before, a malformed id reached PostgreSQL's UUID column and the
// request answered 500. GET is covered above; this drives PUT and DELETE
// through the same real validation path.
func TestMalformedProductIDIsRefusedBeforeTheDatabase(t *testing.T) {
	h := newProductsHarness(t)

	for _, tc := range []struct {
		method string
		body   string
	}{
		{http.MethodPut, `{"name":"renamed","price":1}`},
		{http.MethodDelete, ""},
	} {
		t.Run(tc.method, func(t *testing.T) {
			status, body := send(t, tc.method, h.base+"/products/not-a-uuid", tc.body)
			assert.Equal(t, http.StatusBadRequest, status, string(body))
			assert.Zero(t, h.dbCalls.Load(), "a malformed id must not reach the database")
			assert.Empty(t, h.db.QueryLog(), "a malformed id must not reach a query")
			assert.Empty(t, h.db.ExecLog(), "a malformed id must not reach a statement")
		})
	}
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
	return send(t, http.MethodGet, url, "")
}

// send sends one request, with a JSON body when one is given, and returns the
// status and the response body.
func send(t *testing.T, method, url, payload string) (status int, body []byte) {
	t.Helper()

	var reqBody io.Reader = http.NoBody
	if payload != "" {
		reqBody = strings.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, reqBody)
	require.NoError(t, err)
	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}
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
