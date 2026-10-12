package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	flagSeries = "--series"
	flagOut    = "--out"
)

// update rewrites the golden files: go test ./cmd/perf-report -run TestGolden -update
var update = flag.Bool("update", false, "rewrite testdata/golden from the fixture series")

// extractSeries expands testdata/series.txtar into a temporary series
// directory. Each "-- <path> --" line starts a file; the lines after it, up to
// the next marker, are its content. Text before the first marker is a comment.
func extractSeries(t testing.TB) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "series.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var path string
	var body []string
	flush := func() {
		if path != "" {
			writeFile(t, filepath.Join(dir, filepath.FromSlash(path)), strings.Join(body, "\n")+"\n")
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if name, ok := strings.CutPrefix(line, "-- "); ok && strings.HasSuffix(name, " --") {
			flush()
			path, body = strings.TrimSuffix(name, " --"), nil
			continue
		}
		body = append(body, line)
	}
	flush()
	return dir
}

// TestGolden renders the synthetic series in testdata/series.txtar and
// compares both outputs byte for byte. The fixture covers three labels and an A/A
// label, a void rep re-run under the same run_id, a summary without p(99) or
// dropped_iterations, a throttled rep that makes a P2 verdict
// environment-limited, an nr-gap rep, a simulator route left out of the rate
// gap, and a staircase with and without its Q18 series.
func TestGolden(t *testing.T) {
	out := t.TempDir()
	if code := run([]string{flagSeries, extractSeries(t), flagOut, out}, io.Discard, os.Stderr); code != exitOK {
		t.Fatalf("run exit = %d, want %d", code, exitOK)
	}
	for _, name := range []string{reportFile, runsFile} {
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", "golden", name)
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, got, 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run with -update to create it)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from %s; run with -update and review the diff", name, golden)
		}
	}
}

// TestGoldenIsStable renders twice: the output must not depend on map order.
func TestGoldenIsStable(t *testing.T) {
	var first []byte
	dir := extractSeries(t)
	for i := 0; i < 5; i++ {
		s, err := loadSeries(dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := []byte(s.renderReport())
		if first == nil {
			first = got
		} else if !bytes.Equal(first, got) {
			t.Fatal("REPORT.md differs between two renders of the same series")
		}
	}
}

func TestRunExitCodes(t *testing.T) {
	bad := t.TempDir()
	writeFile(t, filepath.Join(bad, manifestFile), `{"labels":["one"]}`)
	writeFile(t, filepath.Join(bad, "one", windowsFile), "wrong header\n")
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "no flags", args: nil, want: exitUsage},
		{name: "missing --out", args: []string{flagSeries, "x"}, want: exitUsage},
		{name: "stray argument", args: []string{flagSeries, "x", flagOut, "y", "z"}, want: exitUsage},
		{name: "unknown flag", args: []string{"--nope"}, want: exitUsage},
		{name: "help", args: []string{"-h"}, want: exitOK},
		{name: "no manifest", args: []string{flagSeries, t.TempDir(), flagOut, t.TempDir()}, want: exitError},
		{name: "bad windows.tsv header", args: []string{flagSeries, bad, flagOut, t.TempDir()}, want: exitError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(tt.args, io.Discard, io.Discard); got != tt.want {
				t.Errorf("exit = %d, want %d", got, tt.want)
			}
		})
	}
}
