// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

func TestGetRequiresExplicitFleetScope(t *testing.T) {
	reader := &cacheReader{}
	_, stderr, exitCode := runCLIWithReader(t, []string{"get", "pods", "-A"}, reader)
	if exitCode == 0 || !strings.Contains(stderr, "choose exactly one") {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	if reader.queryCount() != 0 {
		t.Fatalf("query count = %d, want validation before connector access", reader.queryCount())
	}
}

func TestGetRendersCacheWithPartialCoverageWarning(t *testing.T) {
	reader := &cacheReader{unreachable: "beta"}
	stdout, stderr, exitCode := runCLIWithReader(t, []string{"get", "pods", "-A", "--all-clusters"}, reader)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	for _, want := range []string{"CLUSTER", "payments-0", "covered 1/2 clusters", "1 unreachable (beta)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	if !strings.Contains(stderr, "warning: covered 1/2 clusters") {
		t.Fatalf("stderr = %q, want partial warning", stderr)
	}
}

func TestGetJSONUsesStableCacheSchema(t *testing.T) {
	reader := &cacheReader{}
	stdout, stderr, exitCode := runCLIWithReader(t, []string{"get", "pods", "-A", "--context", "alpha", "-o", "json"}, reader)
	if exitCode != 0 {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	var snapshot fleetcache.Snapshot
	if err := json.Unmarshal([]byte(stdout), &snapshot); err != nil {
		t.Fatalf("unmarshal output %q: %v", stdout, err)
	}
	if len(snapshot.Records) != 1 || snapshot.Records[0].Cluster != "alpha" || snapshot.Coverage.Requested != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestSearchAndCorrelateUseNormalizedCrossClusterCache(t *testing.T) {
	reader := &cacheReader{}
	stdout, stderr, exitCode := runCLIWithReader(t, []string{"search", "image:*log4j*"}, reader)
	if exitCode != 0 {
		t.Fatalf("search exit/stderr = %d/%q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "payments-0") || strings.Contains(stdout, "worker-0") || !strings.Contains(stdout, "covered 2/2") {
		t.Fatalf("search stdout = %q", stdout)
	}

	stdout, stderr, exitCode = runCLIWithReader(t, []string{"correlate", "deploy/payments", "status!=Healthy"}, reader)
	if exitCode != 0 {
		t.Fatalf("correlate exit/stderr = %d/%q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "beta") || strings.Contains(stdout, "alpha       apps") || !strings.Contains(stdout, "Degraded") {
		t.Fatalf("correlate stdout = %q", stdout)
	}
}

func TestGetTotalFailureIsNonZeroAfterCoverageOutput(t *testing.T) {
	reader := &cacheReader{unreachable: "all"}
	stdout, stderr, exitCode := runCLIWithReader(t, []string{"get", "pods", "-A", "--all-clusters"}, reader)
	if exitCode == 0 {
		t.Fatalf("exit = 0, stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, "covered 0/2") || !strings.Contains(stderr, "reached 0/2") {
		t.Fatalf("stdout/stderr = %q/%q", stdout, stderr)
	}
}

func TestNonTerminalRootStaysScriptSafe(t *testing.T) {
	reader := &cacheReader{}
	stdout, stderr, exitCode := runCLIWithReader(t, nil, reader)
	if exitCode != 0 || !strings.Contains(stdout, "Usage:") || stderr != "" {
		t.Fatalf("root exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
	_, stderr, exitCode = runCLIWithReader(t, []string{"tui"}, reader)
	if exitCode == 0 || !strings.Contains(stderr, "TUI input is unavailable") {
		t.Fatalf("tui exit/stderr = %d/%q", exitCode, stderr)
	}
}

func runCLIWithReader(t *testing.T, args []string, reader connector.Reader) (stdout, stderr string, exitCode int) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SITH_LOG_LEVEL", "")
	t.Setenv("SITH_LOG_FORMAT", "")
	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	exitCode = executeWithReader(args, reader, &stdoutBuffer, &stderrBuffer)
	return stdoutBuffer.String(), stderrBuffer.String(), exitCode
}

type cacheReader struct {
	mu          sync.Mutex
	queries     int
	unreachable string
}

func (*cacheReader) Kind() string { return "cache-test" }

func (*cacheReader) Capabilities() []connector.Capability {
	return []connector.Capability{connector.CapDiscover, connector.CapRead, connector.CapQuery}
}

func (reader *cacheReader) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Kind: reader.Kind(), ConnKind: connector.KindReadAdapter, ProtocolV: "1.0.0", Owner: "test",
		Capabilities: reader.Capabilities(),
	}
}

func (reader *cacheReader) Discover(_ context.Context) (connector.Discovery, error) {
	now := time.Now().UTC()
	alpha := connector.Scope{Name: "alpha", Reachable: reader.unreachable != "all", ObservedAt: now}
	beta := connector.Scope{Name: "beta", Reachable: reader.unreachable == "", ObservedAt: now}
	unreachable := []string{}
	if !alpha.Reachable {
		unreachable = append(unreachable, "alpha")
	}
	if !beta.Reachable {
		unreachable = append(unreachable, "beta")
	}
	return connector.Discovery{Scopes: []connector.Scope{alpha, beta}, Unreachable: unreachable}, nil
}

func (*cacheReader) Read(_ context.Context, _ fleet.ResourceRef) (fleet.Evidence, error) {
	return fleet.Evidence{}, errors.New("not used")
}

func (reader *cacheReader) Query(_ context.Context, query fleet.Query) (fleet.QueryResult, error) {
	reader.mu.Lock()
	reader.queries++
	reader.mu.Unlock()
	unreachable := []string{}
	live := []string{"alpha", "beta"}
	switch reader.unreachable {
	case "all":
		unreachable = []string{"alpha", "beta"}
		live = nil
	case "beta":
		unreachable = []string{"beta"}
		live = []string{"alpha"}
	}
	facts := make([]fleet.Fact, 0, len(live))
	for _, scope := range live {
		switch query.Selector.ResourceKind {
		case "Pod":
			name, image, status := "worker-0", "registry/worker:v1", "Running"
			if scope == "alpha" {
				name, image, status = "payments-0", "registry/payments:log4j-fix", "CrashLoopBackOff"
			}
			facts = append(facts, cacheObjectFact("Pod", scope, name, image, status, 7))
		case "Deployment":
			available := 3
			if scope == "beta" {
				available = 0
			}
			facts = append(facts, cacheDeploymentFact(scope, available))
		}
	}
	return fleet.QueryResult{
		Facts: facts,
		Coverage: fleet.Coverage{
			Requested: 2, Reachable: len(live), Unreachable: unreachable,
		},
	}, nil
}

func (reader *cacheReader) queryCount() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.queries
}

func cacheObjectFact(kind, scope, name, image, status string, restarts int) fleet.Fact {
	object := map[string]any{
		"apiVersion": "v1", "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": "apps", "creationTimestamp": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": image}}},
		"status": map[string]any{
			"phase": "Running",
			"containerStatuses": []any{map[string]any{
				"ready": status == "Running", "restartCount": restarts,
				"state": map[string]any{"waiting": map[string]any{"reason": status}},
			}},
		},
	}
	return cacheFact(kind, scope, name, object)
}

func cacheDeploymentFact(scope string, available int) fleet.Fact {
	object := map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "payments", "namespace": "apps"},
		"spec":     map[string]any{"replicas": 3},
		"status":   map[string]any{"availableReplicas": available, "updatedReplicas": available},
	}
	return cacheFact("Deployment", scope, "payments", object)
}

func cacheFact(kind, scope, name string, object map[string]any) fleet.Fact {
	payload, _ := json.Marshal(object)
	return fleet.Fact{Evidence: fleet.Evidence{
		Ref:  fleet.ResourceRef{SourceKind: "cache-test", Scope: scope, Kind: kind, Namespace: "apps", Name: name},
		Kind: fleet.FactInventory, Observed: payload, ObservedAt: time.Now().UTC(), Source: scope,
	}, Workspace: fleet.LocalWorkspace}
}
