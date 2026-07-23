// SPDX-License-Identifier: Apache-2.0

package fleetrender

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

func TestBuildSharesStableTierOneRows(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 10, 21, 0, 0, 0, time.UTC)
	tests := []struct {
		lens        string
		record      fleetcache.Record
		wide        bool
		wantColumns []string
		wantRow     []string
	}{
		{
			lens: "pods",
			record: fleetcache.Record{
				Cluster: "prod", Namespace: "apps", Name: "api-0", Ready: "0/1", Status: "CrashLoopBackOff",
				Restarts: 7, Node: "node-1", Images: []string{"registry/api:v2"}, CreatedAt: now.Add(-time.Hour), Stale: true,
			},
			wide:        true,
			wantColumns: []string{"CLUSTER", "NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "AGE", "NODE", "IMAGE"},
			wantRow:     []string{"~prod", "apps", "api-0", "0/1", "CrashLoopBackOff", "7", "1h", "node-1", "registry/api:v2"},
		},
		{
			lens: "deploy",
			record: fleetcache.Record{
				Cluster: "prod", Namespace: "apps", Name: "api", Ready: "2/3", Status: "Progressing", CreatedAt: now.Add(-48 * time.Hour),
			},
			wantColumns: []string{"CLUSTER", "NAMESPACE", "NAME", "READY", "STATUS", "AGE"},
			wantRow:     []string{"prod", "apps", "api", "2/3", "Progressing", "2d"},
		},
		{
			lens: "events",
			record: fleetcache.Record{
				Cluster: "prod", Namespace: "apps", Status: "Warning", Reason: "BackOff", Ready: "Pod/api-0",
				Message: "container is backing off", ObservedAt: now.Add(-30 * time.Second),
			},
			wantColumns: []string{"CLUSTER", "NAMESPACE", "LAST-SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE"},
			wantRow:     []string{"prod", "apps", "30s", "Warning", "BackOff", "Pod/api-0", "container is backing off"},
		},
		{
			lens:        "nodes",
			record:      fleetcache.Record{Cluster: "prod", Name: "node-1", Status: "Ready", CreatedAt: now.Add(-10 * time.Minute), Version: "v1.36.1"},
			wantColumns: []string{"CLUSTER", "NAME", "STATUS", "AGE", "VERSION"},
			wantRow:     []string{"prod", "node-1", "Ready", "10m", "v1.36.1"},
		},
	}
	for _, test := range tests {
		t.Run(test.lens, func(t *testing.T) {
			t.Parallel()
			table := Build(fleetcache.Snapshot{Records: []fleetcache.Record{test.record}}, Options{
				Lens: test.lens, Wide: test.wide, Now: now,
			})
			if !slices.Equal(table.Columns, test.wantColumns) || len(table.Rows) != 1 || !slices.Equal(table.Rows[0], test.wantRow) {
				t.Fatalf("table = %#v, want columns=%v row=%v", table, test.wantColumns, test.wantRow)
			}
		})
	}
}

func TestWriteTextAlwaysIncludesCoverage(t *testing.T) {
	t.Parallel()
	table := Table{
		Columns: []string{"CLUSTER", "NAME"},
		Rows:    [][]string{{"alpha", "api"}},
		Coverage: fleet.Coverage{
			Requested: 3, Reachable: 2, Stale: []string{"beta"}, Unreachable: []string{"gamma"}, Truncated: []string{"alpha"},
		},
	}
	var output bytes.Buffer
	if err := WriteText(&output, table); err != nil {
		t.Fatalf("WriteText() error = %v", err)
	}
	for _, want := range []string{
		"CLUSTER", "alpha", "covered 2/3 clusters", "1 stale (beta)", "1 unreachable (gamma)", "1 truncated (alpha)",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output = %q, want %q", output.String(), want)
		}
	}
}

func TestWriteNamesUsesSourceAbstractIdentity(t *testing.T) {
	t.Parallel()
	snapshot := fleetcache.Snapshot{
		Records: []fleetcache.Record{
			{Fact: fleet.Fact{Evidence: fleet.Evidence{Ref: fleet.ResourceRef{
				SourceKind: "local-kubeconfig", Scope: "alpha", Kind: "Pod", Namespace: "apps", Name: "api-0",
			}}}},
		},
		Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	}
	var output bytes.Buffer
	if err := WriteNames(&output, snapshot); err != nil {
		t.Fatalf("WriteNames() error = %v", err)
	}
	if !strings.Contains(output.String(), "local-kubeconfig:alpha/Pod/apps/api-0") || !strings.Contains(output.String(), "covered 1/1") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestBuildRespectsRowLimitAndGenericLens(t *testing.T) {
	t.Parallel()
	snapshot := fleetcache.Snapshot{Records: []fleetcache.Record{
		{Cluster: "a", Kind: "Service", Name: "one"},
		{Cluster: "b", Kind: "Service", Name: "two"},
	}}
	table := Build(snapshot, Options{Lens: "Service", MaxRows: 1})
	if len(table.Rows) != 1 || !slices.Equal(table.Columns, []string{"CLUSTER", "NAMESPACE", "KIND", "NAME", "STATUS", "AGE"}) {
		t.Fatalf("table = %#v", table)
	}
}

func TestBuildUsesServerDisplayFieldsForGenericLens(t *testing.T) {
	t.Parallel()
	snapshot := fleetcache.Snapshot{Records: []fleetcache.Record{{
		Cluster: "alpha", Namespace: "apps", Kind: "Widget", Name: "sample",
		Display: []fleet.DisplayField{
			{Name: "Name", Value: "sample"},
			{Name: "Ready", Value: "3/3"},
			{Name: "Image", Value: "registry/widget:v1", Priority: 1},
		},
	}}}
	table := Build(snapshot, Options{Lens: "Widget"})
	if !slices.Equal(table.Columns, []string{"CLUSTER", "NAMESPACE", "NAME", "READY"}) ||
		!slices.Equal(table.Rows[0], []string{"alpha", "apps", "sample", "3/3"}) {
		t.Fatalf("generic table = %#v", table)
	}
	wide := Build(snapshot, Options{Lens: "Widget", Wide: true})
	if !slices.Equal(wide.Columns, []string{"CLUSTER", "NAMESPACE", "NAME", "READY", "IMAGE"}) {
		t.Fatalf("wide columns = %v", wide.Columns)
	}
}

func TestBuildStripsTerminalControlSequences(t *testing.T) {
	t.Parallel()
	table := Build(fleetcache.Snapshot{Records: []fleetcache.Record{{
		Cluster: "alpha\x1b[31m", Kind: "Widget",
		Display: []fleet.DisplayField{{Name: "Message\n", Value: "unsafe\x1b[2J\nnext"}},
	}}}, Options{Lens: "Widget"})
	if strings.ContainsAny(table.Columns[1], "\n\x1b") || strings.ContainsAny(table.Rows[0][0], "\n\x1b") ||
		strings.ContainsAny(table.Rows[0][1], "\n\x1b") {
		t.Fatalf("table contains terminal controls: %#v", table)
	}
}
