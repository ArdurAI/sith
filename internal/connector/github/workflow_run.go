// SPDX-License-Identifier: Apache-2.0

package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// WorkflowRunProtocolVersion identifies the normalized Get-a-workflow-run fact contract.
	WorkflowRunProtocolVersion = "workflow-runs/" + APIVersion

	workflowRunFailureKind = "workflow-run-failed"
)

// WorkflowRunProjection supplies one already-authorized GitHub Get-a-workflow-run response.
// ProjectFailedWorkflowRun performs no discovery, network access, credential loading, persistence,
// repository-to-workload correlation, or mutation.
type WorkflowRunProjection struct {
	Workspace  string
	Host       string
	Owner      string
	Repository string
	RunID      int64
	ObservedAt time.Time
	Response   []byte
}

type workflowRunResponse struct {
	ID         *int64                 `json:"id"`
	WorkflowID *int64                 `json:"workflow_id"`
	RunAttempt *int64                 `json:"run_attempt"`
	Status     *string                `json:"status"`
	Conclusion *string                `json:"conclusion"`
	UpdatedAt  *string                `json:"updated_at"`
	Repository *workflowRunRepository `json:"repository"`
}

type workflowRunRepository struct {
	FullName *string `json:"full_name"`
}

func (response *workflowRunResponse) UnmarshalJSON(document []byte) error {
	type exactField struct {
		name   string
		target any
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return fmt.Errorf("workflow-run response must be a JSON object")
	}
	for _, field := range []exactField{
		{name: "id", target: &response.ID},
		{name: "workflow_id", target: &response.WorkflowID},
		{name: "run_attempt", target: &response.RunAttempt},
		{name: "status", target: &response.Status},
		{name: "conclusion", target: &response.Conclusion},
		{name: "updated_at", target: &response.UpdatedAt},
		{name: "repository", target: &response.Repository},
	} {
		value, exists := fields[field.name]
		if !exists {
			continue
		}
		if err := json.Unmarshal(value, field.target); err != nil {
			return fmt.Errorf("%s is invalid", field.name)
		}
	}
	return nil
}

func (repository *workflowRunRepository) UnmarshalJSON(document []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return fmt.Errorf("workflow-run repository must be a JSON object")
	}
	value, exists := fields["full_name"]
	if !exists {
		return nil
	}
	if err := json.Unmarshal(value, &repository.FullName); err != nil {
		return fmt.Errorf("full_name is invalid")
	}
	return nil
}

type workflowRunObservation struct {
	RunID      int64     `json:"run_id"`
	WorkflowID int64     `json:"workflow_id"`
	RunAttempt int64     `json:"run_attempt"`
	ChangeKind string    `json:"change_kind"`
	Conclusion string    `json:"conclusion"`
	EventAt    time.Time `json:"event_at"`
}

// ProjectFailedWorkflowRun returns one deterministic, bounded TIMELINE fact only when GitHub
// reports an internally consistent completed workflow run with an explicit failure conclusion.
// Valid incomplete and non-failure completed runs abstain with no facts.
func ProjectFailedWorkflowRun(input WorkflowRunProjection) ([]fleet.GraphFact, error) {
	if err := validateWorkflowRunProjection(input); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSON(input.Response); err != nil {
		return nil, fmt.Errorf("decode GitHub workflow-run response: %w", err)
	}

	var response workflowRunResponse
	decoder := json.NewDecoder(bytes.NewReader(input.Response))
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode GitHub workflow-run response: %w", err)
	}
	observation, failed, err := failedWorkflowRunObservation(response, input)
	if err != nil {
		return nil, err
	}
	if !failed {
		return []fleet.GraphFact{}, nil
	}

	encoded, err := json.Marshal(observation)
	if err != nil {
		return nil, fmt.Errorf("encode GitHub workflow-run change fact: %w", err)
	}
	if len(encoded) > maxFactPayloadBytes {
		return nil, fmt.Errorf("GitHub workflow-run change fact exceeds %d encoded bytes", maxFactPayloadBytes)
	}

	resourceName := workflowRunResourceName(input.Repository, input.RunID, observation.RunAttempt)
	nativeID := input.Owner + "/" + resourceName
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind,
					Scope:      input.Host,
					Kind:       "WorkflowRun",
					Namespace:  input.Owner,
					Name:       resourceName,
				},
				Kind:       fleet.FactChange,
				Observed:   encoded,
				ObservedAt: observation.EventAt,
				Source:     input.Host,
				Provenance: fleet.Provenance{
					Adapter: Kind, ProtocolV: WorkflowRunProtocolVersion, NativeID: nativeID,
				},
			},
			Workspace: input.Workspace,
		},
		Lens: fleet.LensTimeline,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return nil, fmt.Errorf("validate GitHub workflow-run change fact: %w", err)
	}
	return []fleet.GraphFact{fact}, nil
}

func validateWorkflowRunProjection(input WorkflowRunProjection) error {
	if err := validateText("workspace", input.Workspace, maxWorkspaceBytes); err != nil {
		return err
	}
	if err := validateHost(input.Host); err != nil {
		return err
	}
	if err := validatePathComponent("owner", input.Owner, maxOwnerBytes); err != nil {
		return err
	}
	if err := validatePathComponent("repository", input.Repository, maxRepositoryBytes); err != nil {
		return err
	}
	if strings.HasSuffix(strings.ToLower(input.Repository), ".git") {
		return fmt.Errorf("repository must not include the .git suffix")
	}
	if input.RunID <= 0 {
		return fmt.Errorf("workflow run ID must be positive")
	}
	if len(workflowRunResourceName(input.Repository, input.RunID, 1)) > maxResourceName {
		return fmt.Errorf("workflow-run resource name exceeds %d bytes", maxResourceName)
	}
	if input.ObservedAt.IsZero() {
		return fmt.Errorf("projection observation time is required")
	}
	if len(input.Response) == 0 {
		return fmt.Errorf("GitHub workflow-run response is required")
	}
	if len(input.Response) > maxResponseBytes {
		return fmt.Errorf("GitHub workflow-run response exceeds %d bytes", maxResponseBytes)
	}
	if !utf8.Valid(input.Response) {
		return fmt.Errorf("GitHub workflow-run response must be valid UTF-8")
	}
	return nil
}

func failedWorkflowRunObservation(
	response workflowRunResponse,
	input WorkflowRunProjection,
) (workflowRunObservation, bool, error) {
	if response.ID == nil || *response.ID != input.RunID {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run ID does not match trusted caller identity")
	}
	if response.WorkflowID == nil || *response.WorkflowID <= 0 {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow ID must be positive")
	}
	if response.RunAttempt == nil || *response.RunAttempt <= 0 {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run attempt must be positive")
	}
	if response.Repository == nil || response.Repository.FullName == nil ||
		!validWorkflowRunRepositoryIdentity(*response.Repository.FullName, input.Owner, input.Repository) {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run repository does not match trusted caller identity")
	}
	if response.Status == nil {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run status is required")
	}
	switch *response.Status {
	case "queued", "in_progress", "requested", "waiting", "pending", "completed":
	default:
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run status %q is unsupported", *response.Status)
	}
	if response.UpdatedAt == nil || *response.UpdatedAt == "" {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run updated_at is required")
	}
	eventAt, err := time.Parse(time.RFC3339Nano, *response.UpdatedAt)
	if err != nil || eventAt.IsZero() {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run updated_at must be a non-zero RFC3339 timestamp")
	}
	eventAt = eventAt.UTC()
	if eventAt.After(input.ObservedAt.UTC().Add(maxClockSkew)) {
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run update time exceeds allowed collection clock skew")
	}

	if *response.Status != "completed" {
		if response.Conclusion != nil {
			return workflowRunObservation{}, false, fmt.Errorf("incomplete GitHub workflow run must not have a conclusion")
		}
		return workflowRunObservation{}, false, nil
	}
	if response.Conclusion == nil {
		return workflowRunObservation{}, false, fmt.Errorf("completed GitHub workflow-run conclusion is required")
	}
	failed := false
	switch *response.Conclusion {
	case "failure", "timed_out", "startup_failure":
		failed = true
	case "action_required", "cancelled", "neutral", "skipped", "stale", "success": //nolint:misspell // GitHub's wire value uses British spelling.
	default:
		return workflowRunObservation{}, false, fmt.Errorf("GitHub workflow-run conclusion %q is unsupported", *response.Conclusion)
	}
	if !failed {
		return workflowRunObservation{}, false, nil
	}

	return workflowRunObservation{
		RunID:      *response.ID,
		WorkflowID: *response.WorkflowID,
		RunAttempt: *response.RunAttempt,
		ChangeKind: workflowRunFailureKind,
		Conclusion: *response.Conclusion,
		EventAt:    eventAt,
	}, true, nil
}

func validWorkflowRunRepositoryIdentity(fullName, owner, repository string) bool {
	responseOwner, responseRepository, found := strings.Cut(fullName, "/")
	if !found || strings.Contains(responseRepository, "/") ||
		validatePathComponent("response owner", responseOwner, maxOwnerBytes) != nil ||
		validatePathComponent("response repository", responseRepository, maxRepositoryBytes) != nil ||
		strings.HasSuffix(strings.ToLower(responseRepository), ".git") {
		return false
	}
	return strings.EqualFold(responseOwner, owner) && strings.EqualFold(responseRepository, repository)
}

func workflowRunResourceName(repository string, runID, attempt int64) string {
	return repository + "#" + strconv.FormatInt(runID, 10) + "-attempt-" + strconv.FormatInt(attempt, 10)
}
