package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// table accumulates rows for aligned output. Output is compact on purpose: this
// tool is read by agents as often as by people, and a wide JSON dump costs
// context for no gain.
type table struct {
	header []string
	rows   [][]string
}

func newTable(header ...string) *table { return &table{header: header} }

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) render(w io.Writer) {
	if len(t.rows) == 0 {
		fmt.Fprintln(w, "(no results)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(t.header, "\t"))
	for _, row := range t.rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%d row(s)\n", len(t.rows))
}

// emitJSON pretty-prints a payload for --json.
func emitJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// renderVector prints a Prometheus instant vector as a table. Label columns are
// ordered so the most identifying ones come first, matching the CSV export in
// the web reports.
func renderVector(v promVector, valueHeader string) {
	labels := vectorLabels(v)

	header := append([]string{}, labels...)
	header = append(header, valueHeader)
	t := newTable(header...)

	type row struct {
		cells []string
		value float64
	}
	rows := make([]row, 0, len(v.Data.Result))
	for _, entry := range v.Data.Result {
		value := sampleValue(entry.Value)
		cells := make([]string, 0, len(labels)+1)
		for _, label := range labels {
			cells = append(cells, entry.Metric[label])
		}
		cells = append(cells, formatFloat(value))
		rows = append(rows, row{cells, value})
	}

	// Reports arrive ranked, but re-sorting keeps output stable when an endpoint
	// returns an unordered vector.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].value > rows[j].value })
	for _, r := range rows {
		t.add(r.cells...)
	}
	t.render(os.Stdout)
}

// vectorLabels picks which label columns to show, preferring identifying labels
// and dropping any that are empty across every sample.
func vectorLabels(v promVector) []string {
	preferred := []string{"user", "app", "category", "hostname", "lab", "building", "room", "exe"}
	present := map[string]bool{}
	for _, entry := range v.Data.Result {
		for label, value := range entry.Metric {
			if value != "" {
				present[label] = true
			}
		}
	}

	labels := make([]string, 0, len(present))
	for _, label := range preferred {
		if present[label] {
			labels = append(labels, label)
			delete(present, label)
		}
	}
	// Anything unexpected still gets shown, alphabetically, rather than silently
	// dropped — a missing column is worse than an extra one.
	extra := make([]string, 0, len(present))
	for label := range present {
		if label != "__name__" {
			extra = append(extra, label)
		}
	}
	sort.Strings(extra)
	return append(labels, extra...)
}

// sampleValue extracts the numeric value from a [timestamp, "value"] pair.
func sampleValue(pair []interface{}) float64 {
	if len(pair) < 2 {
		return 0
	}
	s, ok := pair[1].(string)
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "-"
}
