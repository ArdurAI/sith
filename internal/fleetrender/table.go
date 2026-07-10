// SPDX-License-Identifier: Apache-2.0

// Package fleetrender builds the shared cache-backed tables used by CLI and TUI surfaces.
package fleetrender

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

// Options selects a lens and render shape without changing the underlying cache query.
type Options struct {
	Lens    string
	Wide    bool
	MaxRows int
	Now     time.Time
}

// Table is the stable surface-neutral representation shared by CLI and TUI.
type Table struct {
	Columns  []string         `json:"columns"`
	Rows     [][]string       `json:"rows"`
	Coverage fleet.Coverage   `json:"coverage"`
	State    fleetcache.State `json:"state"`
}

// Build projects one immutable store snapshot into deterministic columns and rows.
func Build(snapshot fleetcache.Snapshot, options Options) Table {
	lens := canonicalLens(options.Lens)
	if lens == "" && len(snapshot.Records) > 0 {
		lens = canonicalLens(snapshot.Records[0].Kind)
	}
	columns := columnsFor(lens, options.Wide)
	rows := make([][]string, 0, len(snapshot.Records))
	now := options.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, record := range snapshot.Records {
		rows = append(rows, rowFor(record, lens, options.Wide, now))
		if options.MaxRows > 0 && len(rows) == options.MaxRows {
			break
		}
	}
	return Table{Columns: columns, Rows: rows, Coverage: snapshot.Coverage, State: snapshot.State}
}

// WriteText writes a pipe-friendly table followed by the mandatory coverage line.
func WriteText(output io.Writer, table Table) error {
	var rendered bytes.Buffer
	tabular := tabwriter.NewWriter(&rendered, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tabular, strings.Join(table.Columns, "\t")); err != nil {
		return fmt.Errorf("write table header: %w", err)
	}
	for _, row := range table.Rows {
		if _, err := fmt.Fprintln(tabular, strings.Join(row, "\t")); err != nil {
			return fmt.Errorf("write table row: %w", err)
		}
	}
	if err := tabular.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}
	if _, err := io.Copy(output, &rendered); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	if _, err := fmt.Fprintln(output, CoverageLine(table.Coverage)); err != nil {
		return fmt.Errorf("write coverage: %w", err)
	}
	return nil
}

// WriteNames writes stable source-abstract resource addresses and a coverage line.
func WriteNames(output io.Writer, snapshot fleetcache.Snapshot) error {
	for _, record := range snapshot.Records {
		if _, err := fmt.Fprintln(output, record.Fact.Ref.String()); err != nil {
			return fmt.Errorf("write resource name: %w", err)
		}
	}
	if _, err := fmt.Fprintln(output, CoverageLine(snapshot.Coverage)); err != nil {
		return fmt.Errorf("write coverage: %w", err)
	}
	return nil
}

// CoverageLine renders coverage honesty identically on every text surface.
func CoverageLine(coverage fleet.Coverage) string {
	parts := []string{fmt.Sprintf("covered %d/%d clusters", coverage.Reachable, coverage.Requested)}
	if len(coverage.Stale) == 0 {
		parts = append(parts, "0 stale")
	} else {
		parts = append(parts, fmt.Sprintf("%d stale (%s)", len(coverage.Stale), strings.Join(coverage.Stale, ", ")))
	}
	if len(coverage.Unreachable) == 0 {
		parts = append(parts, "0 unreachable")
	} else {
		parts = append(parts, fmt.Sprintf("%d unreachable (%s)", len(coverage.Unreachable), strings.Join(coverage.Unreachable, ", ")))
	}
	return strings.Join(parts, " · ")
}

func columnsFor(lens string, wide bool) []string {
	var columns []string
	switch lens {
	case "Deployment":
		columns = []string{"CLUSTER", "NAMESPACE", "NAME", "READY", "STATUS", "AGE"}
		if wide {
			columns = append(columns, "IMAGE")
		}
	case "Event":
		columns = []string{"CLUSTER", "NAMESPACE", "LAST-SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE"}
	case "Node":
		columns = []string{"CLUSTER", "NAME", "STATUS", "AGE", "VERSION"}
	case "Pod":
		columns = []string{"CLUSTER", "NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "AGE"}
		if wide {
			columns = append(columns, "NODE", "IMAGE")
		}
	default:
		columns = []string{"CLUSTER", "NAMESPACE", "KIND", "NAME", "STATUS", "AGE"}
	}
	return columns
}

func rowFor(record fleetcache.Record, lens string, wide bool, now time.Time) []string {
	cluster := record.Cluster
	if record.Stale {
		cluster = "~" + cluster
	}
	age := humanAge(now, record.CreatedAt)
	switch lens {
	case "Deployment":
		row := []string{cluster, record.Namespace, record.Name, record.Ready, record.Status, age}
		if wide {
			row = append(row, strings.Join(record.Images, ","))
		}
		return row
	case "Event":
		return []string{cluster, record.Namespace, humanAge(now, record.ObservedAt), record.Status, record.Reason, record.Ready, truncate(record.Message, 72)}
	case "Node":
		return []string{cluster, record.Name, record.Status, age, record.Version}
	case "Pod":
		row := []string{cluster, record.Namespace, record.Name, record.Ready, record.Status, strconv.FormatInt(record.Restarts, 10), age}
		if wide {
			row = append(row, record.Node, strings.Join(record.Images, ","))
		}
		return row
	default:
		return []string{cluster, record.Namespace, record.Kind, record.Name, record.Status, age}
	}
}

func humanAge(now, then time.Time) string {
	if then.IsZero() {
		return "-"
	}
	age := now.Sub(then)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func canonicalLens(lens string) string {
	switch strings.ToLower(strings.TrimSpace(lens)) {
	case "pod", "pods", "po":
		return "Pod"
	case "deployment", "deployments", "deploy":
		return "Deployment"
	case "event", "events", "ev":
		return "Event"
	case "node", "nodes", "no":
		return "Node"
	default:
		return strings.TrimSpace(lens)
	}
}
