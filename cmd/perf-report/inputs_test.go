package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifest(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
		check   func(*testing.T, *manifest)
	}{
		{name: "labels and aa", body: `{"labels":["one","two"],"aa":{"one-aa":"one"},"extra":{"k6":"x"}}`,
			check: func(t *testing.T, m *manifest) {
				if strings.Join(m.allLabels(), ",") != "one,two,one-aa" {
					t.Errorf("allLabels = %v", m.allLabels())
				}
			}},
		{name: "missing labels", body: `{"aa":{}}`, wantErr: `"labels"`},
		{name: "empty labels", body: `{"labels":[]}`, wantErr: `"labels"`},
		{name: "aa points at an unknown label", body: `{"labels":["one"],"aa":{"x-aa":"x"}}`, wantErr: "not in labels"},
		{name: "duplicate label", body: `{"labels":["one","one"]}`, wantErr: "appears twice"},
		{name: "aa label reuses a label", body: `{"labels":["one","two"],"aa":{"two":"one"}}`, wantErr: "appears twice"},
		{name: "path in a label", body: `{"labels":["../one"]}`, wantErr: "plain directory name"},
		{name: "optional key of the wrong shape warns", body: `{"labels":["one"],"notes":"not a list"}`,
			check: func(t *testing.T, m *manifest) {
				if len(m.warnings) != 1 || !strings.Contains(m.warnings[0], `"notes"`) {
					t.Errorf("warnings = %v", m.warnings)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, manifestFile), tt.body)
			m, err := loadManifest(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, m)
		})
	}
}

func TestLoadWindows(t *testing.T) {
	row := "a-S1-r1\tS1\t1\t2026-01-01T10:00:00Z\t2026-01-01T10:02:00Z\t2026-01-01T10:12:00Z\t0\t1\t"
	tests := []struct {
		name    string
		body    string
		wantErr string
		wantN   int
	}{
		{name: "header and one row", body: windowsHead + "\n" + row + "\n", wantN: 1},
		{name: "CRLF line endings", body: windowsHead + "\r\n" + row + "\r\n", wantN: 1},
		{name: "void row with reasons", body: windowsHead + "\n" + strings.Replace(row, "\t0\t1\t", "\t3\t0\tdropped;429", 1) + "\n", wantN: 1},
		{name: "wrong header", body: "run_id\tscenario\n" + row, wantErr: "header must be exactly"},
		{name: "short row", body: windowsHead + "\na-S1-r1\tS1\t1\n", wantErr: "want 9 tab-separated fields"},
		{name: "bad valid", body: windowsHead + "\n" + strings.Replace(row, "\t0\t1\t", "\t0\tyes\t", 1), wantErr: "valid must be 1 or 0"},
		{name: "bad time", body: windowsHead + "\n" + strings.Replace(row, "2026-01-01T10:02:00Z", "10:02", 1), wantErr: "time"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), windowsFile)
			writeFile(t, path, tt.body)
			rows, found, err := loadWindows(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !found || len(rows) != tt.wantN {
				t.Fatalf("rows = %v, found = %v, err = %v", rows, found, err)
			}
		})
	}
	if _, found, err := loadWindows(filepath.Join(t.TempDir(), windowsFile)); found || err != nil {
		t.Errorf("missing file: found = %v, err = %v; want false, nil", found, err)
	}
}

func TestNRResultShapes(t *testing.T) {
	contract := jsonDoc{"data": map[string]any{"actor": map[string]any{"account": map[string]any{"nrql": map[string]any{
		fieldResults: []any{map[string]any{fieldN: 10.0}, map[string]any{fieldN: 5.0}},
	}}}}}
	if n, ok := sumField(nrResults(contract), "n"); !ok || n != 15 {
		t.Errorf("contract shape: n = %v, %v; want 15, true", n, ok)
	}
	aliased := jsonDoc{"data": map[string]any{"actor": map[string]any{"account": map[string]any{
		"b": map[string]any{fieldResults: []any{map[string]any{"mcores": 2.0}}},
		"a": map[string]any{fieldResults: []any{map[string]any{fieldRPS: 1.0}}},
	}}}}
	rows := nrResults(aliased)
	if len(rows) != 2 || rows[0][fieldRPS] != 1.0 {
		t.Errorf("aliased shape rows = %v, want the a alias first", rows)
	}
	if rows := nrResults(jsonDoc{"errors": []any{"boom"}}); len(rows) != 0 {
		t.Errorf("error response rows = %v, want none", rows)
	}

	const routeB = "/b"
	routes := []struct {
		row  map[string]any
		want string
	}{
		{row: map[string]any{fieldRoute: "/a", fieldFacet: routeB}, want: "/a"},
		{row: map[string]any{fieldFacet: routeB}, want: routeB},
		{row: map[string]any{fieldFacet: []any{"/c", "GET"}}, want: "/c"},
		{row: map[string]any{fieldN: 1.0}, want: ""},
	}
	for _, r := range routes {
		if got := routeOf(r.row); got != r.want {
			t.Errorf("routeOf(%v) = %q, want %q", r.row, got, r.want)
		}
	}

	const q95 = "95"
	percentiles := []struct {
		row  map[string]any
		q    string
		want float64
		ok   bool
	}{
		{row: map[string]any{"p" + q95: 12.0}, q: q95, want: 12, ok: true},
		{row: map[string]any{"percentile.http.server.request.duration": map[string]any{"50": 3.0, q95: 9.0}}, q: q95, want: 9, ok: true},
		{row: map[string]any{"percentile.x": map[string]any{"50": 3.0}}, q: "99", ok: false},
	}
	for _, p := range percentiles {
		got, ok := percentile(p.row, p.q)
		if ok != p.ok || (ok && got != p.want) {
			t.Errorf("percentile(%v, %s) = %v, %v; want %v, %v", p.row, p.q, got, ok, p.want, p.ok)
		}
	}
}

func TestReadK6ToleratesMissingMetrics(t *testing.T) {
	r := &rep{vals: map[string]float64{}}
	readK6(r, jsonDoc{"perfMeta": map[string]any{"rate": nil, "steps": nil}})
	if len(r.vals) != 0 {
		t.Errorf("vals = %v, want none", r.vals)
	}
	if !math.IsNaN(r.meta.rate) || r.meta.steps != nil {
		t.Errorf("meta = %+v, want NaN rate and no steps", r.meta)
	}
	r = &rep{vals: map[string]float64{}}
	readK6(r, jsonDoc{"metrics": map[string]any{
		"http_req_duration": map[string]any{"values": map[string]any{"med": 4.0, "p(95)": 9.0}},
	}})
	if r.vals[mK6P50] != 4 || r.vals[mK6P95] != 9 {
		t.Errorf("vals = %v, want p50 from med and p95", r.vals)
	}
	if _, ok := r.vals[mK6P99]; ok {
		t.Errorf("p99 present without p(99) in the summary")
	}
}

// TestIdleMissingSubtractsNothing builds a two-label series with no idle
// window: CPU per request is then raw, and the report says so.
func TestIdleMissingSubtractsNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, manifestFile), `{"labels":["one","two"]}`)
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	for _, label := range []string{"one", "two"} {
		lines := []string{windowsHead}
		for rep := 1; rep <= 3; rep++ {
			id := label + "-S1-r" + strconv.Itoa(rep)
			lines = append(lines, strings.Join([]string{id, "S1", strconv.Itoa(rep), "",
				start.Format(time.RFC3339), start.Add(10 * time.Minute).Format(time.RFC3339), "0", "1", ""}, "\t"))
			writeFile(t, filepath.Join(dir, label, "nr", "Q2-"+id+".json"),
				`{"data":{"actor":{"account":{"nrql":{"results":[{"rps":10,"n":4800}]}}}}}`)
			writeFile(t, filepath.Join(dir, label, "nr", "Q7-"+id+".json"),
				`{"data":{"actor":{"account":{"nrql":{"results":[{"cpu_s":48,"mcores":100}]}}}}}`)
		}
		writeFile(t, filepath.Join(dir, label, windowsFile), strings.Join(lines, "\n")+"\n")
	}
	s, err := loadSeries(dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	r := s.reps["one"][0]
	if got := r.vals[mCPUPerReq]; !near(got, 10) {
		t.Errorf("cpu_ms_per_req = %v, want 10 (1000 x 48 / 4800, nothing subtracted)", got)
	}
	if got := r.vals[mWindow]; !near(got, 480) {
		t.Errorf("window_s = %v, want 480 (n / rps)", got)
	}
	report := s.renderReport()
	for _, want := range []string{"not idle-subtracted", "k6 summary missing for 3 valid rep(s)", "insufficient reps (n=0 vs 0"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}
