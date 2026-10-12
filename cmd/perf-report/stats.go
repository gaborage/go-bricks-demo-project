package main

import (
	"math"
	"sort"
)

// Statistics rules from wiki/PERF_VERSION_COMPARISON.md ("Statistics"). Every
// function here is pure, so the table tests in stats_test.go pin the rules.

const (
	// bandFloor is the smallest noise band any comparison gets.
	bandFloor = 0.05
	// bandFloorP99 replaces bandFloor for p99 metrics, which are noisier.
	bandFloorP99 = 0.10
	// minVerdictReps is the number of valid reps each arm needs before a
	// primary outcome gets a verdict word.
	minVerdictReps = 3
	// minHodgesLehmannReps is the number of reps per arm at which the
	// Hodges-Lehmann shift is reported (descriptively).
	minHodgesLehmannReps = 5
	// repExtensionP1 and repExtensionP2 are the reference label's relative
	// ranges above which a scenario should run 5 reps instead of 3.
	repExtensionP1 = 0.10
	repExtensionP2 = 0.05
)

// stat summarizes the valid reps of one metric for one label and scenario.
type stat struct {
	n      int
	median float64
	min    float64
	max    float64
	values []float64 // sorted ascending
}

// summarize drops NaN values and returns false when none remain.
func summarize(values []float64) (stat, bool) {
	kept := make([]float64, 0, len(values))
	for _, v := range values {
		if !math.IsNaN(v) {
			kept = append(kept, v)
		}
	}
	if len(kept) == 0 {
		return stat{}, false
	}
	sort.Float64s(kept)
	return stat{
		n:      len(kept),
		median: median(kept),
		min:    kept[0],
		max:    kept[len(kept)-1],
		values: kept,
	}, true
}

// median of an ascending slice; the mean of the middle pair for an even count.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// relRange is (max - min) / median. It is undefined for a zero median.
func relRange(s *stat) (float64, bool) {
	if s.median == 0 {
		return 0, false
	}
	return (s.max - s.min) / math.Abs(s.median), true
}

// relDelta is (x - ref) / ref. It is undefined for a zero reference.
func relDelta(x, ref float64) (float64, bool) {
	if ref == 0 {
		return 0, false
	}
	return (x - ref) / math.Abs(ref), true
}

// noiseBand is B = max(floor, 2 x relative range of the reference, |A/A drift|),
// with a 10% floor for p99 metrics. A negative aaDrift means "no A/A data".
func noiseBand(refRelRange, aaDrift float64, p99 bool) float64 {
	b := bandFloor
	if p99 {
		b = bandFloorP99
	}
	b = math.Max(b, 2*refRelRange)
	if aaDrift >= 0 {
		b = math.Max(b, aaDrift)
	}
	return b
}

// rangesOverlap reports whether the min-max ranges of two arms share any value.
// Touching ranges overlap.
func rangesOverlap(a, b *stat) bool {
	return a.min <= b.max && b.min <= a.max
}

type verdictKind int

const (
	verdictInsufficient verdictKind = iota
	verdictNoDifference
	verdictChanged
)

// verdict is the outcome of one comparison of one primary metric.
type verdict struct {
	kind  verdictKind
	delta float64 // (other - base) / base
	band  float64
	nBase int
	nTest int
}

// decide applies the verdict rule: "changed" only when the two min-max ranges
// do not overlap and |delta| exceeds the band. Fewer than minVerdictReps reps
// in either arm gives no verdict.
func decide(base, test *stat, band float64) verdict {
	v := verdict{kind: verdictInsufficient, band: band, nBase: base.n, nTest: test.n}
	d, ok := relDelta(test.median, base.median)
	if !ok {
		return v
	}
	v.delta = d
	if base.n < minVerdictReps || test.n < minVerdictReps {
		return v
	}
	if !rangesOverlap(base, test) && math.Abs(d) > band {
		v.kind = verdictChanged
	} else {
		v.kind = verdictNoDifference
	}
	return v
}

// sameVerdict reports whether a sensitivity rerun confirms the main verdict:
// the same kind and, for "changed", the same direction.
func sameVerdict(a, b verdict) bool {
	if a.kind != b.kind {
		return false
	}
	if a.kind == verdictChanged {
		return (a.delta > 0) == (b.delta > 0)
	}
	return true
}

// hodgesLehmann is the median of every pairwise difference test[i] - base[j].
func hodgesLehmann(test, base []float64) float64 {
	diffs := make([]float64, 0, len(test)*len(base))
	for _, x := range test {
		for _, y := range base {
			diffs = append(diffs, x-y)
		}
	}
	sort.Float64s(diffs)
	return median(diffs)
}

func mean(values []float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
