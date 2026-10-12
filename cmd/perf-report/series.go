package main

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Scenario ids the report treats specially. P1 and P2 are pre-registered: only
// they get verdict words.
const (
	scenarioP1        = "S4"
	scenarioP2        = "S1"
	scenarioStaircase = "S5"
	scenarioIdle      = "idle"
	simRouteMarker    = "/__sim/"
	gapFlagPct        = 2.0  // NR vs k6 rate gap above which a rep is flagged
	throttleFlag      = 0.01 // CFS throttled-period ratio at which a rep is flagged
)

// Per-rep metric keys. They double as runs.csv column names.
const (
	mOffered   = "offered_rps"
	mK6RPS     = "k6_rps"
	mK6Reqs    = "k6_reqs"
	mDropped   = "dropped"
	mK6P50     = "k6_p50_ms"
	mK6P95     = "k6_p95_ms"
	mK6P99     = "k6_p99_ms"
	mErrPct    = "err_pct"
	mN429      = "n429"
	mNRRPS     = "nr_rps"
	mNRN       = "nr_n"
	mGap       = "nr_k6_gap_pct"
	mWindow    = "window_s"
	mCPUS      = "cpu_s"
	mMcores    = "cpu_mcores"
	mIdle      = "idle_mcores"
	mCPUPerReq = "cpu_ms_per_req"
	mThrottle  = "throttle_pct"
	mWSAvg     = "ws_avg_mib"
	mWSMax     = "ws_max_mib"
	mBytesReq  = "alloc_bytes_per_req"
	mAllocsReq = "allocs_per_req"
	// Dynamic keys: k6 submetrics ("k6_p95_ms{endpoint:x}") and NR routes
	// ("nr_p95_ms GET /x" style, prefix + space + route).
	prefixNRRoute = "nr_"
)

// k6Duration is the k6 request-duration trend. quantiles are the percentiles
// read from it, its submetrics and the NR latency rows.
const k6Duration = "http_req_duration"

var quantiles = []string{"50", "95", "99"}

// Inputs the report reads per rep, named as in the notes it writes.
const (
	inK6  = "k6 summary"
	inQ2  = "Q2"
	inQ3  = "Q3"
	inQ7  = "Q7"
	inQ8  = "Q8"
	inQ9  = "Q9"
	inQ18 = "Q18"
)

// perfMeta is the perfMeta block of a k6 summary.
type perfMeta struct {
	rate       float64 // NaN when null
	readMix    string
	harnessTag string
	seedRows   float64
	preAlloc   float64
	maxVUs     float64
	steps      []step
}

// rep is one attempted measured rep: a windows.tsv row plus what its files say.
type rep struct {
	label      string
	w          windowRow
	superseded bool // a later windows.tsv row reuses this run_id
	vals       map[string]float64
	flags      []string
	meta       perfMeta
	missing    []string
	buckets    []bucket // S5 per-minute NR series
}

func (r *rep) val(key string) float64 {
	if v, ok := r.vals[key]; ok {
		return v
	}
	return math.NaN()
}

// put returns a setter, so a (value, ok) call result can be stored directly.
func (r *rep) put(key string) func(float64, bool) {
	return func(v float64, ok bool) { r.set(key, v, ok) }
}

func (r *rep) set(key string, v float64, ok bool) {
	if ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
		r.vals[key] = v
	}
}

// counts reports whether the rep enters statistics.
func (r *rep) counts() bool { return r.w.valid && !r.superseded }

func (r *rep) flagged() bool { return len(r.flags) > 0 }

// idleLevel is a label's no-load baseline from its idle window(s).
type idleLevel struct {
	mcores    float64 // NaN when unknown
	bytesPerS float64
	objsPerS  float64
}

type series struct {
	dir   string
	trim  time.Duration
	m     *manifest
	reps  map[string][]*rep // label -> reps in windows.tsv order
	idle  map[string]idleLevel
	notes []string
}

func loadSeries(dir string, trim time.Duration) (*series, error) {
	m, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}
	s := &series{dir: dir, trim: trim, m: m, reps: map[string][]*rep{}, idle: map[string]idleLevel{}}
	s.notes = append(s.notes, m.warnings...)
	for _, label := range m.allLabels() {
		if err := s.loadLabel(label); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *series) loadLabel(label string) error {
	rows, found, err := loadWindows(filepath.Join(s.dir, label, windowsFile))
	if err != nil {
		return err
	}
	if !found {
		s.notes = append(s.notes, fmt.Sprintf("%s: no %s yet, so no reps.", label, windowsFile))
		return nil
	}
	var idleRows []windowRow
	var reps []*rep
	lastByRunID := map[string]int{}
	for _, row := range rows {
		if strings.EqualFold(row.scenario, scenarioIdle) {
			idleRows = append(idleRows, row)
			continue
		}
		lastByRunID[row.runID] = len(reps)
		reps = append(reps, &rep{label: label, w: row, vals: map[string]float64{}})
	}
	for i, r := range reps {
		r.superseded = lastByRunID[r.w.runID] != i
	}
	if s.idle[label], err = s.loadIdle(label, idleRows); err != nil {
		return err
	}
	for _, r := range reps {
		if r.superseded {
			continue
		}
		if err := s.loadRep(r); err != nil {
			return err
		}
	}
	s.reps[label] = reps
	s.noteMissing(label, reps)
	return nil
}

// loadIdle takes the median over the label's valid idle windows.
func (s *series) loadIdle(label string, rows []windowRow) (idleLevel, error) {
	var mc, bps, ops []float64
	for i := range rows {
		if !rows[i].valid {
			continue
		}
		smp, err := s.idleSample(label, &rows[i])
		if err != nil {
			return idleLevel{}, err
		}
		if math.IsNaN(smp.mcores) {
			continue
		}
		// The median drops a NaN rate just as it would an absent one.
		mc, bps, ops = append(mc, smp.mcores), append(bps, smp.bytesPerS), append(ops, smp.objsPerS)
	}
	lvl := idleLevel{mcores: medianOrNaN(mc), bytesPerS: medianOrNaN(bps), objsPerS: medianOrNaN(ops)}
	if math.IsNaN(lvl.mcores) {
		s.notes = append(s.notes, fmt.Sprintf(
			"%s: no valid idle window with Q7 mcores, so its CPU and allocations per request are not idle-subtracted.", label))
	}
	return lvl, nil
}

// idleSample reads one idle window. A field the window lacks stays NaN, and
// without Q7 mcores the allocation rates are not read either.
func (s *series) idleSample(label string, row *windowRow) (idleLevel, error) {
	smp := idleLevel{mcores: math.NaN(), bytesPerS: math.NaN(), objsPerS: math.NaN()}
	q7, err := s.nrRows(label, inQ7, row.runID)
	if err != nil {
		return smp, err
	}
	q9, err := s.nrRows(label, inQ9, row.runID)
	if err != nil {
		return smp, err
	}
	cpuS, okCPU := sumField(q7, "cpu_s")
	mcores, okM := sumField(q7, "mcores")
	if !okM {
		return smp, nil
	}
	smp.mcores = mcores
	window := s.window(row, math.NaN(), math.NaN(), cpuS, okCPU, mcores)
	if window <= 0 {
		return smp, nil
	}
	if b, ok := sumField(q9, "bytes"); ok {
		smp.bytesPerS = b / window
	}
	if o, ok := sumField(q9, "objs"); ok {
		smp.objsPerS = o / window
	}
	return smp, nil
}

func medianOrNaN(values []float64) float64 {
	st, ok := summarize(values)
	if !ok {
		return math.NaN()
	}
	return st.median
}

func (s *series) nrRows(label, query, runID string) ([]map[string]any, error) {
	doc, _, err := loadJSON(filepath.Join(s.dir, label, "nr", query+"-"+runID+".json"))
	if err != nil || doc == nil {
		return nil, err
	}
	return nrResults(doc), nil
}

func (s *series) loadRep(r *rep) error {
	k6Path := filepath.Join(s.dir, r.label, "k6", fmt.Sprintf("%s-r%d.json", r.w.scenario, r.w.rep))
	doc, found, err := loadJSON(k6Path)
	if err != nil {
		return err
	}
	if found {
		readK6(r, doc)
	} else {
		r.missing = append(r.missing, inK6)
	}
	nr := map[string][]map[string]any{}
	queries := []string{inQ2, inQ3, inQ7, inQ8, inQ9}
	if r.w.scenario == scenarioStaircase {
		queries = append(queries, inQ18)
	}
	for _, q := range queries {
		path := filepath.Join(s.dir, r.label, "nr", q+"-"+r.w.runID+".json")
		qdoc, ok, err := loadJSON(path)
		if err != nil {
			return err
		}
		if !ok {
			r.missing = append(r.missing, q)
			continue
		}
		nr[q] = nrResults(qdoc)
	}
	s.readNR(r, nr)
	r.flags = repFlags(r)
	return nil
}

// readK6 pulls the C2 summary fields. Each metric is optional.
func readK6(r *rep, doc jsonDoc) {
	r.put(mK6P50)(k6Percentile(doc, k6Duration, "50"))
	r.put(mK6P95)(k6Percentile(doc, k6Duration, "95"))
	r.put(mK6P99)(k6Percentile(doc, k6Duration, "99"))
	r.put(mK6Reqs)(doc.num("metrics", "http_reqs", "values", "count"))
	r.put(mK6RPS)(doc.num("metrics", "http_reqs", "values", "rate"))
	r.put(mDropped)(doc.num("metrics", "dropped_iterations", "values", "count"))
	r.put(mN429)(doc.num("metrics", "http_reqs{status:429}", "values", "count"))
	if rate, ok := doc.num("metrics", "http_req_failed", "values", "rate"); ok {
		r.set(mErrPct, rate*100, true)
	}
	readK6Submetrics(r, doc)
	r.meta = readPerfMeta(doc)
	r.set(mOffered, r.meta.rate, true)
}

// k6Percentile reads percentile q of a k6 trend metric; the median falls back
// to k6's own "med" stat when "p(50)" is not in the summary.
func k6Percentile(doc jsonDoc, metric, q string) (float64, bool) {
	v, ok := doc.num("metrics", metric, "values", "p("+q+")")
	if !ok && q == "50" {
		v, ok = doc.num("metrics", metric, "values", "med")
	}
	return v, ok
}

// readK6Submetrics stores p50, p95 and p99 of every tagged http_req_duration
// submetric, keyed like "k6_p95_ms{endpoint:x}".
func readK6Submetrics(r *rep, doc jsonDoc) {
	metrics, ok := doc.path("metrics").(map[string]any)
	if !ok {
		return
	}
	for name := range metrics {
		tags, isSub := strings.CutPrefix(name, k6Duration+"{")
		if !isSub {
			continue
		}
		for _, q := range quantiles {
			r.put("k6_p" + q + "_ms{" + tags)(k6Percentile(doc, name, q))
		}
	}
}

func readPerfMeta(doc jsonDoc) perfMeta {
	pm := perfMeta{rate: math.NaN(), seedRows: math.NaN(), preAlloc: math.NaN(), maxVUs: math.NaN()}
	if v, ok := doc.num("perfMeta", "rate"); ok {
		pm.rate = v
	}
	if v, ok := doc.num("perfMeta", "seedRows"); ok {
		pm.seedRows = v
	}
	if v, ok := doc.num("perfMeta", "preAllocatedVUs"); ok {
		pm.preAlloc = v
	}
	if v, ok := doc.num("perfMeta", "maxVUs"); ok {
		pm.maxVUs = v
	}
	pm.readMix = doc.str("perfMeta", "readMix")
	pm.harnessTag = doc.str("perfMeta", "harnessTag")
	pm.steps = parseSteps(doc.path("perfMeta", "steps"))
	return pm
}

// readNR derives the server-side metrics. Per-request values divide by Q2 n
// summed across route facets, after subtracting the label's idle level.
func (s *series) readNR(r *rep, nr map[string][]map[string]any) {
	q2 := nr[inQ2]
	n, okN := sumField(q2, fieldN)
	rps, okRPS := sumField(q2, fieldRPS)
	r.set(mNRN, n, okN)
	r.set(mNRRPS, rps, okRPS)
	if k6, ok := r.vals[mK6RPS]; ok && okRPS && k6 > 0 {
		r.set(mGap, (clientRPS(q2)-k6)/k6*100, true)
	}
	cpuS, okCPU := sumField(nr[inQ7], "cpu_s")
	mcores, okM := sumField(nr[inQ7], "mcores")
	r.set(mCPUS, cpuS, okCPU)
	r.set(mMcores, mcores, okM)
	window := s.window(&r.w, n, rps, cpuS, okCPU, mcores)
	r.set(mWindow, window, window > 0)
	idle := s.idle[r.label]
	r.set(mIdle, idle.mcores, true)
	// perRequest subtracts the idle rate over the window, then divides by n.
	// The float64 conversion stops the compiler fusing the multiply into the
	// subtraction on some architectures, so every platform rounds alike.
	perRequest := func(key string, total float64, ok bool, idlePerS float64) {
		if ok && okN && n > 0 && window > 0 {
			r.set(key, (total-float64(zeroIfNaN(idlePerS)*window))/n, true)
		}
	}
	perRequest(mCPUPerReq, 1000*cpuS, okCPU, idle.mcores)
	if v, ok := firstField(nr[inQ8], "throttle"); ok {
		r.set(mThrottle, v*100, true)
	}
	r.put(mWSAvg)(firstField(nr[inQ8], "ws_avg"))
	r.put(mWSMax)(firstField(nr[inQ8], "ws_max"))
	bytes, okB := sumField(nr[inQ9], "bytes")
	perRequest(mBytesReq, bytes, okB, idle.bytesPerS)
	objs, okO := sumField(nr[inQ9], "objs")
	perRequest(mAllocsReq, objs, okO, idle.objsPerS)
	readRoutes(r, nr[inQ3])
	if q18, ok := nr[inQ18]; ok {
		r.buckets = readBuckets(q18)
	}
}

// readRoutes stores the Q3 per-route percentiles.
func readRoutes(r *rep, rows []map[string]any) {
	for _, row := range rows {
		route := routeOf(row)
		if route == "" {
			route = "all routes"
		}
		for _, q := range quantiles {
			r.put(prefixNRRoute + "p" + q + "_ms " + route)(percentile(row, q))
		}
	}
}

// clientRPS is the Q2 rate without simulator routes: a relay request reaches
// its in-process peer simulator as a second server request k6 never sent.
func clientRPS(rows []map[string]any) float64 {
	var sum float64
	for _, row := range rows {
		if strings.Contains(routeOf(row), simRouteMarker) {
			continue
		}
		if v, ok := asNum(row[fieldRPS]); ok {
			sum += v
		}
	}
	return sum
}

// window is the length in seconds of the NR window the rep's sums cover. It is
// read back from the NR data (n / rps, else cpu_s / cores), because that is
// the window NR integrated over; the windows.tsv run window minus the trim on
// each side is the fallback.
func (s *series) window(w *windowRow, n, rps, cpuS float64, okCPU bool, mcores float64) float64 {
	if n > 0 && rps > 0 {
		return n / rps
	}
	if okCPU && cpuS > 0 && mcores > 0 {
		return 1000 * cpuS / mcores
	}
	if w.runStart.IsZero() || w.runEnd.IsZero() {
		return 0
	}
	return (w.runEnd.Sub(w.runStart) - 2*s.trim).Seconds()
}

func zeroIfNaN(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return v
}

// repFlags marks a valid rep that is kept but may be environment-affected. A
// reason code the runner wrote on a valid row is carried over as a flag.
func repFlags(r *rep) []string {
	var flags []string
	if r.w.valid && r.w.reason != "" {
		for _, code := range strings.Split(r.w.reason, ";") {
			if code = strings.TrimSpace(code); code != "" {
				flags = append(flags, code)
			}
		}
	}
	if v, ok := r.vals[mThrottle]; ok && v >= throttleFlag*100 {
		flags = append(flags, "throttle")
	}
	if v, ok := r.vals[mGap]; ok && math.Abs(v) > gapFlagPct {
		flags = append(flags, "nr-gap")
	}
	sort.Strings(flags)
	return flags
}

// noteMissing lists, per input, the valid reps that lack it.
func (s *series) noteMissing(label string, reps []*rep) {
	byInput := map[string][]string{}
	for _, r := range reps {
		if !r.counts() {
			continue
		}
		for _, in := range r.missing {
			byInput[in] = append(byInput[in], r.w.runID)
		}
	}
	for _, in := range []string{inK6, inQ2, inQ3, inQ7, inQ8, inQ9, inQ18} {
		if ids := byInput[in]; len(ids) > 0 {
			s.notes = append(s.notes, fmt.Sprintf("%s: %s missing for %d valid rep(s): %s.",
				label, in, len(ids), strings.Join(ids, ", ")))
		}
	}
}

// values returns the metric for the counted reps of label/scenario.
func (s *series) values(label, scenario, key string, skipFlagged bool) []float64 {
	var out []float64
	for _, r := range s.reps[label] {
		if !r.counts() || r.w.scenario != scenario || (skipFlagged && r.flagged()) {
			continue
		}
		if v, ok := r.vals[key]; ok {
			out = append(out, v)
		}
	}
	return out
}

// scenarios lists every measured scenario in the series: S1..S4 first, then
// the rest sorted.
func (s *series) scenarios() []string {
	seen := map[string]bool{}
	for _, reps := range s.reps {
		for _, r := range reps {
			seen[r.w.scenario] = true
		}
	}
	out := make([]string, 0, len(seen))
	for sc := range seen {
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return scenarioLess(out[i], out[j]) })
	return out
}

// scenarioLess orders S1 < S2 < ... < S10 numerically, then anything else.
func scenarioLess(a, b string) bool {
	na, okA := scenarioNumber(a)
	nb, okB := scenarioNumber(b)
	switch {
	case okA && okB:
		return na < nb
	case okA != okB:
		return okA
	default:
		return a < b
	}
}

func scenarioNumber(s string) (int, bool) {
	rest, ok := strings.CutPrefix(s, "S")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}
