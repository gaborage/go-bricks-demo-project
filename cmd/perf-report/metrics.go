package main

import (
	"sort"
	"strings"
)

// pctPoints follows a difference between two percentages.
const pctPoints = " pp"

// metricDef is one row of a comparison table.
type metricDef struct {
	key      string
	name     string
	decimals int
	p99      bool // takes the 10% band floor
	// absolute rows (rates already in percent, counts that are usually 0)
	// compare by difference, with absUnit after it, and carry no band.
	absolute bool
	absUnit  string
}

// Primary outcomes: the only rows that get verdict words.
var (
	primaryP1 = metricDef{key: mK6P95, name: "P1 " + scenarioP1 + " k6 http_req_duration p95 (ms)", decimals: 2}
	primaryP2 = metricDef{key: mCPUPerReq, name: "P2 " + scenarioP2 + " app CPU-ms per request", decimals: 3}
)

// descriptiveHead and descriptiveTail frame the per-scenario tables; the k6
// submetric and NR route rows found in the data go between them.
var (
	descriptiveHead = []metricDef{
		{key: mOffered, name: "offered rps", decimals: 1},
		{key: mK6RPS, name: "achieved rps (k6)", decimals: 1},
		{key: mNRRPS, name: "NR rps (Q2)", decimals: 1},
		{key: mGap, name: "NR vs k6 rate gap %", decimals: 2, absolute: true, absUnit: pctPoints},
		{key: mK6P50, name: "k6 p50 ms", decimals: 2},
		{key: mK6P95, name: "k6 p95 ms", decimals: 2},
		{key: mK6P99, name: "k6 p99 ms", decimals: 2, p99: true},
	}
	descriptiveTail = []metricDef{
		{key: mErrPct, name: "error %", decimals: 3, absolute: true, absUnit: pctPoints},
		{key: mN429, name: "429 responses", decimals: 0, absolute: true},
		{key: mDropped, name: "dropped iterations", decimals: 0, absolute: true},
		{key: mMcores, name: "CPU mcores (Q7)", decimals: 0},
		{key: mCPUPerReq, name: "CPU-ms per request", decimals: 3},
		{key: mThrottle, name: "CPU throttled %", decimals: 2, absolute: true, absUnit: pctPoints},
		{key: mWSAvg, name: "working set avg MiB", decimals: 1},
		{key: mWSMax, name: "working set max MiB", decimals: 1},
		{key: mBytesReq, name: "alloc bytes per request", decimals: 0},
		{key: mAllocsReq, name: "allocs per request", decimals: 1},
	}
)

// moveDefs are the efficiency metrics scanned for "largest descriptive moves".
var moveDefs = []metricDef{
	{key: mCPUPerReq, name: "CPU-ms per request"},
	{key: mAllocsReq, name: "allocs per request"},
	{key: mBytesReq, name: "alloc bytes per request"},
	{key: mK6P50, name: "k6 p50"},
}

// dynamicDefs builds rows for the k6 submetrics and NR routes some rep of the
// scenario carries, sorted by key.
func (s *series) dynamicDefs(scenario string) []metricDef {
	seen := map[string]bool{}
	for _, reps := range s.reps {
		for _, r := range reps {
			if r.w.scenario == scenario {
				addDynamicKeys(seen, r.vals)
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	defs := make([]metricDef, 0, len(keys))
	for _, k := range keys {
		defs = append(defs, metricDef{key: k, name: dynamicName(k), decimals: 2, p99: strings.Contains(k, "p99")})
	}
	return defs
}

func addDynamicKeys(seen map[string]bool, vals map[string]float64) {
	for k := range vals {
		if isDynamicKey(k) {
			seen[k] = true
		}
	}
}

func isDynamicKey(k string) bool {
	return strings.Contains(k, "{") || (strings.HasPrefix(k, prefixNRRoute+"p") && strings.Contains(k, " "))
}

// dynamicName turns "k6_p95_ms{endpoint:x}" into "k6 p95 ms {endpoint:x}" and
// "nr_p95_ms GET /x" into "NR p95 ms GET /x".
func dynamicName(k string) string {
	if head, tags, ok := strings.Cut(k, "{"); ok {
		return strings.ReplaceAll(head, "_", " ") + " {" + tags
	}
	head, route, _ := strings.Cut(k, " ")
	head = strings.Replace(strings.ReplaceAll(head, "_", " "), "nr ", "NR ", 1)
	return head + " " + route
}
