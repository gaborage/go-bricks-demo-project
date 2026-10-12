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

// q18Minute is one Q18 minute as NerdGraph returns it: a server row (Q18a)
// and a container row (Q18b) with the same beginTimeSeconds. n = 0 is the
// zero-filled minute TIMESERIES emits when the server sent nothing; the
// container row has one CPU point (pts 1).
func q18Minute(begin int64, n, p95, mcores float64) []map[string]any {
	server := map[string]any{fieldBegin: float64(begin), fieldEnd: float64(begin + minuteSeconds),
		fieldN: n, fieldRPS: n / minuteSeconds, aliasP95: p95, "pct5xx": 0.0}
	if n == 0 {
		server["pct5xx"] = nil
	}
	container := map[string]any{fieldBegin: float64(begin), fieldEnd: float64(begin + minuteSeconds),
		aliasMcores: mcores, "throttle": 0.0, fieldPts: 1.0}
	return []map[string]any{server, container}
}

// TestStaircaseContainerMinuteWithoutPoints: the container CPU counter is
// scraped about once a minute, so a minute inside a step can hold no point.
// TIMESERIES zero-fills it as mcores 0, throttle null, pts 0, which is no
// data, not an idle minute: it must not lower the step's mean mcores. A Q18b
// row without pts (captured before the alias existed) still merges.
func TestStaircaseContainerMinuteWithoutPoints(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t0 := start.Unix()
	rows := make([]map[string]any, 0, 6)
	for i, mc := range []float64{100, 0, 140} {
		rows = append(rows, q18Minute(t0+int64(i*minuteSeconds), 3000, 8, mc)...)
	}
	missed := rows[3] // minute 2's container row
	missed[fieldPts], missed["throttle"] = 0.0, nil

	got := stepStats([]step{{50, 3 * time.Minute}}, start, 0, readBuckets(rows))
	if st := got[0]; st.buckets != 3 || !near(st.mcores, 120) || !near(st.throttle, 0) || !near(st.rps, 50) {
		t.Errorf("step 1 = %+v; want mcores 120 and throttle 0 from the two scraped minutes, rps 50 from all three", st)
	}

	delete(missed, fieldPts)
	missed["throttle"] = 0.0
	got = stepStats([]step{{50, 3 * time.Minute}}, start, 0, readBuckets(rows))
	if st := got[0]; !near(st.mcores, 80) {
		t.Errorf("mcores = %v, want 80: a row without pts merges as before", st.mcores)
	}
}

// TestStaircaseZeroFilledMinutes: a zero-filled minute (n 0, rps 0, p95 0) is
// no data, not a fast minute. Inside a step it must not drag the step's p95
// and rps down; filling the whole first step it leaves the knee n/a instead of
// "below the first step" against a limit of 2 x 0.
func TestStaircaseZeroFilledMinutes(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t0 := start.Unix()
	steps := []step{{50, 3 * time.Minute}, {100, 3 * time.Minute}}
	buckets := func(step1 [3][2]float64) []bucket { // step 1 minutes as {n, p95}
		var rows []map[string]any
		for i, m := range step1 {
			rows = append(rows, q18Minute(t0+int64(i*minuteSeconds), m[0], m[1], 100)...)
		}
		for i := 3; i < 6; i++ {
			rows = append(rows, q18Minute(t0+int64(i*minuteSeconds), 6000, 12, 200)...)
		}
		return readBuckets(rows)
	}

	got := stepStats(steps, start, 0, buckets([3][2]float64{{3000, 8}, {0, 0}, {3000, 10}}))
	if st := got[0]; st.buckets != 3 || !near(st.p95, 9) || !near(st.rps, 50) || !near(st.pct5xx, 0) ||
		!near(st.mcores, 100) {
		t.Errorf("step 1 = %+v; want p95 9, rps 50 and 5xx 0 from the two real minutes, mcores 100 from all three", st)
	}
	if knee := findKnee(got, 0); knee != 1 {
		t.Errorf("knee = %d, want 1 (step 2 p95 12 < 2 x 9)", knee)
	}

	got = stepStats(steps, start, 0, buckets([3][2]float64{{0, 0}, {0, 0}, {0, 0}}))
	if st := got[0]; !math.IsNaN(st.p95) || !math.IsNaN(st.rps) || !math.IsNaN(st.pct5xx) {
		t.Errorf("step 1 = %+v; want p95, rps and 5xx unknown when every minute is zero-filled", st)
	}
	knee := findKnee(got, 0)
	if knee != -1 {
		t.Errorf("knee = %d, want -1 when step 1 has no server p95", knee)
	}
	if cell := kneeCell(got, knee, true); cell != kneeNoBaseP95 {
		t.Errorf("kneeCell = %q, want %q", cell, kneeNoBaseP95)
	}
}
