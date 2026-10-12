package main

import (
	"math"
	"testing"
	"time"
)

const (
	eps      = 1e-9
	aliasP95 = "p95"
)

func near(a, b float64) bool { return math.Abs(a-b) < eps }

func TestSummarize(t *testing.T) {
	nan := math.NaN()
	tests := []struct {
		name               string
		in                 []float64
		ok                 bool
		n                  int
		median, minV, maxV float64
	}{
		{name: "empty", in: nil, ok: false},
		{name: "only NaN", in: []float64{nan, nan}, ok: false},
		{name: "odd count", in: []float64{3, 1, 2}, ok: true, n: 3, median: 2, minV: 1, maxV: 3},
		{name: "even count averages the middle pair", in: []float64{4, 1, 3, 2}, ok: true, n: 4, median: 2.5, minV: 1, maxV: 4},
		{name: "NaN dropped", in: []float64{10, nan, 12}, ok: true, n: 2, median: 11, minV: 10, maxV: 12},
		{name: "single value", in: []float64{7}, ok: true, n: 1, median: 7, minV: 7, maxV: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := summarize(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.n != tt.n || !near(got.median, tt.median) || !near(got.min, tt.minV) || !near(got.max, tt.maxV) {
				t.Errorf("got n=%d median=%v min=%v max=%v, want n=%d median=%v min=%v max=%v",
					got.n, got.median, got.min, got.max, tt.n, tt.median, tt.minV, tt.maxV)
			}
		})
	}
}

func TestRelRangeAndDelta(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		wantRR float64
		okRR   bool
	}{
		{name: "spread around the median", values: []float64{9, 10, 11}, wantRR: 0.2, okRR: true},
		{name: "no spread", values: []float64{5, 5, 5}, wantRR: 0, okRR: true},
		{name: "zero median is undefined", values: []float64{-1, 0, 1}, okRR: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, _ := summarize(tt.values)
			got, ok := relRange(&st)
			if ok != tt.okRR || (ok && !near(got, tt.wantRR)) {
				t.Errorf("relRange = %v, %v; want %v, %v", got, ok, tt.wantRR, tt.okRR)
			}
		})
	}

	deltas := []struct {
		x, ref, want float64
		ok           bool
	}{
		{x: 110, ref: 100, want: 0.10, ok: true},
		{x: 90, ref: 100, want: -0.10, ok: true},
		{x: 1, ref: 0, ok: false},
		{x: -12, ref: -10, want: -0.2, ok: true},
	}
	for _, d := range deltas {
		got, ok := relDelta(d.x, d.ref)
		if ok != d.ok || (ok && !near(got, d.want)) {
			t.Errorf("relDelta(%v, %v) = %v, %v; want %v, %v", d.x, d.ref, got, ok, d.want, d.ok)
		}
	}
}

func TestNoiseBand(t *testing.T) {
	tests := []struct {
		name    string
		rr, aa  float64
		p99     bool
		want    float64
		comment string
	}{
		{name: "floor", rr: 0.01, aa: -1, want: 0.05},
		{name: "twice the reference range", rr: 0.04, aa: -1, want: 0.08},
		{name: "A/A drift dominates", rr: 0.01, aa: 0.07, want: 0.07},
		{name: "A/A drift below the floor", rr: 0.01, aa: 0.02, want: 0.05},
		{name: "p99 floor", rr: 0.01, aa: -1, p99: true, want: 0.10},
		{name: "p99 floor loses to a wide range", rr: 0.08, aa: 0.03, p99: true, want: 0.16},
		{name: "zero A/A drift is data, not absence", rr: 0, aa: 0, want: 0.05},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := noiseBand(tt.rr, tt.aa, tt.p99); !near(got, tt.want) {
				t.Errorf("noiseBand = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRangesOverlap(t *testing.T) {
	tests := []struct {
		name string
		a, b []float64
		want bool
	}{
		{name: "disjoint", a: []float64{1, 2}, b: []float64{3, 4}, want: false},
		{name: "disjoint reversed", a: []float64{3, 4}, b: []float64{1, 2}, want: false},
		{name: "touching counts as overlap", a: []float64{1, 2}, b: []float64{2, 3}, want: true},
		{name: "nested", a: []float64{1, 5}, b: []float64{2, 3}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := summarize(tt.a)
			b, _ := summarize(tt.b)
			if got := rangesOverlap(&a, &b); got != tt.want {
				t.Errorf("rangesOverlap = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name       string
		base, test []float64
		band       float64
		want       verdictKind
		wantDelta  float64
	}{
		{name: "disjoint and beyond the band", base: []float64{10, 10.2, 10.4}, test: []float64{11.8, 12, 12.1},
			band: 0.078, want: verdictChanged, wantDelta: 12.0/10.2 - 1},
		{name: "faster also counts as changed", base: []float64{12, 12.1, 12.2}, test: []float64{10, 10.1, 10.2},
			band: 0.05, want: verdictChanged, wantDelta: 10.1/12.1 - 1},
		{name: "disjoint but inside the band", base: []float64{10, 10.1, 10.2}, test: []float64{10.3, 10.4, 10.5},
			band: 0.05, want: verdictNoDifference, wantDelta: 10.4/10.1 - 1},
		{name: "beyond the band but ranges overlap", base: []float64{10, 10.2, 13}, test: []float64{12, 12.5, 13},
			band: 0.05, want: verdictNoDifference, wantDelta: 12.5/10.2 - 1},
		{name: "two reps in one arm", base: []float64{10, 10.2}, test: []float64{12, 12.1, 12.2},
			band: 0.05, want: verdictInsufficient, wantDelta: 12.1/10.1 - 1},
		{name: "zero base median", base: []float64{0, 0, 0}, test: []float64{1, 1, 1},
			band: 0.05, want: verdictInsufficient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, _ := summarize(tt.base)
			test, _ := summarize(tt.test)
			got := decide(&base, &test, tt.band)
			if got.kind != tt.want || !near(got.delta, tt.wantDelta) {
				t.Errorf("decide = kind %v delta %v, want kind %v delta %v", got.kind, got.delta, tt.want, tt.wantDelta)
			}
		})
	}
}

func TestSameVerdict(t *testing.T) {
	up := verdict{kind: verdictChanged, delta: 0.1}
	down := verdict{kind: verdictChanged, delta: -0.1}
	none := verdict{kind: verdictNoDifference, delta: 0.01}
	tests := []struct {
		name string
		a, b verdict
		want bool
	}{
		{name: "same direction", a: up, b: verdict{kind: verdictChanged, delta: 0.2}, want: true},
		{name: "direction flips", a: up, b: down, want: false},
		{name: "changed becomes no difference", a: up, b: none, want: false},
		{name: "no difference holds", a: none, b: verdict{kind: verdictNoDifference, delta: -0.02}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameVerdict(tt.a, tt.b); got != tt.want {
				t.Errorf("sameVerdict = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHodgesLehmann(t *testing.T) {
	tests := []struct {
		name       string
		test, base []float64
		want       float64
	}{
		{name: "constant shift", test: []float64{11, 12, 13, 14, 15}, base: []float64{1, 2, 3, 4, 5}, want: 10},
		{name: "one outlier barely moves it", test: []float64{11, 12, 13, 14, 100}, base: []float64{1, 2, 3, 4, 5}, want: 10},
		{name: "even count of differences", test: []float64{2, 4}, base: []float64{1}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hodgesLehmann(tt.test, tt.base); !near(got, tt.want) {
				t.Errorf("hodgesLehmann = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindKnee(t *testing.T) {
	nan := math.NaN()
	ok := func(offered, p95 float64) stepStat {
		return stepStat{offered: offered, buckets: 2, rps: offered, p95: p95, pct5xx: 0, mcores: offered, throttle: 0}
	}
	with := func(st stepStat, f func(*stepStat)) stepStat { f(&st); return st }
	tests := []struct {
		name    string
		steps   []stepStat
		dropped float64
		want    int
	}{
		{name: "no steps", steps: nil, want: -1},
		{name: "every step passes", steps: []stepStat{ok(50, 8), ok(100, 9), ok(150, 12)}, want: 2},
		{name: "p95 doubles at step 3", steps: []stepStat{ok(50, 8), ok(100, 9), ok(150, 16), ok(200, 9)}, want: 1},
		{name: "5xx at the limit fails", steps: []stepStat{ok(50, 8), with(ok(100, 9), func(s *stepStat) { s.pct5xx = 0.1 })}, want: 0},
		{name: "throttling at 25% fails", steps: []stepStat{ok(50, 8), with(ok(100, 9), func(s *stepStat) { s.throttle = 25 })}, want: 0},
		{name: "missing 5xx and throttle are not checked",
			steps: []stepStat{ok(50, 8), with(ok(100, 9), func(s *stepStat) { s.pct5xx, s.throttle = nan, nan })}, want: 1},
		{name: "dropped total 0 ignores a low achieved rate",
			steps: []stepStat{ok(50, 8), with(ok(100, 9), func(s *stepStat) { s.rps = 80 })}, dropped: 0, want: 1},
		{name: "dropped total > 0 needs 99% achieved",
			steps: []stepStat{ok(50, 8), with(ok(100, 9), func(s *stepStat) { s.rps = 98 })}, dropped: 12, want: 0},
		{name: "unknown dropped total needs 99% achieved",
			steps: []stepStat{ok(50, 8), ok(100, 9), with(ok(150, 9), func(s *stepStat) { s.rps = nan })}, dropped: nan, want: 1},
		{name: "a step without minutes fails", steps: []stepStat{ok(50, 8), {offered: 100, p95: nan}}, want: 0},
		{name: "first step without p95", steps: []stepStat{{offered: 50, p95: nan}, ok(100, 9)}, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findKnee(tt.steps, tt.dropped); got != tt.want {
				t.Errorf("findKnee = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseSteps(t *testing.T) {
	three := time.Duration(3) * time.Minute
	tests := []struct {
		name string
		in   any
		want []step
	}{
		{name: "comma string", in: "50:3m, 100:3m", want: []step{{50, three}, {100, three}}},
		{name: "string list", in: []any{"50:3m", "100:90s"}, want: []step{{50, three}, {100, 90 * time.Second}}},
		{name: "object list", in: []any{map[string]any{"rate": 50.0, "duration": "3m"}, map[string]any{"target": 75.0, "duration": "1m"}},
			want: []step{{50, three}, {75, time.Minute}}},
		{name: "null", in: nil, want: nil},
		{name: "malformed entry rejects the list", in: "50:3m,100", want: nil},
		{name: "bad duration", in: "50:3 minutes", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSteps(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseSteps = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("step %d = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestStepStatsAssignsWholeMinutes(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 30, 0, time.UTC)
	t0 := start.Unix()
	rows := []map[string]any{
		// 10:01:00-10:02:00 is inside step 1 [10:00:30, 10:02:30).
		{fieldBegin: float64(t0 + 30), fieldEnd: float64(t0 + 90), fieldRPS: 25.0, aliasP95: 8.0, "mcores": 100.0},
		// A second facet of the same minute adds rps and keeps the worst p95.
		{fieldBegin: float64(t0 + 30), fieldEnd: float64(t0 + 90), fieldRPS: 25.0, aliasP95: 9.0},
		// 10:02:00-10:03:00 straddles the step boundary and is dropped.
		{fieldBegin: float64(t0 + 90), fieldEnd: float64(t0 + 150), fieldRPS: 75.0, aliasP95: 50.0},
		// 10:03:00-10:04:00 is inside step 2.
		{fieldBegin: float64(t0 + 150), fieldEnd: float64(t0 + 210), fieldRPS: 100.0,
			"percentile.http.server.request.duration": map[string]any{"95": 10.0}, "throttle": 0.02},
	}
	steps := []step{{50, 2 * time.Minute}, {100, 2 * time.Minute}}
	got := stepStats(steps, start, readBuckets(rows))
	if got[0].buckets != 1 || !near(got[0].rps, 50) || !near(got[0].p95, 9) || !near(got[0].mcores, 100) {
		t.Errorf("step 1 = %+v", got[0])
	}
	if got[1].buckets != 1 || !near(got[1].rps, 100) || !near(got[1].p95, 10) || !near(got[1].throttle, 2) {
		t.Errorf("step 2 = %+v", got[1])
	}
	if !math.IsNaN(got[1].mcores) {
		t.Errorf("step 2 mcores = %v, want NaN (absent)", got[1].mcores)
	}
}
