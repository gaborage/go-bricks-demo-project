package main

import (
	"bytes"
	"encoding/csv"
	"math"
	"strconv"
	"strings"
	"time"
)

// csvDecimals bounds every number in runs.csv, so the file does not depend on
// the last bit of a float that differs between CPU architectures.
const csvDecimals = 4

// runColumns is the runs.csv column order after the identity columns.
var runColumns = []string{
	mOffered, mK6RPS, mK6Reqs, mDropped, mK6P50, mK6P95, mK6P99, mErrPct, mN429,
	mNRRPS, mNRN, mGap, mWindow, mCPUS, mMcores, mIdle, mCPUPerReq,
	mThrottle, mWSAvg, mWSMax, mBytesReq, mAllocsReq,
}

var runIdentity = []string{
	"label", "scenario", "rep", "run_id", "valid", "superseded", "reason", "flags",
	"warm_start", "run_start", "run_end", "rc",
}

// renderRuns builds runs.csv: one row per attempted rep, labels in report
// order, reps in windows.tsv order. An empty cell means the input was absent.
func (s *series) renderRuns() ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(append(append([]string{}, runIdentity...), runColumns...)); err != nil {
		return nil, err
	}
	for _, label := range s.m.allLabels() {
		for _, r := range s.reps[label] {
			if err := w.Write(runRecord(r)); err != nil {
				return nil, err
			}
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func runRecord(r *rep) []string {
	rec := []string{
		r.label, r.w.scenario, strconv.Itoa(r.w.rep), r.w.runID, boolDigit(r.w.valid), boolDigit(r.superseded),
		r.w.reason, strings.Join(r.flags, ";"),
		isoTime(r.w.warmStart), isoTime(r.w.runStart), isoTime(r.w.runEnd), r.w.rc,
	}
	for _, key := range runColumns {
		v, ok := r.vals[key]
		if !ok || math.IsNaN(v) {
			rec = append(rec, "")
			continue
		}
		rec = append(rec, strconv.FormatFloat(roundTo(v, csvDecimals)+0, 'f', -1, 64))
	}
	return rec
}

func boolDigit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func isoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(secondUTC)
}
