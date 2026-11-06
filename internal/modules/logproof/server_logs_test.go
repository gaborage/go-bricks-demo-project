package logproof

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/logproof/handlers"
)

// The token a caller puts in the path. No framework line may print it.
const pathToken = "4111111111111111"

const basePath = "/api/v1"

// TestFrameworkLinesThroughARealServer boots a real go-bricks server on a
// loopback port with the module's routes, sends one request per behavior, and
// reads back the JSON lines the framework logger wrote. It needs no
// infrastructure, so CI proves C73.29, C73.31 and C73.33 on every run;
// make log-correlation-demo proves the same against the full app.
func TestFrameworkLinesThroughARealServer(t *testing.T) {
	lines := runRequests(t)

	// One startup line per engine names the rule (C73.29).
	paths := findLines(lines, func(l map[string]any) bool {
		return strings.HasPrefix(str(l, "message"), "[server.paths]")
	})
	require.Len(t, paths, 1)
	assert.Equal(t, "[server.paths] inbound paths render from the matched route template "+
		"rule=masked_route_template listener=application token=REDACTED unmatched=REDACTED",
		str(paths[0], "message"))

	// No line carries the path token.
	for _, l := range lines {
		raw, err := json.Marshal(l)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), pathToken, "a log line printed the path token")
	}

	t.Run("WARN through the request context keeps an escalated access line", func(t *testing.T) {
		access := accessLine(t, lines, basePath+handlers.WarnPath)
		assert.Equal(t, "warn", str(access, "level"))
		assert.Equal(t, "WARN", str(access, "result_code"))
		assert.EqualValues(t, http.StatusOK, access["http.response.status_code"])
		assert.Equal(t, basePath+"/__sim/observability/warn/REDACTED", str(access, "url.path"))

		warn := findLines(lines, func(l map[string]any) bool { return str(l, "message") == handlers.WarnMessage })
		require.Len(t, warn, 1)
		assert.Equal(t, "warn", str(warn[0], "level"))
		assert.Equal(t, str(access, "request_id"), str(warn[0], "request_id"))
	})

	t.Run("EscalateSeverity keeps an escalated access line", func(t *testing.T) {
		access := accessLine(t, lines, basePath+handlers.EscalatePath)
		assert.Equal(t, "warn", str(access, "level"))
		assert.Equal(t, "WARN", str(access, "result_code"))
		assert.EqualValues(t, http.StatusOK, access["http.response.status_code"])
	})

	t.Run("Panic recovered carries the route and the request's correlation", func(t *testing.T) {
		// The client got a 500 (runRequests checks it). The access line's status and
		// level are deliberately not asserted: at this pin the recover middleware
		// runs inside the request logger, so the access line is written from the
		// uncommitted response (http.response.status_code=200, level=info, the
		// recovered error in its error field) before echo's error handler writes
		// the 500. Only its masked path and request id are
		// pinned here.
		access := accessLine(t, lines, basePath+handlers.PanicPath)
		assert.Equal(t, basePath+"/__sim/observability/panic/REDACTED", str(access, "url.path"))
		requestID := str(access, "request_id")
		require.NotEmpty(t, requestID)

		panics := findLines(lines, func(l map[string]any) bool { return str(l, "message") == "Panic recovered" })
		require.Len(t, panics, 1)
		assert.Equal(t, basePath+handlers.PanicPath, str(panics[0], "http.route"))
		assert.Equal(t, requestID, str(panics[0], "request_id"))
		assert.Equal(t, requestID, str(panics[0], "correlation_id"))
	})

	t.Run("an unmatched path prints the bare token", func(t *testing.T) {
		unmatched := findLines(lines, func(l map[string]any) bool {
			return str(l, "log.type") == "action" && l["http.response.status_code"] == float64(http.StatusNotFound)
		})
		require.Len(t, unmatched, 1)
		assert.Equal(t, "REDACTED", str(unmatched[0], "url.path"))
	})
}

// runRequests starts the server, sends the four requests, stops it, and returns
// every JSON line the logger wrote meanwhile.
func runRequests(t *testing.T) []map[string]any {
	t.Helper()

	logFile, err := os.CreateTemp(t.TempDir(), "server-*.log")
	require.NoError(t, err)

	// logger.New binds os.Stdout as it is at construction, so the swap lasts only
	// for that call.
	stdout := os.Stdout
	os.Stdout = logFile
	log := logger.New("info", false)
	os.Stdout = stdout

	cfg := &config.Config{
		App: config.AppConfig{Name: "logproof-test", Version: "1.0.0", Env: config.EnvDevelopment},
		Server: config.ServerConfig{
			Host:    "127.0.0.1",
			Port:    0,
			Timeout: config.TimeoutConfig{Read: 5 * time.Second, Write: 5 * time.Second},
			Path:    config.PathConfig{Base: basePath, Health: "/health", Ready: "/ready"},
		},
	}

	m := NewModule()
	require.NoError(t, m.Init(&app.ModuleDeps{Config: cfg, Logger: log}))
	srv := server.New(cfg, log)
	m.RegisterRoutes(server.NewHandlerRegistry(cfg), srv.ModuleGroup())

	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start() }()
	select {
	case <-srv.ReadyCh():
	case err := <-startErr:
		t.Fatalf("server did not start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start within 10s")
	}

	base := "http://" + srv.BoundAddr().String()
	// No keep-alive: every connection is closed when its response is read, so
	// Shutdown has nothing to drain.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}
	for path, want := range map[string]int{
		basePath + "/__sim/observability/warn/" + pathToken:     http.StatusOK,
		basePath + "/__sim/observability/escalate/" + pathToken: http.StatusOK,
		basePath + "/__sim/observability/panic/" + pathToken:    http.StatusInternalServerError,
		"/not-a-route/" + pathToken:                             http.StatusNotFound,
	} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, want, resp.StatusCode, path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
	if err := <-startErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("server stopped with an error: %v", err)
	}

	return readJSONLines(t, logFile.Name())
}

func readJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var lines []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var l map[string]any
		if json.Unmarshal(sc.Bytes(), &l) == nil {
			lines = append(lines, l)
		}
	}
	require.NoError(t, sc.Err())
	return lines
}

func accessLine(t *testing.T, lines []map[string]any, route string) map[string]any {
	t.Helper()
	found := findLines(lines, func(l map[string]any) bool {
		return str(l, "log.type") == "action" && str(l, "http.route") == route
	})
	require.Len(t, found, 1, "access lines for %s", route)
	return found[0]
}

func findLines(lines []map[string]any, keep func(map[string]any) bool) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

func str(l map[string]any, key string) string {
	s, _ := l[key].(string)
	return s
}
