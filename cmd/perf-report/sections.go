package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// writeControls renders the operator's control rows from the manifest, then
// the rows perf-report derives from windows.tsv and the k6 perfMeta blocks.
func (s *series) writeControls(b *strings.Builder) {
	b.WriteString("## Versions and controls\n\n")
	labels := s.m.allLabels()
	var rows [][]string
	for _, r := range s.m.controls {
		rows = append(rows, labelRow(r.Name, labels, func(l string) string { return r.Values[l] }))
	}
	derived := []struct {
		name string
		cell func(label string) string
	}{
		{"session UTC window", s.sessionWindow},
		{"reps attempted / valid / void", s.repCounts},
		{"voided reps", s.voided},
		{"idle mcores", func(l string) string { return fixed(s.idle[l].mcores, 0) }},
		{"harness tag", s.metaValues(func(m *perfMeta) string { return m.harnessTag })},
		{"seed rows", s.metaValues(func(m *perfMeta) string { return numOrEmpty(m.seedRows) })},
		{"offered rps by scenario", s.offeredRates},
		{"k6 VUs (preallocated / max)", s.metaValues(func(m *perfMeta) string {
			if math.IsNaN(m.preAlloc) && math.IsNaN(m.maxVUs) {
				return ""
			}
			return fixed(m.preAlloc, 0) + " / " + fixed(m.maxVUs, 0)
		})},
	}
	for _, d := range derived {
		rows = append(rows, labelRow(d.name, labels, d.cell))
	}
	writeTable(b, append([]string{""}, labels...), rows)
}

// labelRow is a table row: a name, then one cell per label.
func labelRow(name string, labels []string, cell func(label string) string) []string {
	row := make([]string, 0, 1+len(labels))
	row = append(row, name)
	for _, l := range labels {
		row = append(row, cell(l))
	}
	return row
}

func numOrEmpty(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	return fixed(v, 0)
}

func (s *series) sessionWindow(label string) string {
	var first, last time.Time
	for _, r := range s.reps[label] {
		start := r.w.warmStart
		if start.IsZero() {
			start = r.w.runStart
		}
		if !start.IsZero() && (first.IsZero() || start.Before(first)) {
			first = start
		}
		if r.w.runEnd.After(last) {
			last = r.w.runEnd
		}
	}
	if first.IsZero() || last.IsZero() {
		return na
	}
	return first.Format(minuteUTC) + " to " + last.Format(minuteUTC)
}

func (s *series) repCounts(label string) string {
	var valid, void, replaced int
	for _, r := range s.reps[label] {
		switch {
		case !r.w.valid:
			void++
		case r.superseded:
			replaced++
		default:
			valid++
		}
	}
	out := fmt.Sprintf("%d / %d / %d", len(s.reps[label]), valid, void)
	if replaced > 0 {
		out += fmt.Sprintf(" (+%d valid but superseded)", replaced)
	}
	return out
}

func (s *series) voided(label string) string {
	var ids []string
	for _, r := range s.reps[label] {
		if !r.w.valid {
			ids = append(ids, r.w.runID)
		}
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

// metaValues lists the distinct non-empty perfMeta values of a label's counted reps.
func (s *series) metaValues(get func(*perfMeta) string) func(string) string {
	return func(label string) string {
		seen := map[string]bool{}
		for _, r := range s.reps[label] {
			if v := get(&r.meta); r.counts() && v != "" {
				seen[v] = true
			}
		}
		return joinSorted(seen)
	}
}

func joinSorted(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func (s *series) offeredRates(label string) string {
	byScenario := map[string]map[string]bool{}
	for _, r := range s.reps[label] {
		if !r.counts() || math.IsNaN(r.meta.rate) {
			continue
		}
		if byScenario[r.w.scenario] == nil {
			byScenario[r.w.scenario] = map[string]bool{}
		}
		byScenario[r.w.scenario][strconv.FormatFloat(r.meta.rate, 'f', -1, 64)] = true
	}
	scenarios := make([]string, 0, len(byScenario))
	for sc := range byScenario {
		scenarios = append(scenarios, sc)
	}
	sort.Slice(scenarios, func(i, j int) bool { return scenarioLess(scenarios[i], scenarios[j]) })
	parts := make([]string, 0, len(scenarios))
	for _, sc := range scenarios {
		parts = append(parts, sc+" "+strings.ReplaceAll(joinSorted(byScenario[sc]), ", ", "/"))
	}
	return strings.Join(parts, ", ")
}

// staircaseRun is the last S5 attempt of a label, valid or void: crossing the
// rate and dropped-iteration gates is what a staircase measures.
func (s *series) staircaseRun(label string) *rep {
	var last *rep
	for _, r := range s.reps[label] {
		if !r.superseded && r.w.scenario == scenarioStaircase {
			last = r
		}
	}
	return last
}

func (s *series) writeStaircase(b *strings.Builder) {
	b.WriteString("## Stress staircase (" + scenarioStaircase + ")\n\n")
	type run struct {
		label string
		stats []stepStat
		knee  int
		r     *rep
	}
	var runs []run
	maxSteps := 0
	for _, label := range s.m.allLabels() {
		r := s.staircaseRun(label)
		if r == nil {
			continue
		}
		st := stepStats(r.meta.steps, r.w.runStart, r.buckets)
		runs = append(runs, run{label: label, stats: st, knee: findKnee(st, r.val(mDropped)), r: r})
		maxSteps = max(maxSteps, len(st))
	}
	if len(runs) == 0 {
		b.WriteString("No staircase (" + scenarioStaircase + ") attempt recorded in this series.\n\n")
		return
	}
	b.WriteString("Indicative: one rep per label, its last attempt whether valid or void. Cells are p95 ms / 5xx % / CPU mcores / CPU throttled %, " +
		"from the per-minute Q18 series over the minutes wholly inside each step.\n\n")
	head := []string{"step (offered rps)"}
	for _, r := range runs {
		head = append(head, r.label)
	}
	var rows [][]string
	for i := 0; i < maxSteps; i++ {
		row := []string{fmt.Sprintf("%d (%s)", i+1, stepOffered(runs[0].stats, i))}
		for _, r := range runs {
			row = append(row, stepCell(r.stats, i))
		}
		rows = append(rows, row)
	}
	knee := []string{"knee"}
	for _, r := range runs {
		knee = append(knee, kneeCell(r.stats, r.knee, len(r.r.buckets) > 0))
	}
	writeTable(b, head, append(rows, knee))
}

func stepOffered(stats []stepStat, i int) string {
	if i >= len(stats) {
		return na
	}
	return fixed(stats[i].offered, 0)
}

func stepCell(stats []stepStat, i int) string {
	if i >= len(stats) || stats[i].buckets == 0 {
		return na
	}
	st := &stats[i]
	return fixed(st.p95, 2) + " / " + fixed(st.pct5xx, 2) + " / " + fixed(st.mcores, 0) + " / " + fixed(st.throttle, 1)
}

func kneeCell(stats []stepStat, knee int, hasSeries bool) string {
	switch {
	case !hasSeries:
		return na + " (no Q18 series)"
	case len(stats) == 0:
		return na + " (no steps in perfMeta)"
	case stats[0].buckets == 0:
		return na + " (no Q18 minute inside step 1)"
	case knee < 0:
		return "below the first step"
	}
	st := &stats[knee]
	return fmt.Sprintf("step %d: %s rps offered, %s achieved, %s mcores",
		knee+1, fixed(st.offered, 0), fixed(st.rps, 1), fixed(st.mcores, 0))
}

func (s *series) writeTelemetryNotes(b *strings.Builder) {
	b.WriteString("## Telemetry comparability notes\n\n")
	b.WriteString("- P1 comes from the k6 summary, so a change in what a version's telemetry emits cannot move it.\n")
	b.WriteString("- NR latency percentiles come from histograms and are approximations.\n")
	b.WriteString("- Per-request server metrics divide by Q2 `n` summed over route facets, after subtracting the label's " +
		"idle level for the length of the NR window. That window is read back from the NR data (`n / rps`, else " +
		"`cpu_s / cores`), and from windows.tsv minus the trim only when neither is present.\n")
	b.WriteString("- The NR vs k6 rate gap leaves out `" + simRouteMarker + "` routes, which k6 never calls directly. " +
		"A rep beyond ±" + fixed(gapFlagPct, 0) + "% is flagged `nr-gap`; " + s.gapSummary() + "\n")
	for _, n := range s.m.notes {
		b.WriteString("- " + n + "\n")
	}
	b.WriteString("\n")
}

func (s *series) gapSummary() string {
	var flagged []string
	computed := 0
	for _, label := range s.m.allLabels() {
		for _, r := range s.reps[label] {
			v, ok := r.vals[mGap]
			if !ok || !r.counts() {
				continue
			}
			computed++
			if math.Abs(v) > gapFlagPct {
				flagged = append(flagged, r.w.runID+" "+fixed(v, 2)+"%")
			}
		}
	}
	switch {
	case computed == 0:
		return "no valid rep has both Q2 and a k6 rate."
	case len(flagged) == 0:
		return fmt.Sprintf("none of the %d valid reps with both is.", computed)
	default:
		return "flagged: " + strings.Join(flagged, ", ") + "."
	}
}

func (s *series) writeValidityLog(b *strings.Builder) {
	b.WriteString("## Validity log\n\n")
	b.WriteString("Every attempted rep. Only valid reps enter the statistics; a superseded row was re-run under the same run_id.\n\n")
	head := []string{"run_id", "run window (UTC)", "rc", "valid", "reason", "flags", "NR vs k6 gap %", "throttled %"}
	var rows [][]string
	for _, label := range s.m.allLabels() {
		for _, r := range s.reps[label] {
			valid := "0"
			if r.w.valid {
				valid = "1"
			}
			if r.superseded {
				valid += " (superseded)"
			}
			rows = append(rows, []string{
				r.w.runID, timeRange(r.w.runStart, r.w.runEnd), r.w.rc, valid, r.w.reason,
				strings.Join(r.flags, ";"), fixed(r.val(mGap), 2), fixed(r.val(mThrottle), 2),
			})
		}
	}
	if len(rows) == 0 {
		b.WriteString("No reps yet.\n\n")
	} else {
		writeTable(b, head, rows)
	}
	if len(s.notes) > 0 {
		b.WriteString("Data gaps and warnings:\n\n")
		for _, n := range s.notes {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("\n")
	}
}

func timeRange(from, to time.Time) string {
	if from.IsZero() || to.IsZero() {
		return na
	}
	return from.Format(secondUTC) + " to " + to.Format(secondUTC)
}
