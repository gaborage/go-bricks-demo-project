package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	na         = "n/a"
	cellBreak  = "<br>"
	dayLayout  = "2006-01-02"
	minuteUTC  = "2006-01-02T15:04Z"
	secondUTC  = "2006-01-02T15:04:05Z"
	methodLink = "wiki/PERF_VERSION_COMPARISON.md"
)

// comparison is one pre-registered pairing: test against base.
type comparison struct {
	base, test string
	hop        bool // consecutive labels; false for the derived pairing with the reference
}

func (c comparison) String() string { return c.test + " vs " + c.base }

// comparisons pairs every label with the one before it, and every label from
// the third on with the reference as well.
func (s *series) comparisons() []comparison {
	l := s.m.labels
	var out []comparison
	for i := 1; i < len(l); i++ {
		out = append(out, comparison{base: l[i-1], test: l[i], hop: true})
		if i >= 2 {
			out = append(out, comparison{base: l[0], test: l[i]})
		}
	}
	return out
}

// statOf summarizes a metric over the counted reps of one label and scenario.
func (s *series) statOf(label, scenario, key string, skipFlagged bool) (stat, bool) {
	return summarize(s.values(label, scenario, key, skipFlagged))
}

// aaDrift is the largest |delta| between an A/A label and the label it
// repeats, or -1 when no pair has data for this metric.
func (s *series) aaDrift(scenario, key string) float64 {
	drift := -1.0
	for _, aa := range s.m.aaLabels {
		a, okA := s.statOf(aa, scenario, key, false)
		b, okB := s.statOf(s.m.aa[aa], scenario, key, false)
		if !okA || !okB {
			continue
		}
		if d, ok := relDelta(a.median, b.median); ok {
			drift = math.Max(drift, math.Abs(d))
		}
	}
	return drift
}

// assessed is one comparison of one metric, with its band and verdict.
type assessed struct {
	cmp       comparison
	def       metricDef
	diff      float64 // test - base, for absolute metrics
	band      float64
	hasBand   bool
	delta     float64
	hasDelta  bool
	verdict   verdict
	sensitive *verdict // verdict with flagged reps excluded, when that differs in input
}

func (s *series) assess(scenario string, def metricDef, c comparison, skipFlagged bool) assessed {
	base, okB := s.statOf(c.base, scenario, def.key, skipFlagged)
	test, okT := s.statOf(c.test, scenario, def.key, skipFlagged)
	// A missing arm has the zero stat, so the counts read n=0 for it.
	a := assessed{cmp: c, def: def, verdict: verdict{kind: verdictInsufficient, nBase: base.n, nTest: test.n}}
	if !okB || !okT {
		return a
	}
	if def.absolute {
		a.diff, a.hasDelta = test.median-base.median, true
		return a
	}
	a.delta, a.hasDelta = relDelta(test.median, base.median)
	if rr, ok := relRange(&base); ok {
		a.band, a.hasBand = noiseBand(rr, s.aaDrift(scenario, def.key), def.p99), true
	}
	if a.hasBand {
		a.verdict = decide(&base, &test, a.band)
	}
	return a
}

// assessPrimary adds the sensitivity rerun: flagged reps excluded.
func (s *series) assessPrimary(scenario string, def metricDef, c comparison) assessed {
	a := s.assess(scenario, def, c, false)
	if !s.anyFlagged(scenario, c) {
		return a
	}
	alt := s.assess(scenario, def, c, true)
	a.sensitive = &alt.verdict
	return a
}

func (s *series) anyFlagged(scenario string, c comparison) bool {
	for _, label := range []string{c.base, c.test} {
		for _, r := range s.reps[label] {
			if r.counts() && r.w.scenario == scenario && r.flagged() {
				return true
			}
		}
	}
	return false
}

func pct(v float64) string { return strconv.FormatFloat(v*100, 'f', 1, 64) + "%" }

func signedPct(v float64) string { return signed(v*100, 1) + "%" }

// signed renders v with an explicit sign, chosen after rounding so a tiny
// negative value prints as +0, never -0.
func signed(v float64, decimals int) string {
	r := roundTo(v, decimals) + 0
	if r < 0 {
		return "-" + fixed(-r, decimals)
	}
	return "+" + fixed(r, decimals)
}

func fixed(v float64, decimals int) string {
	if math.IsNaN(v) {
		return na
	}
	// Adding 0 turns a rounded -0 into 0.
	return strconv.FormatFloat(roundTo(v, decimals)+0, 'f', decimals, 64)
}

func roundTo(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

func statCell(st *stat, ok bool, decimals int) string {
	if !ok {
		return na
	}
	return fmt.Sprintf("%s [%s–%s] n=%d", fixed(st.median, decimals), fixed(st.min, decimals), fixed(st.max, decimals), st.n)
}

func (a *assessed) deltaCell() string {
	switch {
	case !a.hasDelta:
		return na
	case a.def.absolute:
		return signed(a.diff, a.def.decimals) + a.def.absUnit
	default:
		return signedPct(a.delta)
	}
}

func (a *assessed) bandText() string {
	if !a.hasBand {
		return na
	}
	return "±" + pct(a.band)
}

func verdictText(v *verdict) string {
	switch v.kind {
	case verdictChanged:
		return "changed (band ±" + pct(v.band) + ")"
	case verdictNoDifference:
		return "no detectable difference (band ±" + pct(v.band) + ")"
	default:
		if v.nBase >= minVerdictReps && v.nTest >= minVerdictReps {
			return "no verdict: the reference median is 0"
		}
		return fmt.Sprintf("insufficient reps (n=%d vs %d; need %d each)", v.nBase, v.nTest, minVerdictReps)
	}
}

// fullVerdict adds the sensitivity outcome to the verdict words.
func (a *assessed) fullVerdict() string {
	text := verdictText(&a.verdict)
	if a.sensitive == nil || a.verdict.kind == verdictInsufficient {
		return text
	}
	switch {
	case a.sensitive.kind == verdictInsufficient:
		return text + "; no sensitivity rerun: fewer than " + strconv.Itoa(minVerdictReps) + " unflagged reps"
	case !sameVerdict(a.verdict, *a.sensitive):
		return "environment-limited: " + text + ", but " + verdictText(a.sensitive) + " with flagged reps excluded"
	default:
		return text + "; holds with flagged reps excluded"
	}
}

// tableHeader is the wide comparison layout: each label's median and range,
// followed by its deltas against the labels it is compared with.
func (s *series) tableHeader(withVerdict bool) []string {
	head := []string{"metric"}
	cmps := s.comparisons()
	for _, label := range s.m.labels {
		head = append(head, label+" med [range]")
		for _, c := range cmps {
			if c.test == label {
				head = append(head, "Δ% "+c.String())
			}
		}
	}
	head = append(head, "band B", "A/A drift")
	if withVerdict {
		head = append(head, "verdict")
	}
	return head
}

func (s *series) tableRow(scenario string, def metricDef, primary bool) []string {
	row := []string{def.name}
	cmps := s.comparisons()
	results := map[comparison]assessed{}
	for _, c := range cmps {
		if primary {
			results[c] = s.assessPrimary(scenario, def, c)
		} else {
			results[c] = s.assess(scenario, def, c, false)
		}
	}
	for _, label := range s.m.labels {
		st, ok := s.statOf(label, scenario, def.key, false)
		row = append(row, statCell(&st, ok, def.decimals))
		for _, c := range cmps {
			if c.test == label {
				a := results[c]
				row = append(row, a.deltaCell())
			}
		}
	}
	if def.absolute {
		return append(row, "—", "—")
	}
	var bands, verdicts []string
	for _, c := range cmps {
		a := results[c]
		bands = append(bands, s.prefix(c)+a.bandText())
		verdicts = append(verdicts, s.prefix(c)+a.fullVerdict())
	}
	row = append(row, strings.Join(bands, cellBreak), s.driftCell(scenario, def.key))
	if primary {
		row = append(row, strings.Join(verdicts, cellBreak))
	}
	return row
}

// prefix names the comparison inside a multi-comparison cell.
func (s *series) prefix(c comparison) string {
	if len(s.comparisons()) == 1 {
		return ""
	}
	return c.String() + ": "
}

func (s *series) driftCell(scenario, key string) string {
	d := s.aaDrift(scenario, key)
	if d < 0 {
		return na
	}
	return pct(d)
}

// hasData reports whether any label has a counted value for the metric.
func (s *series) hasData(scenario, key string) bool {
	for _, label := range s.m.allLabels() {
		if len(s.values(label, scenario, key, false)) > 0 {
			return true
		}
	}
	return false
}

func writeTable(b *strings.Builder, head []string, rows [][]string) {
	b.WriteString("| " + strings.Join(escapeCells(head), " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(head)) + "\n")
	for _, r := range rows {
		b.WriteString("| " + strings.Join(escapeCells(r), " | ") + " |\n")
	}
	b.WriteString("\n")
}

func escapeCells(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	return out
}

// renderReport builds REPORT.md. Its only inputs are the series files, so the
// same series always renders the same bytes.
func (s *series) renderReport() string {
	var b strings.Builder
	s.writeTitle(&b)
	s.writeVerdict(&b)
	s.writeControls(&b)
	s.writePrimary(&b)
	s.writeScenarios(&b)
	s.writeStaircase(&b)
	b.WriteString("## Soak (optional)\n\nperf-report does not compute leak slopes: they need per-minute working-set " +
		"and goroutine series, which the consumed queries do not carry. Read them from the soak run's raw NR files.\n\n")
	s.writeTelemetryNotes(&b)
	s.writeValidityLog(&b)
	s.writeManifestTable(&b, "Covariates per session", s.m.covariates, "covariates")
	s.writeMethod(&b)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func (s *series) writeTitle(b *strings.Builder) {
	title := s.m.title
	if title == "" {
		title = "Perf comparison: " + strings.Join(s.m.labels, " vs ")
	}
	first, last := s.span()
	dates := "no reps yet"
	if !first.IsZero() {
		dates = first.Format(dayLayout)
		if last.Format(dayLayout) != dates {
			dates += " to " + last.Format(dayLayout)
		}
	}
	fmt.Fprintf(b, "# %s (%s)\n\n", title, dates)
}

// span is the earliest RUN_START and the latest RUN_END over every rep.
func (s *series) span() (first, last time.Time) {
	for _, label := range s.m.allLabels() {
		for _, r := range s.reps[label] {
			if !r.w.runStart.IsZero() && (first.IsZero() || r.w.runStart.Before(first)) {
				first = r.w.runStart
			}
			if r.w.runEnd.After(last) {
				last = r.w.runEnd
			}
		}
	}
	return first, last
}

func (s *series) writeVerdict(b *strings.Builder) {
	b.WriteString("## Verdict\n\n")
	for _, p := range []struct {
		scenario string
		def      metricDef
	}{{scenarioP1, primaryP1}, {scenarioP2, primaryP2}} {
		fmt.Fprintf(b, "- **%s**\n", p.def.name)
		if len(s.comparisons()) == 0 {
			b.WriteString("  - one label only, nothing to compare\n")
		}
		for _, c := range s.comparisons() {
			a := s.assessPrimary(p.scenario, p.def, c)
			name := c.String()
			if !c.hop {
				name += " (derived)"
			}
			fmt.Fprintf(b, "  - %s %s: %s%s\n", name, a.deltaCell(), a.fullVerdict(), s.hlText(p.scenario, p.def, c))
		}
	}
	b.WriteString("- " + s.repExtensionText() + "\n")
	if moves := s.largestMoves(); moves != "" {
		b.WriteString("- Largest descriptive moves (no verdict): " + moves + ".\n")
	}
	b.WriteString("\n")
}

// hlText reports the Hodges-Lehmann shift once both arms have 5 or more reps.
func (s *series) hlText(scenario string, def metricDef, c comparison) string {
	base := s.values(c.base, scenario, def.key, false)
	test := s.values(c.test, scenario, def.key, false)
	if len(base) < minHodgesLehmannReps || len(test) < minHodgesLehmannReps {
		return ""
	}
	return " [Hodges-Lehmann shift " + fixed(hodgesLehmann(test, base), def.decimals) + ", descriptive]"
}

func (s *series) repExtensionText() string {
	ref := s.m.labels[0]
	var parts []string
	for _, p := range []struct {
		scenario string
		key      string
		limit    float64
	}{{scenarioP1, mK6P95, repExtensionP1}, {scenarioP2, mCPUPerReq, repExtensionP2}} {
		st, ok := s.statOf(ref, p.scenario, p.key, false)
		rr, okRR := relRange(&st)
		switch {
		case !ok || !okRR:
			parts = append(parts, fmt.Sprintf("%s relative range %s", p.scenario, na))
		case rr > p.limit:
			parts = append(parts, fmt.Sprintf("%s relative range %s exceeds %s: run 5 reps of %s in every session",
				p.scenario, pct(rr), pct(p.limit), p.scenario))
		default:
			parts = append(parts, fmt.Sprintf("%s relative range %s is within %s: 3 reps suffice", p.scenario, pct(rr), pct(p.limit)))
		}
	}
	return "Rep extension check on reference " + ref + ": " + strings.Join(parts, "; ") + "."
}

// largestMoves lists the three largest |delta| among the efficiency metrics,
// per consecutive pairing, outside the two primaries.
func (s *series) largestMoves() string {
	var hops []string
	for _, c := range s.comparisons() {
		if !c.hop {
			continue
		}
		if texts := s.hopMoves(c); len(texts) > 0 {
			hops = append(hops, c.String()+": "+strings.Join(texts, ", "))
		}
	}
	return strings.Join(hops, "; ")
}

// move is one efficiency metric's delta in one scenario.
type move struct {
	text string
	abs  float64
}

// hopMoves returns the three largest moves of one pairing, largest first;
// ties keep scenario order, then moveDefs order.
func (s *series) hopMoves(c comparison) []string {
	var moves []move
	for _, sc := range s.scenarios() {
		moves = append(moves, s.scenarioMoves(sc, c)...)
	}
	sort.SliceStable(moves, func(i, j int) bool { return moves[i].abs > moves[j].abs })
	if len(moves) > 3 {
		moves = moves[:3]
	}
	texts := make([]string, 0, len(moves))
	for _, m := range moves {
		texts = append(texts, m.text)
	}
	return texts
}

func (s *series) scenarioMoves(sc string, c comparison) []move {
	var out []move
	for _, def := range moveDefs {
		if sc == scenarioP2 && def.key == primaryP2.key {
			continue
		}
		if a := s.assess(sc, def, c, false); a.hasDelta {
			out = append(out, move{text: sc + " " + def.name + " " + signedPct(a.delta), abs: math.Abs(a.delta)})
		}
	}
	return out
}

func (s *series) writePrimary(b *strings.Builder) {
	b.WriteString("## Primary table (" + scenarioP1 + " P1, " + scenarioP2 + " P2)\n\n")
	rows := [][]string{
		s.tableRow(scenarioP1, primaryP1, true),
		s.tableRow(scenarioP2, primaryP2, true),
	}
	writeTable(b, s.tableHeader(true), rows)
}

func (s *series) writeScenarios(b *strings.Builder) {
	b.WriteString("## Per-scenario descriptive tables\n\n")
	b.WriteString("Descriptive only: these rows never get a verdict word. Band B is shown for scale.\n\n")
	wrote := false
	for _, sc := range s.scenarios() {
		if sc == scenarioStaircase {
			continue
		}
		defs := append(append(append([]metricDef{}, descriptiveHead...), s.dynamicDefs(sc)...), descriptiveTail...)
		var rows [][]string
		for _, def := range defs {
			if s.hasData(sc, def.key) {
				rows = append(rows, s.tableRow(sc, def, false))
			}
		}
		if len(rows) == 0 {
			continue
		}
		wrote = true
		b.WriteString("### " + scenarioTitle(sc) + "\n\n")
		writeTable(b, s.tableHeader(false), rows)
	}
	if !wrote {
		b.WriteString("No valid reps yet.\n\n")
	}
}

var scenarioNames = map[string]string{
	"S1": "S1 read mix",
	"S2": "S2 CRUD",
	"S3": "S3 tokens",
	"S4": "S4 get-only",
	"S5": "S5 stress staircase",
}

func scenarioTitle(sc string) string {
	if t, ok := scenarioNames[sc]; ok {
		return t
	}
	return sc
}

func (s *series) writeManifestTable(b *strings.Builder, heading string, rows []manifestRow, key string) {
	b.WriteString("## " + heading + "\n\n")
	if len(rows) == 0 {
		b.WriteString("Not recorded: add a `" + key + "` array to " + manifestFile + ".\n\n")
		return
	}
	labels := s.m.allLabels()
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, labelRow(r.Name, labels, func(l string) string { return r.Values[l] }))
	}
	writeTable(b, append([]string{""}, labels...), out)
}

func (s *series) writeMethod(b *strings.Builder) {
	b.WriteString("## Method & raw data\n\n")
	b.WriteString("- Method, controls and statistics: " + methodLink + ".\n")
	b.WriteString("- One row per attempted rep: runs.csv, next to this report.\n")
	b.WriteString("- Raw inputs per label: `windows.tsv`, `k6/<scenario>-r<n>.json` and `nr/<query>-<run_id>.json`.\n")
}
