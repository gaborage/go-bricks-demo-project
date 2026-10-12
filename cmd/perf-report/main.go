// perf-report turns a cross-version perf series into runs.csv and REPORT.md.
//
// A series directory holds one subdirectory per label, laid out as the method
// in wiki/PERF_VERSION_COMPARISON.md describes:
//
//	<series>/series-manifest.json             {"labels":["a","b","c"],"aa":{"a-aa":"a"}}
//	<series>/<label>/windows.tsv              one row per attempted rep, plus idle windows
//	<series>/<label>/k6/<scenario>-r<n>.json  k6 handleSummary data with a perfMeta block
//	<series>/<label>/nr/<query>-<run_id>.json raw NerdGraph NRQL responses (Q2, Q3, Q7, Q8, Q9, Q18)
//
// The first label is the reference. Each label is compared with the one before
// it, and from the third label on also with the reference. Only two outcomes,
// fixed before any run, get a verdict word: P1 (scenario S4, k6
// http_req_duration p95) and P2 (scenario S1, app CPU-ms per request with the
// idle level subtracted). Everything else is descriptive.
//
// Usage:
//
//	go run ./cmd/perf-report --series <dir> --out <dir> [--trim 1m]
//
// Both outputs are rebuilt from the whole series on every run, so running it
// again after a session adds that session's reps. The output is a pure function
// of the input files: no clock, no randomness.
//
// Exit codes: 0 written; 1 the series could not be read or the outputs could
// not be written; 2 usage.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	runsFile   = "runs.csv"
	reportFile = "REPORT.md"
	exitOK     = 0
	exitError  = 1
	exitUsage  = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("perf-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	seriesDir := fs.String("series", "", "series directory holding series-manifest.json and one directory per label (required)")
	outDir := fs.String("out", "", "directory for runs.csv and REPORT.md (required)")
	trim := fs.Duration("trim", time.Minute, "NR window trim on each side of a run; used only when the NR data cannot give the window")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *seriesDir == "" || *outDir == "" || fs.NArg() > 0 || *trim < 0 {
		fmt.Fprintln(stderr, "usage: perf-report --series <dir> --out <dir> [--trim 1m]")
		return exitUsage
	}
	if err := generate(*seriesDir, *outDir, *trim, stdout); err != nil {
		fmt.Fprintln(stderr, "perf-report:", err)
		return exitError
	}
	return exitOK
}

func generate(seriesDir, outDir string, trim time.Duration, stdout io.Writer) error {
	s, err := loadSeries(seriesDir, trim)
	if err != nil {
		return err
	}
	runs, err := s.renderRuns()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}
	outputs := []struct {
		name string
		data []byte
	}{{runsFile, runs}, {reportFile, []byte(s.renderReport())}}
	for _, o := range outputs {
		path := filepath.Join(outDir, o.name)
		if err := os.WriteFile(path, o.data, 0o600); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "wrote", path)
	}
	return nil
}
