// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

const (
	defaultLimit = 500
	maximumLimit = 5000
)

type inventoryInput struct {
	Kind   string   `json:"kind,omitempty" jsonschema:"optional Kubernetes resource kind"`
	Scopes []string `json:"scopes,omitempty" jsonschema:"optional kubeconfig context names or globs"`
	Limit  int      `json:"limit,omitempty" jsonschema:"maximum records from 1 to 5000"`
}

type healthInput struct {
	Kind   string   `json:"kind,omitempty" jsonschema:"optional Kubernetes resource kind"`
	Status string   `json:"status,omitempty" jsonschema:"optional normalized status"`
	Scopes []string `json:"scopes,omitempty" jsonschema:"optional kubeconfig context names or globs"`
	Limit  int      `json:"limit,omitempty" jsonschema:"maximum records from 1 to 5000"`
}

type correlateInput struct {
	Expression string   `json:"expression" jsonschema:"correlation expression such as deploy/payments status!=Healthy"`
	Scopes     []string `json:"scopes,omitempty" jsonschema:"optional kubeconfig context names or globs"`
	Limit      int      `json:"limit,omitempty" jsonschema:"maximum records from 1 to 5000"`
}

type cveSearchInput struct {
	Image  string   `json:"image,omitempty" jsonschema:"optional image name or glob"`
	CVE    string   `json:"cve,omitempty" jsonschema:"optional CVE identifier or glob"`
	Scopes []string `json:"scopes,omitempty" jsonschema:"optional kubeconfig context names or globs"`
	Limit  int      `json:"limit,omitempty" jsonschema:"maximum records from 1 to 5000"`
}

type toolOutput struct {
	Workspace string         `json:"workspace"`
	Snapshot  snapshotOutput `json:"snapshot"`
}

type snapshotOutput struct {
	Version   uint64            `json:"version"`
	State     fleetcache.State  `json:"state"`
	Records   []recordOutput    `json:"records"`
	Coverage  fleet.Coverage    `json:"coverage"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Scopes    []connector.Scope `json:"scopes"`
}

type recordOutput struct {
	Workspace  string            `json:"workspace"`
	Kind       string            `json:"kind"`
	Cluster    string            `json:"cluster"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Ready      string            `json:"ready,omitempty"`
	Status     string            `json:"status,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Node       string            `json:"node,omitempty"`
	Version    string            `json:"version,omitempty"`
	Restarts   int64             `json:"restarts,omitempty"`
	Images     []string          `json:"images,omitempty"`
	CVEs       []string          `json:"cves,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
	Stale      bool              `json:"stale"`
	StaleFor   string            `json:"stale_for,omitempty"`
}

func (app *application) addTools(server *mcp.Server) {
	annotations := &mcp.ToolAnnotations{
		ReadOnlyHint: true, IdempotentHint: true,
		DestructiveHint: boolPointer(false), OpenWorldHint: boolPointer(false),
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "fleet.inventory", Description: "Read workspace-scoped normalized fleet inventory from the local cache",
		Annotations: annotations,
	}, app.inventory)
	mcp.AddTool(server, &mcp.Tool{
		Name: "fleet.health", Description: "Read workspace-scoped fleet health and coverage from the local cache",
		Annotations: annotations,
	}, app.health)
	mcp.AddTool(server, &mcp.Tool{
		Name: "fleet.correlate", Description: "Run a workspace-scoped cross-cluster correlation over the local cache",
		Annotations: annotations,
	}, app.correlate)
	mcp.AddTool(server, &mcp.Tool{
		Name: "fleet.cve-search", Description: "Search workspace-scoped image and CVE facts in the local cache",
		Annotations: annotations,
	}, app.cveSearch)
}

func (app *application) inventory(ctx context.Context, _ *mcp.CallToolRequest, input inventoryInput) (*mcp.CallToolResult, toolOutput, error) {
	limit, err := normalizedLimit(input.Limit)
	if err != nil {
		return nil, toolOutput{}, err
	}
	return app.execute(ctx, "fleet.inventory", fleetcache.Query{
		Kind: strings.TrimSpace(input.Kind), Scopes: cleanScopes(input.Scopes), Limit: limit,
	})
}

func (app *application) health(ctx context.Context, _ *mcp.CallToolRequest, input healthInput) (*mcp.CallToolResult, toolOutput, error) {
	limit, err := normalizedLimit(input.Limit)
	if err != nil {
		return nil, toolOutput{}, err
	}
	return app.execute(ctx, "fleet.health", fleetcache.Query{
		Kind: strings.TrimSpace(input.Kind), Status: strings.TrimSpace(input.Status),
		Scopes: cleanScopes(input.Scopes), Limit: limit,
	})
}

func (app *application) correlate(ctx context.Context, _ *mcp.CallToolRequest, input correlateInput) (*mcp.CallToolResult, toolOutput, error) {
	query, err := fleetcache.ParseCorrelation(input.Expression)
	if err != nil {
		return nil, toolOutput{}, err
	}
	query.Limit, err = normalizedLimit(input.Limit)
	if err != nil {
		return nil, toolOutput{}, err
	}
	if len(input.Scopes) > 0 {
		query.Scopes = cleanScopes(input.Scopes)
	}
	return app.execute(ctx, "fleet.correlate", query)
}

func (app *application) cveSearch(ctx context.Context, _ *mcp.CallToolRequest, input cveSearchInput) (*mcp.CallToolResult, toolOutput, error) {
	if strings.TrimSpace(input.Image) == "" && strings.TrimSpace(input.CVE) == "" {
		return nil, toolOutput{}, fmt.Errorf("fleet.cve-search requires image, cve, or both")
	}
	limit, err := normalizedLimit(input.Limit)
	if err != nil {
		return nil, toolOutput{}, err
	}
	return app.execute(ctx, "fleet.cve-search", fleetcache.Query{
		Image: strings.TrimSpace(input.Image), CVE: strings.ToUpper(strings.TrimSpace(input.CVE)),
		Scopes: cleanScopes(input.Scopes), Limit: limit,
	})
}

func (app *application) execute(
	ctx context.Context,
	tool string,
	query fleetcache.Query,
) (*mcp.CallToolResult, toolOutput, error) {
	started := app.now()
	event := AuditEvent{At: started.UTC(), Tool: tool, Workspace: fleet.LocalWorkspace}
	actor, err := app.authorizeExecution(ctx)
	if err != nil {
		event.Err = err.Error()
		event.Duration = app.now().Sub(started)
		app.auditor.Record(ctx, event)
		return nil, toolOutput{}, err
	}
	event.Actor, event.Allowed = actor, true
	snapshot := app.store.Query(fleet.LocalWorkspace, query)
	event.Records = len(snapshot.Records)
	event.Duration = app.now().Sub(started)
	app.auditor.Record(ctx, event)
	return &mcp.CallToolResult{}, toolOutput{Workspace: fleet.LocalWorkspace, Snapshot: projectSnapshot(snapshot)}, nil
}

func projectSnapshot(snapshot fleetcache.Snapshot) snapshotOutput {
	output := snapshotOutput{
		Version: snapshot.Version, State: snapshot.State, Coverage: snapshot.Coverage,
		UpdatedAt: snapshot.UpdatedAt, Scopes: append([]connector.Scope(nil), snapshot.Scopes...),
		Records: make([]recordOutput, 0, len(snapshot.Records)),
	}
	for _, record := range snapshot.Records {
		labels := make(map[string]string, len(record.Labels))
		for key, value := range record.Labels {
			labels[key] = value
		}
		output.Records = append(output.Records, recordOutput{
			Workspace: record.Workspace, Kind: record.Kind, Cluster: record.Cluster,
			Namespace: record.Namespace, Name: record.Name, Ready: record.Ready, Status: record.Status,
			Reason: record.Reason, Node: record.Node, Version: record.Version, Restarts: record.Restarts,
			Images: append([]string(nil), record.Images...), CVEs: append([]string(nil), record.CVEs...),
			Labels: labels, ObservedAt: record.ObservedAt, Stale: record.Stale,
			StaleFor: record.Fact.StaleFor,
		})
	}
	return output
}

func normalizedLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 1 || limit > maximumLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", maximumLimit)
	}
	return limit, nil
}

func cleanScopes(scopes []string) []string {
	cleaned := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope = strings.TrimSpace(scope); scope != "" {
			cleaned = append(cleaned, scope)
		}
	}
	return cleaned
}

func boolPointer(value bool) *bool { return &value }
