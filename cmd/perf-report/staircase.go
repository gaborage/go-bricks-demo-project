package main

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Knee rule from the method doc: the last step, counting from the first, where
// p95 stays under kneeP95Factor x the first step's p95, 5xx stays under
// kneeMax5xxPct, nothing was dropped and CFS throttling stays under
// kneeMaxThrottlePct.
const (
	kneeP95Factor      = 2.0
	kneeMax5xxPct      = 0.1
	kneeMaxThrottlePct = 25.0
	// kneeMinAchieved stands in for "dropped = 0" per step once the run's
	// dropped_iterations total is non-zero: k6 reports only the total.
	kneeMinAchieved = 0.99
	minuteSeconds   = 60
	// defaultStepRamp is the ramp the k6 suite runs before every step after
	// the first, used when perfMeta carries no stepRampSeconds.
	defaultStepRamp = 15 * time.Second
)

// step is one PERF_STEPS stage: an arrival rate held for a duration. The first
// step starts at the run's start; every later step starts after the previous
// one ends plus a separate ramp stage.
type step struct {
	rate     float64
	duration time.Duration
}

// parseSteps reads perfMeta.steps: "r:dur,r:dur", a list of "r:dur" strings, or
// a list of {"rate"|"target": r, "duration": "3m"} objects. Anything else
// yields no steps.
func parseSteps(v any) []step {
	switch t := v.(type) {
	case string:
		return parseStepList(strings.Split(t, ","))
	case []any:
		var parts []string
		var out []step
		for _, item := range t {
			switch it := item.(type) {
			case string:
				parts = append(parts, it)
			case map[string]any:
				if st, ok := stepFromObject(it); ok {
					out = append(out, st)
				}
			}
		}
		if len(parts) > 0 {
			return parseStepList(parts)
		}
		return out
	}
	return nil
}

func parseStepList(parts []string) []step {
	var out []step
	for _, p := range parts {
		rate, dur, ok := strings.Cut(strings.TrimSpace(p), ":")
		if !ok {
			return nil
		}
		r, err := strconv.ParseFloat(rate, 64)
		if err != nil {
			return nil
		}
		d, err := time.ParseDuration(dur)
		if err != nil {
			return nil
		}
		out = append(out, step{rate: r, duration: d})
	}
	return out
}

func stepFromObject(obj map[string]any) (step, bool) {
	r, ok := asNum(obj["rate"])
	if !ok {
		r, ok = asNum(obj["target"])
	}
	ds, okD := obj["duration"].(string)
	if !ok || !okD {
		return step{}, false
	}
	d, err := time.ParseDuration(ds)
	if err != nil {
		return step{}, false
	}
	return step{rate: r, duration: d}, true
}

// bucket is one minute of the staircase's NR time series (Q18).
type bucket struct {
	begin    int64 // epoch seconds
	end      int64
	rps      float64 // NaN when absent
	p95      float64
	pct5xx   float64
	mcores   float64
	throttle float64 // ratio, 0..1
}

// readBuckets merges Q18 rows by beginTimeSeconds. Route facets add up for
// rps and mcores; p95, 5xx and throttle take the worst facet.
func readBuckets(rows []map[string]any) []bucket {
	byBegin := map[int64]*bucket{}
	for _, row := range rows {
		begin, ok := asNum(row[fieldBegin])
		if !ok {
			continue
		}
		b := byBegin[int64(begin)]
		if b == nil {
			b = newBucket(begin, row)
			byBegin[b.begin] = b
		}
		b.merge(row)
	}
	out := make([]bucket, 0, len(byBegin))
	for _, b := range byBegin {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].begin < out[j].begin })
	return out
}

// newBucket starts the minute that begins at begin; a row without
// endTimeSeconds spans one minute.
func newBucket(begin float64, row map[string]any) *bucket {
	end, ok := asNum(row[fieldEnd])
	if !ok {
		end = begin + minuteSeconds
	}
	return &bucket{begin: int64(begin), end: int64(end), rps: math.NaN(), p95: math.NaN(),
		pct5xx: math.NaN(), mcores: math.NaN(), throttle: math.NaN()}
}

// merge folds one facet row of the minute into the bucket. TIMESERIES
// zero-fills a minute that has no data, so a zero count leaves that row's
// values unknown instead of reading as a real 0: a server row (Q18a) with
// n = 0 (rps 0, p95 0) leaves rps, p95 and 5xx unknown, and a container row
// (Q18b) with pts = 0 (mcores 0, throttle null) leaves mcores and throttle
// unknown. Each row carries only its own count; a row without one, such as a
// Q18b row captured before pts existed, always merges.
func (b *bucket) merge(row map[string]any) {
	if !zeroCount(row, fieldPts) {
		b.mergeContainer(row)
	}
	if !zeroCount(row, fieldN) {
		b.mergeServer(row)
	}
}

// zeroCount reports whether row carries the count field key and it is 0.
func zeroCount(row map[string]any, key string) bool {
	n, ok := asNum(row[key])
	return ok && n == 0
}

func (b *bucket) mergeContainer(row map[string]any) {
	b.mcores = addNaN(b.mcores, row["mcores"])
	if v, ok := asNum(row["throttle"]); ok {
		b.throttle = maxNaN(b.throttle, v)
	}
}

func (b *bucket) mergeServer(row map[string]any) {
	b.rps = addNaN(b.rps, row[fieldRPS])
	if v, ok := percentile(row, "95"); ok {
		b.p95 = maxNaN(b.p95, v)
	}
	if v, ok := asNum(row["pct5xx"]); ok {
		b.pct5xx = maxNaN(b.pct5xx, v)
	}
}

func addNaN(acc float64, v any) float64 {
	f, ok := asNum(v)
	if !ok {
		return acc
	}
	if math.IsNaN(acc) {
		return f
	}
	return acc + f
}

func maxNaN(acc, v float64) float64 {
	if math.IsNaN(acc) {
		return v
	}
	return math.Max(acc, v)
}

// stepStat aggregates the minute buckets that fall wholly inside one step.
type stepStat struct {
	offered  float64
	buckets  int
	rps      float64 // mean; NaN when no bucket
	p95      float64 // median of the per-minute p95
	pct5xx   float64 // mean
	mcores   float64 // mean
	throttle float64 // mean, percent
}

// stepStats lays the steps out from start, with ramp between consecutive steps,
// and aggregates the minutes that fall wholly inside each step. Ramp minutes
// belong to no step.
func stepStats(steps []step, start time.Time, ramp time.Duration, buckets []bucket) []stepStat {
	out := make([]stepStat, len(steps))
	from := float64(start.UnixNano()) / 1e9
	for i, st := range steps {
		if i > 0 {
			from += ramp.Seconds()
		}
		to := from + st.duration.Seconds()
		var rps, p95, e5, mc, th []float64
		for _, b := range buckets {
			if float64(b.begin) < from || float64(b.end) > to {
				continue
			}
			rps, p95 = append(rps, b.rps), append(p95, b.p95)
			e5, mc, th = append(e5, b.pct5xx), append(mc, b.mcores), append(th, b.throttle*100)
		}
		out[i] = stepStat{offered: st.rate, buckets: len(rps), rps: meanNaN(rps), p95: medianOrNaN(p95),
			pct5xx: meanNaN(e5), mcores: meanNaN(mc), throttle: meanNaN(th)}
		from = to
	}
	return out
}

func meanNaN(values []float64) float64 {
	var kept []float64
	for _, v := range values {
		if !math.IsNaN(v) {
			kept = append(kept, v)
		}
	}
	if len(kept) == 0 {
		return math.NaN()
	}
	return mean(kept)
}

// findKnee returns the index of the knee step, or -1 when even the first step
// fails or has no usable p95 to scale from (NaN, or 0 from minutes without
// server data). A step with no p95 fails; a missing 5xx or throttle value is
// not checked. totalDropped is the run's k6 dropped_iterations (NaN if unknown).
func findKnee(steps []stepStat, totalDropped float64) int {
	if len(steps) == 0 || !hasBaseP95(&steps[0]) {
		return -1
	}
	limit := kneeP95Factor * steps[0].p95
	knee := -1
	for i := range steps {
		st := &steps[i]
		ok := st.buckets > 0 && !math.IsNaN(st.p95) && st.p95 < limit &&
			belowOrUnknown(st.pct5xx, kneeMax5xxPct) && belowOrUnknown(st.throttle, kneeMaxThrottlePct) &&
			droppedOK(st, totalDropped)
		if !ok {
			break
		}
		knee = i
	}
	return knee
}

// hasBaseP95 reports whether the first step's p95 can anchor the knee limit:
// a limit of twice 0, or of twice NaN, would place the knee anywhere.
func hasBaseP95(first *stepStat) bool {
	return !math.IsNaN(first.p95) && first.p95 > 0
}

// belowOrUnknown passes a value under the limit, and a value the series lacks.
func belowOrUnknown(v, limit float64) bool {
	return math.IsNaN(v) || v < limit
}

func droppedOK(st *stepStat, totalDropped float64) bool {
	if totalDropped == 0 {
		return true
	}
	return !math.IsNaN(st.rps) && st.rps >= kneeMinAchieved*st.offered
}
