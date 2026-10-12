package main

import (
	"math"
	"sort"
	"testing"
	"time"
)

// staircaseSchedule is the offered rate at second t after the first step
// starts, as the k6 suite runs PERF_STEPS: each step held for its duration, and
// a linear ramp of `ramp` seconds before every step after the first.
func staircaseSchedule(steps []step, ramp float64, t float64) float64 {
	from := 0.0
	for i, st := range steps {
		if i > 0 {
			if t < from+ramp {
				prev := steps[i-1].rate
				return prev + (st.rate-prev)*(t-from)/ramp
			}
			from += ramp
		}
		if t < from+st.duration.Seconds() {
			return st.rate
		}
		from += st.duration.Seconds()
	}
	return 0
}

// minuteBuckets simulates Q18 rows for the whole minutes after runStart: rps
// is the mean offered rate, mcores is 2 per offered request, and p95 is a
// latency curve read at the minute's 95th-percentile rate.
func minuteBuckets(steps []step, ramp float64, runStart, anchor time.Time, minutes int, latency func(float64) float64) []bucket {
	offset := anchor.Sub(runStart).Seconds()
	var out []bucket
	for m := 1; m <= minutes; m++ {
		rates := make([]float64, 0, minuteSeconds)
		for s := 0; s < minuteSeconds; s++ {
			rates = append(rates, staircaseSchedule(steps, ramp, float64(m*minuteSeconds+s)-offset))
		}
		meanRate := mean(rates)
		sort.Float64s(rates)
		begin := runStart.Unix() + int64(m*minuteSeconds)
		out = append(out, bucket{begin: begin, end: begin + minuteSeconds, rps: meanRate,
			p95: latency(rates[minuteSeconds*95/100]), pct5xx: 0, mcores: 2 * meanRate, throttle: 0})
	}
	return out
}

// TestStaircaseRampModel replays the shape that misplaced the knee: six
// 3-minute steps with a separate 15 s ramp before each step after the first,
// and k6's first step starting 1 s after RUN_START. Latency doubles only at
// step 6, so the knee is step 5, and every step sees exactly its own rate.
func TestStaircaseRampModel(t *testing.T) {
	three := 3 * time.Minute
	steps := []step{{50, three}, {100, three}, {150, three}, {200, three}, {250, three}, {300, three}}
	runStart := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	anchor := runStart.Add(time.Second)
	latency := func(rate float64) float64 {
		if rate > 250 {
			return 30
		}
		return 8
	}
	// 18 min of steps + 75 s of ramps; the NR window drops the last minute.
	buckets := minuteBuckets(steps, 15, runStart, anchor, 18, latency)

	got := stepStats(steps, anchor, defaultStepRamp, buckets)
	if knee := findKnee(got, 0); knee != 4 {
		t.Errorf("knee = step %d, want step 5", knee+1)
	}
	for i, st := range got {
		if st.buckets == 0 || !near(st.mcores, 2*st.offered) || !near(st.rps, st.offered) {
			t.Errorf("step %d: %d minutes, rps %v, mcores %v; want only minutes at %v rps", i+1, st.buckets, st.rps, st.mcores, st.offered)
		}
	}

	// Laid out without the ramps, later steps take in ramp and earlier-step minutes.
	flat := stepStats(steps, runStart, 0, buckets)
	mixed := 0
	for i := range flat {
		if !near(flat[i].mcores, 2*flat[i].offered) {
			mixed++
		}
	}
	if mixed == 0 {
		t.Error("the no-ramp layout matched every step: the fixture no longer exercises the ramps")
	}
}

func TestStaircaseAnchorAndRampFromPerfMeta(t *testing.T) {
	runStart := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	r := &rep{w: windowRow{runStart: runStart}, vals: map[string]float64{}}
	readK6(r, jsonDoc{keyPerfMeta: map[string]any{"startedAt": "2026-01-01T10:00:01.5Z", "stepRampSeconds": 30.0}})
	if want := runStart.Add(1500 * time.Millisecond); !staircaseAnchor(r).Equal(want) {
		t.Errorf("anchor = %v, want perfMeta.startedAt %v", staircaseAnchor(r), want)
	}
	if r.meta.stepRamp != 30*time.Second {
		t.Errorf("stepRamp = %v, want 30s from perfMeta", r.meta.stepRamp)
	}
	r = &rep{w: windowRow{runStart: runStart}, vals: map[string]float64{}}
	readK6(r, jsonDoc{keyPerfMeta: map[string]any{"startedAt": nil}})
	if !staircaseAnchor(r).Equal(runStart) || r.meta.stepRamp != defaultStepRamp {
		t.Errorf("anchor = %v, ramp = %v; want RUN_START and the 15s default", staircaseAnchor(r), r.meta.stepRamp)
	}
	if !math.IsNaN(r.meta.rate) {
		t.Errorf("rate = %v, want NaN for an absent perfMeta.rate", r.meta.rate)
	}
}
