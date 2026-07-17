// SPDX-License-Identifier: Apache-2.0

// Package github normalizes read-only GitHub pull-request evidence for Sith's operational graph.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// Kind is the stable registry identifier for GitHub read evidence.
	Kind = "github"
	// APIVersion is the GitHub REST API version whose pull-response contract is normalized here.
	APIVersion = "2026-03-10"
	// ProtocolVersion identifies the normalized Get-a-pull-request fact contract.
	ProtocolVersion = "pulls/" + APIVersion

	maxResponseBytes    = 512 << 10
	maxWorkspaceBytes   = 253
	maxHostBytes        = 253
	maxOwnerBytes       = 100
	maxRepositoryBytes  = 100
	maxResourceName     = 253
	maxFactPayloadBytes = 4 << 10
	maxJSONDepth        = 64
	maxClockSkew        = 5 * time.Minute
)

// Projection supplies one already-authorized GitHub Get-a-pull-request response.
// ProjectMergedPullRequest does not perform discovery, network access, credential loading,
// persistence, repository-to-workload correlation, or mutation.
type Projection struct {
	Workspace  string
	Host       string
	Owner      string
	Repository string
	PullNumber int64
	ObservedAt time.Time
	Response   []byte
}

type pullResponse struct {
	Number         *int64      `json:"number"`
	State          *string     `json:"state"`
	Draft          *bool       `json:"draft"`
	Merged         *bool       `json:"merged"`
	MergedAt       *string     `json:"merged_at"`
	MergeCommitSHA *string     `json:"merge_commit_sha"`
	Head           *pullCommit `json:"head"`
	Base           *pullCommit `json:"base"`
}

type pullCommit struct {
	SHA *string `json:"sha"`
}

func (response *pullResponse) UnmarshalJSON(document []byte) error {
	type exactField struct {
		name   string
		target any
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return err
	}
	for _, field := range []exactField{
		{name: "number", target: &response.Number},
		{name: "state", target: &response.State},
		{name: "draft", target: &response.Draft},
		{name: "merged", target: &response.Merged},
		{name: "merged_at", target: &response.MergedAt},
		{name: "merge_commit_sha", target: &response.MergeCommitSHA},
		{name: "head", target: &response.Head},
		{name: "base", target: &response.Base},
	} {
		value, exists := fields[field.name]
		if !exists {
			continue
		}
		if err := json.Unmarshal(value, field.target); err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
	}
	return nil
}

func (commit *pullCommit) UnmarshalJSON(document []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return err
	}
	value, exists := fields["sha"]
	if !exists {
		return nil
	}
	if err := json.Unmarshal(value, &commit.SHA); err != nil {
		return fmt.Errorf("sha: %w", err)
	}
	return nil
}

type changeObservation struct {
	PullNumber     int64     `json:"pull_number"`
	ChangeKind     string    `json:"change_kind"`
	HeadSHA        string    `json:"head_sha"`
	BaseSHA        string    `json:"base_sha"`
	MergeCommitSHA string    `json:"merge_commit_sha"`
	MergedAt       time.Time `json:"merged_at"`
}

// ProjectMergedPullRequest returns one deterministic, bounded TIMELINE fact only for an
// internally consistent merged pull request. A valid response for an unmerged pull request
// abstains with no facts; pre-merge test-merge SHAs are never promoted to merge evidence.
func ProjectMergedPullRequest(input Projection) ([]fleet.GraphFact, error) {
	if err := validateProjection(input); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSON(input.Response); err != nil {
		return nil, fmt.Errorf("decode GitHub pull request response: %w", err)
	}

	var response pullResponse
	decoder := json.NewDecoder(bytes.NewReader(input.Response))
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode GitHub pull request response: %w", err)
	}
	if err := validateResponseIdentity(response, input.PullNumber); err != nil {
		return nil, err
	}
	if !*response.Merged {
		if response.MergedAt != nil {
			return nil, fmt.Errorf("unmerged GitHub pull request must not have merged_at")
		}
		return []fleet.GraphFact{}, nil
	}

	observation, err := mergedObservation(response, input.ObservedAt)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return nil, fmt.Errorf("encode GitHub pull request change fact: %w", err)
	}
	if len(encoded) > maxFactPayloadBytes {
		return nil, fmt.Errorf("GitHub pull request change fact exceeds %d encoded bytes", maxFactPayloadBytes)
	}

	resourceName := input.Repository + "#" + strconv.FormatInt(input.PullNumber, 10)
	nativeID := input.Owner + "/" + resourceName + "@" + observation.MergeCommitSHA
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind,
					Scope:      input.Host,
					Kind:       "PullRequest",
					Namespace:  input.Owner,
					Name:       resourceName,
				},
				Kind:       fleet.FactChange,
				Observed:   encoded,
				ObservedAt: observation.MergedAt,
				Source:     input.Host,
				Provenance: fleet.Provenance{
					Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: nativeID,
				},
			},
			Workspace: input.Workspace,
		},
		Lens: fleet.LensTimeline,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return nil, fmt.Errorf("validate GitHub pull request change fact: %w", err)
	}
	return []fleet.GraphFact{fact}, nil
}

func validateProjection(input Projection) error {
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
	if input.PullNumber <= 0 {
		return fmt.Errorf("pull number must be positive")
	}
	resourceName := input.Repository + "#" + strconv.FormatInt(input.PullNumber, 10)
	if len(resourceName) > maxResourceName {
		return fmt.Errorf("pull request resource name exceeds %d bytes", maxResourceName)
	}
	if input.ObservedAt.IsZero() {
		return fmt.Errorf("projection observation time is required")
	}
	if len(input.Response) == 0 {
		return fmt.Errorf("GitHub pull request response is required")
	}
	if len(input.Response) > maxResponseBytes {
		return fmt.Errorf("GitHub pull request response exceeds %d bytes", maxResponseBytes)
	}
	if !utf8.Valid(input.Response) {
		return fmt.Errorf("GitHub pull request response must be valid UTF-8")
	}
	return nil
}

func validateResponseIdentity(response pullResponse, pullNumber int64) error {
	if response.Number == nil {
		return fmt.Errorf("GitHub pull request number is required")
	}
	if *response.Number != pullNumber {
		return fmt.Errorf("GitHub pull request number does not match trusted caller identity")
	}
	if response.State == nil {
		return fmt.Errorf("GitHub pull request state is required")
	}
	if *response.State != "open" && *response.State != "closed" {
		return fmt.Errorf("GitHub pull request state must be open or closed")
	}
	if response.Draft == nil {
		return fmt.Errorf("GitHub pull request draft state is required")
	}
	if response.Merged == nil {
		return fmt.Errorf("GitHub pull request merged state is required")
	}
	return nil
}

func mergedObservation(response pullResponse, observedAt time.Time) (changeObservation, error) {
	if *response.State != "closed" {
		return changeObservation{}, fmt.Errorf("merged GitHub pull request must be closed")
	}
	if *response.Draft {
		return changeObservation{}, fmt.Errorf("merged GitHub pull request must not be a draft")
	}
	if response.MergedAt == nil || *response.MergedAt == "" {
		return changeObservation{}, fmt.Errorf("merged GitHub pull request merged_at is required")
	}
	mergedAt, err := time.Parse(time.RFC3339Nano, *response.MergedAt)
	if err != nil || mergedAt.IsZero() {
		return changeObservation{}, fmt.Errorf("merged GitHub pull request merged_at must be a non-zero RFC3339 timestamp")
	}
	mergedAt = mergedAt.UTC()
	if mergedAt.After(observedAt.UTC().Add(maxClockSkew)) {
		return changeObservation{}, fmt.Errorf("GitHub pull request merge time exceeds allowed collection clock skew")
	}

	headSHA, err := requiredCommitSHA("head.sha", response.Head)
	if err != nil {
		return changeObservation{}, err
	}
	baseSHA, err := requiredCommitSHA("base.sha", response.Base)
	if err != nil {
		return changeObservation{}, err
	}
	if response.MergeCommitSHA == nil || !validCommitSHA(*response.MergeCommitSHA) {
		return changeObservation{}, fmt.Errorf("merged GitHub pull request merge_commit_sha is invalid")
	}
	return changeObservation{
		PullNumber:     *response.Number,
		ChangeKind:     "pull-request-merged",
		HeadSHA:        headSHA,
		BaseSHA:        baseSHA,
		MergeCommitSHA: *response.MergeCommitSHA,
		MergedAt:       mergedAt,
	}, nil
}

func requiredCommitSHA(label string, commit *pullCommit) (string, error) {
	if commit == nil || commit.SHA == nil || !validCommitSHA(*commit.SHA) {
		return "", fmt.Errorf("merged GitHub pull request %s is invalid", label)
	}
	return *commit.SHA, nil
}

func validCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validateHost(value string) error {
	if err := validateText("host", value, maxHostBytes); err != nil {
		return err
	}
	if strings.ToLower(value) != value || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return fmt.Errorf("host is invalid")
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("host is invalid")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return fmt.Errorf("host is invalid")
			}
		}
	}
	return nil
}

func validatePathComponent(label, value string, maximum int) error {
	if err := validateText(label, value, maximum); err != nil {
		return err
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%s is invalid", label)
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return fmt.Errorf("%s is invalid", label)
		}
	}
	return nil
}

func validateText(label, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is invalid", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s is invalid", label)
		}
	}
	return nil
}

func rejectDuplicateJSON(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := consumeUniqueJSON(decoder, 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return fmt.Errorf("JSON contains trailing data")
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	if depth >= maxJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", maxJSONDepth)
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok || seen[name] {
				return fmt.Errorf("JSON contains a duplicate or invalid object member")
			}
			seen[name] = true
			if err := consumeUniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("JSON contains an invalid delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || closing != matchingDelimiter(delimiter) {
		return fmt.Errorf("JSON contains an invalid closing delimiter")
	}
	return nil
}

func matchingDelimiter(open json.Delim) json.Delim {
	if open == '{' {
		return '}'
	}
	return ']'
}
