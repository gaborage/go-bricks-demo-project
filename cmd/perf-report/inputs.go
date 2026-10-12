package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NRQL result field names the report reads (the query aliases).
const (
	fieldResults = "results"
	fieldRPS     = "rps"
	fieldN       = "n"
	fieldPts     = "pts"
	fieldBegin   = "beginTimeSeconds"
	fieldEnd     = "endTimeSeconds"
	fieldFacet   = "facet"
	fieldRoute   = "http.route"
	fieldMethod  = "http.request.method"
)

const (
	manifestFile = "series-manifest.json"
	windowsFile  = "windows.tsv"
	windowsHead  = "run_id\tscenario\trep\twarm_start\trun_start\trun_end\trc\tvalid\treason"
)

// readInput reads one file of the series. Every path is built from the --series
// flag plus names the series itself defines, so reading it is the point.
func readInput(path string) ([]byte, error) {
	return os.ReadFile(filepath.Clean(path))
}

// manifestRow is one operator-supplied table row: a name and a value per label.
type manifestRow struct {
	Name   string            `json:"name"`
	Values map[string]string `json:"values"`
}

// manifest is the part of series-manifest.json perf-report reads. Only labels
// is required; every other key in the file is ignored.
type manifest struct {
	title      string
	labels     []string          // first entry is the reference
	aa         map[string]string // A/A label -> the label it repeats
	aaLabels   []string          // sorted keys of aa
	controls   []manifestRow
	covariates []manifestRow
	notes      []string
	warnings   []string // optional keys that were present but unreadable
}

func loadManifest(seriesDir string) (*manifest, error) {
	raw, err := readInput(filepath.Join(seriesDir, manifestFile))
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestFile, err)
	}
	m := &manifest{aa: map[string]string{}}
	if err := json.Unmarshal(doc["labels"], &m.labels); err != nil || len(m.labels) == 0 {
		return nil, fmt.Errorf("%s: key \"labels\" must be a non-empty array of strings", manifestFile)
	}
	if rawAA, ok := doc["aa"]; ok {
		if err := json.Unmarshal(rawAA, &m.aa); err != nil {
			return nil, fmt.Errorf("%s: key \"aa\" must map an A/A label to a label: %w", manifestFile, err)
		}
	}
	m.optional(doc, "title", &m.title)
	m.optional(doc, "controls", &m.controls)
	m.optional(doc, "covariates", &m.covariates)
	m.optional(doc, "notes", &m.notes)
	for k := range m.aa {
		m.aaLabels = append(m.aaLabels, k)
	}
	sort.Strings(m.aaLabels)
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// optional decodes a key the operator may add; a key of the wrong shape becomes
// a warning in the report instead of a failed run.
func (m *manifest) optional(doc map[string]json.RawMessage, key string, into any) {
	raw, ok := doc[key]
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, into); err != nil {
		m.warnings = append(m.warnings, fmt.Sprintf("%s key %q ignored: %v", manifestFile, key, err))
	}
}

// plainName reports whether s can be used as one path element.
func plainName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}

func (m *manifest) validate() error {
	seen := map[string]bool{}
	for _, l := range append(append([]string{}, m.labels...), m.aaLabels...) {
		if !plainName(l) {
			return fmt.Errorf("%s: label %q is not a plain directory name", manifestFile, l)
		}
		if seen[l] {
			return fmt.Errorf("%s: label %q appears twice", manifestFile, l)
		}
		seen[l] = true
	}
	inLabels := map[string]bool{}
	for _, l := range m.labels {
		inLabels[l] = true
	}
	for _, aa := range m.aaLabels {
		if !inLabels[m.aa[aa]] {
			return fmt.Errorf("%s: A/A label %q repeats %q, which is not in labels", manifestFile, aa, m.aa[aa])
		}
	}
	return nil
}

// allLabels is the labels in report order: the compared labels, then A/A labels.
func (m *manifest) allLabels() []string {
	return append(append([]string{}, m.labels...), m.aaLabels...)
}

// windowRow is one line of a label's windows.tsv.
type windowRow struct {
	runID     string
	scenario  string
	rep       int
	warmStart time.Time
	runStart  time.Time
	runEnd    time.Time
	rc        string // the exit code as written; empty when the row has none
	valid     bool
	reason    string
}

// loadWindows reads <label>/windows.tsv. A missing file means the label has no
// reps yet and is not an error; a wrong header is.
func loadWindows(path string) ([]windowRow, bool, error) {
	raw, err := readInput(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if lines[0] != windowsHead {
		return nil, true, fmt.Errorf("%s: header must be exactly %q", path, windowsHead)
	}
	var rows []windowRow
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row, err := parseWindowRow(line)
		if err != nil {
			return nil, true, fmt.Errorf("%s line %d: %w", path, i+2, err)
		}
		rows = append(rows, row)
	}
	return rows, true, nil
}

func parseWindowRow(line string) (windowRow, error) {
	f := strings.Split(line, "\t")
	if len(f) == 8 {
		// An editor that strips trailing whitespace drops the tab before an
		// empty reason, the usual case on a valid row.
		f = append(f, "")
	}
	if len(f) != 9 {
		return windowRow{}, fmt.Errorf("want 9 tab-separated fields, got %d", len(f))
	}
	var r windowRow
	var err error
	r.runID, r.scenario, r.rc, r.reason = f[0], f[1], f[6], f[8]
	if !plainName(r.runID) || !plainName(r.scenario) {
		return r, fmt.Errorf("run_id %q and scenario %q must be plain names: they become file names", r.runID, r.scenario)
	}
	if r.rep, err = strconv.Atoi(f[2]); err != nil {
		return r, fmt.Errorf("rep %q: %w", f[2], err)
	}
	if r.warmStart, err = parseTime(f[3]); err != nil {
		return r, err
	}
	if r.runStart, err = parseTime(f[4]); err != nil {
		return r, err
	}
	if r.runEnd, err = parseTime(f[5]); err != nil {
		return r, err
	}
	if r.rc != "" {
		if _, err = strconv.Atoi(r.rc); err != nil {
			return r, fmt.Errorf("rc %q: %w", r.rc, err)
		}
	}
	switch f[7] {
	case "1":
		r.valid = true
	case "0":
	default:
		return r, fmt.Errorf("valid must be 1 or 0, got %q", f[7])
	}
	return r, nil
}

// parseTime accepts an empty field (a rep that never started) or ISO-8601 UTC.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return t, fmt.Errorf("time %q: %w", s, err)
	}
	return t.UTC(), nil
}

// jsonDoc is a decoded JSON object read through the typed getters below, so a
// missing or mistyped key reads as "absent" instead of failing the run.
type jsonDoc map[string]any

// loadJSON returns (nil, false, nil) when the file does not exist.
func loadJSON(path string) (jsonDoc, bool, error) {
	raw, err := readInput(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var doc jsonDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, true, fmt.Errorf("%s: %w", path, err)
	}
	return doc, true, nil
}

// path walks nested objects; it returns nil when any step is missing.
func (d jsonDoc) path(keys ...string) any {
	var cur any = map[string]any(d)
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[k]
	}
	return cur
}

func (d jsonDoc) num(keys ...string) (float64, bool) {
	return asNum(d.path(keys...))
}

func (d jsonDoc) str(keys ...string) string {
	s, _ := d.path(keys...).(string)
	return s
}

func asNum(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// nrqlPath leads to the result rows of a single-query NerdGraph response.
var nrqlPath = []string{"data", "actor", "account", "nrql", fieldResults}

// nrResults returns the rows of a raw NerdGraph NRQL response. The contract
// shape is data.actor.account.nrql.results; a response that aliases several
// queries is read by collecting every "results" array found under data.
func nrResults(doc jsonDoc) []map[string]any {
	if rows, ok := doc.path(nrqlPath...).([]any); ok {
		return objects(rows)
	}
	var out []map[string]any
	collectResults(doc.path("data"), &out)
	return out
}

func collectResults(v any, out *[]map[string]any) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if rows, isList := obj[k].([]any); isList && k == fieldResults {
			*out = append(*out, objects(rows)...)
			continue
		}
		collectResults(obj[k], out)
	}
}

func objects(rows []any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if obj, ok := r.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}

// sumField adds a numeric field across rows (facets); false when no row has it.
func sumField(rows []map[string]any, key string) (float64, bool) {
	var sum float64
	found := false
	for _, r := range rows {
		if v, ok := asNum(r[key]); ok {
			sum += v
			found = true
		}
	}
	return sum, found
}

// firstField returns the first row's numeric value for key.
func firstField(rows []map[string]any, key string) (float64, bool) {
	for _, r := range rows {
		if v, ok := asNum(r[key]); ok {
			return v, true
		}
	}
	return 0, false
}

// routeKey names a latency row: "METHOD route" when the row is also faceted
// by method, so a GET and a POST on one route stay apart.
func routeKey(row map[string]any) string {
	route := routeOf(row)
	method, _ := row[fieldMethod].(string)
	if f, ok := row[fieldFacet].([]any); ok && method == "" && len(f) > 1 {
		method, _ = f[1].(string)
	}
	if method == "" {
		return route
	}
	return method + " " + route
}

// nrUsable reports whether a NerdGraph response carries rows: an error body
// or an empty result reads as a missing file.
func nrUsable(doc jsonDoc) bool {
	if doc["errors"] != nil {
		return false
	}
	return len(nrResults(doc)) > 0
}

// routeOf names the facet of a row faceted by http.route.
func routeOf(row map[string]any) string {
	if s, ok := row[fieldRoute].(string); ok {
		return s
	}
	switch f := row[fieldFacet].(type) {
	case string:
		return f
	case []any:
		if len(f) > 0 {
			if s, ok := f[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// percentile reads the q-th percentile from a row, either as an aliased number
// ("p95") or from NRQL's percentile object ({"95": ...}).
func percentile(row map[string]any, q string) (float64, bool) {
	if v, ok := asNum(row["p"+q]); ok {
		return v, true
	}
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if obj, ok := row[k].(map[string]any); ok {
			if v, ok := asNum(obj[q]); ok {
				return v, true
			}
		}
	}
	return 0, false
}
