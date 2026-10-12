package main

import (
	"math"
	"testing"
)

// nest builds nested objects along keys with leaf at the end.
func nest(keys []string, leaf any) map[string]any {
	out := map[string]any{keys[len(keys)-1]: leaf}
	for i := len(keys) - 2; i >= 0; i-- {
		out = map[string]any{keys[i]: out}
	}
	return out
}

// nerdGraph wraps result rows in a single-query NerdGraph response.
func nerdGraph(rows ...any) jsonDoc {
	if rows == nil {
		rows = []any{}
	}
	return jsonDoc(nest(nrqlPath, rows))
}

func TestParseWindowRowKeepsAnEmptyRC(t *testing.T) {
	r, err := parseWindowRow("a-S1-r1\tS1\t1\t\t\t\t\t0\t")
	if err != nil {
		t.Fatal(err)
	}
	if r.rc != "" || r.valid || !r.runStart.IsZero() {
		t.Errorf("row = %+v, want an empty rc and zero times", r)
	}
	if _, err := parseWindowRow("a-S1-r1\tS1\t1\t\t\t\tthree\t0\t"); err == nil {
		t.Error("a non-numeric rc was accepted")
	}
}

func TestSplitRowsSupersedes(t *testing.T) {
	rows := []windowRow{
		{runID: "x-idle-r1", scenario: "idle", valid: true},
		{runID: "x-S1-r1", scenario: scenarioP2, valid: false},
		{runID: "x-S1-r1", scenario: scenarioP2, valid: true},
		{runID: "x-idle-r1", scenario: "idle", valid: false},
		{runID: "x-idle-r2", scenario: "IDLE", valid: true},
	}
	idle, reps := splitRows("x", rows)
	if len(idle) != 2 || idle[0].valid || idle[1].runID != "x-idle-r2" {
		t.Errorf("idle = %+v, want the re-run of r1 (void) and r2", idle)
	}
	if len(reps) != 2 || !reps[0].superseded || reps[1].superseded {
		t.Errorf("reps superseded = %v, %v; want true, false", reps[0].superseded, reps[1].superseded)
	}
}

func TestRouteKeyAndUsableResponses(t *testing.T) {
	const route = "/p"
	keys := []struct {
		row  map[string]any
		want string
	}{
		{row: map[string]any{fieldRoute: route, fieldMethod: "POST"}, want: "POST " + route},
		{row: map[string]any{fieldFacet: []any{route, "GET"}}, want: "GET " + route},
		{row: map[string]any{fieldFacet: route}, want: route},
	}
	for _, k := range keys {
		if got := routeKey(k.row); got != k.want {
			t.Errorf("routeKey(%v) = %q, want %q", k.row, got, k.want)
		}
	}
	usable := []struct {
		name string
		doc  jsonDoc
		want bool
	}{
		{name: "error body", doc: jsonDoc{"errors": []any{"boom"}, "data": nil}, want: false},
		{name: "error next to rows", doc: func() jsonDoc {
			d := nerdGraph(map[string]any{fieldN: 1.0})
			d["errors"] = []any{"partial"}
			return d
		}(), want: false},
		{name: "no rows", doc: nerdGraph(), want: false},
		{name: "rows", doc: nerdGraph(map[string]any{fieldN: 1.0}), want: true},
	}
	for _, u := range usable {
		if got := nrUsable(u.doc); got != u.want {
			t.Errorf("%s: nrUsable = %v, want %v", u.name, got, u.want)
		}
	}
}

func TestKneeCell(t *testing.T) {
	nan := math.NaN()
	passing := stepStat{offered: 50, buckets: 2, rps: 50, p95: 8, mcores: 100}
	tests := []struct {
		name      string
		stats     []stepStat
		knee      int
		hasSeries bool
		want      string
	}{
		{name: "no Q18 file", stats: []stepStat{passing}, knee: 0, want: "n/a (no Q18 series)"},
		{name: "steps unparsed", stats: nil, knee: -1, hasSeries: true, want: "n/a (no steps in perfMeta)"},
		{name: "no minute in step 1", stats: []stepStat{{offered: 50, p95: nan}}, knee: -1, hasSeries: true,
			want: "n/a (no Q18 minute inside step 1)"},
		{name: "first step fails", stats: []stepStat{passing}, knee: -1, hasSeries: true, want: "below the first step"},
		{name: "knee", stats: []stepStat{passing}, knee: 0, hasSeries: true, want: "step 1: 50 rps offered, 50.0 achieved, 100 mcores"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kneeCell(tt.stats, tt.knee, tt.hasSeries); got != tt.want {
				t.Errorf("kneeCell = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignedNeverPrintsNegativeZero(t *testing.T) {
	tests := []struct {
		v        float64
		decimals int
		want     string
	}{
		{v: -0.0004, decimals: 2, want: "+0.00"},
		{v: -0.006, decimals: 2, want: "-0.01"},
		{v: 1.25, decimals: 1, want: "+1.3"},
		{v: 0, decimals: 0, want: "+0"},
	}
	for _, tt := range tests {
		if got := signed(tt.v, tt.decimals); got != tt.want {
			t.Errorf("signed(%v, %d) = %q, want %q", tt.v, tt.decimals, got, tt.want)
		}
	}
	if got := signedPct(-0.0001); got != "+0.0%" {
		t.Errorf("signedPct(-0.0001) = %q, want +0.0%%", got)
	}
}

func TestScenarioOrderIsTotal(t *testing.T) {
	pairs := [][2]string{{"S01", "S1"}, {"S2", "S10"}, {"S9", "soak"}, {"idle2", "soak"}}
	for _, p := range pairs {
		if scenarioLess(p[0], p[1]) == scenarioLess(p[1], p[0]) {
			t.Errorf("scenarioLess(%q, %q) does not order the pair", p[0], p[1])
		}
	}
}

// TestVerdictReasons covers the counts of a one-sided comparison and the
// zero-median case, which share the insufficient kind.
func TestVerdictReasons(t *testing.T) {
	mk := func(label string, vals ...float64) []*rep {
		out := make([]*rep, 0, len(vals))
		for _, v := range vals {
			out = append(out, &rep{label: label, w: windowRow{scenario: scenarioP1, valid: true}, vals: map[string]float64{mK6P95: v}})
		}
		return out
	}
	s := &series{m: &manifest{labels: []string{"a", "b"}}, reps: map[string][]*rep{"b": mk("b", 1, 2, 3)}}
	c := comparison{base: "a", test: "b", hop: true}
	a := s.assess(scenarioP1, primaryP1, c, false)
	if got := verdictText(&a.verdict); got != "insufficient reps (n=0 vs 3; need 3 each)" {
		t.Errorf("one-sided: %q", got)
	}
	s.reps["a"] = mk("a", -1, 0, 1)
	a = s.assess(scenarioP1, primaryP1, c, false)
	if got := verdictText(&a.verdict); got != "no verdict: the reference median is 0" {
		t.Errorf("zero median: %q", got)
	}
}
