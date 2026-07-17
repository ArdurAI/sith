// SPDX-License-Identifier: Apache-2.0

// Package awseks normalizes bounded Amazon EKS managed-nodegroup evidence for
// Sith's operational graph.
package awseks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// Kind is the stable registry identifier for Amazon EKS read evidence.
	Kind = "aws-eks"
	// ProtocolVersion identifies the normalized DescribeNodegroup fact contract.
	ProtocolVersion = "describe-nodegroup/v1"

	maxResponseBytes    = 512 << 10
	maxFactPayloadBytes = 4 << 10
	maxJSONDepth        = 64
	maxHealthIssues     = 64
	maxScalingSize      = 100_000
	maxWorkspaceBytes   = 253
	maxScopeBytes       = 253
	maxClusterNameBytes = 100
	maxNodegroupBytes   = 63
	maxARNBytes         = 512
)

// Projection supplies one already-authorized EKS DescribeNodegroup response. The trusted caller
// owns regional enumeration, pagination, authorization, request identity, and collection time.
// ProjectNodegroupFacts performs no discovery, network access, credential loading, persistence,
// process execution, or mutation.
type Projection struct {
	Workspace     string
	Scope         string
	Partition     string
	Account       string
	Region        string
	ClusterName   string
	NodegroupName string
	ObservedAt    time.Time
	Response      []byte
}

type describeNodegroupResponse struct {
	Nodegroup *nodegroup `json:"nodegroup"`
}

type nodegroup struct {
	ARN          *string          `json:"nodegroupArn"`
	ClusterName  *string          `json:"clusterName"`
	Name         *string          `json:"nodegroupName"`
	Status       *string          `json:"status"`
	CapacityType *string          `json:"capacityType"`
	Scaling      *scalingConfig   `json:"scalingConfig"`
	Health       *nodegroupHealth `json:"health"`
}

type scalingConfig struct {
	MinSize     *int64 `json:"minSize"`
	MaxSize     *int64 `json:"maxSize"`
	DesiredSize *int64 `json:"desiredSize"`
}

type nodegroupHealth struct {
	Issues *[]nodegroupIssue `json:"issues"`
}

type nodegroupIssue struct {
	Code *string `json:"code"`
}

type inventoryObservation struct {
	NodegroupName string `json:"nodegroup_name"`
	CapacityType  string `json:"capacity_type"`
	MinSize       int64  `json:"min_size"`
	DesiredSize   int64  `json:"desired_size"`
	MaxSize       int64  `json:"max_size"`
}

type healthObservation struct {
	NodegroupName  string   `json:"nodegroup_name"`
	ProviderStatus string   `json:"provider_status"`
	IssueCodes     []string `json:"issue_codes"`
}

// ProjectNodegroupFacts returns one inventory fact and one provider-health fact for a managed
// nodegroup. The health fact reports only EKS status and closed issue codes; it never claims that
// Kubernetes nodes or workloads are healthy.
func ProjectNodegroupFacts(input Projection) ([]fleet.GraphFact, error) {
	if err := validateProjection(input); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSON(input.Response); err != nil {
		return nil, fmt.Errorf("decode EKS DescribeNodegroup response")
	}

	var response describeNodegroupResponse
	if err := json.Unmarshal(input.Response, &response); err != nil {
		return nil, fmt.Errorf("decode EKS DescribeNodegroup response")
	}
	issueCodes, err := validateNodegroup(input, response.Nodegroup)
	if err != nil {
		return nil, err
	}
	nodegroup := response.Nodegroup

	inventory := inventoryObservation{
		NodegroupName: input.NodegroupName,
		CapacityType:  *nodegroup.CapacityType,
		MinSize:       *nodegroup.Scaling.MinSize,
		DesiredSize:   *nodegroup.Scaling.DesiredSize,
		MaxSize:       *nodegroup.Scaling.MaxSize,
	}
	health := healthObservation{
		NodegroupName:  input.NodegroupName,
		ProviderStatus: *nodegroup.Status,
		IssueCodes:     issueCodes,
	}

	inventoryFact, err := buildFact(input, *nodegroup.ARN, fleet.FactInventory, inventory)
	if err != nil {
		return nil, err
	}
	healthFact, err := buildFact(input, *nodegroup.ARN, fleet.FactHealth, health)
	if err != nil {
		return nil, err
	}
	return []fleet.GraphFact{inventoryFact, healthFact}, nil
}

func validateProjection(input Projection) error {
	if err := validateText("workspace", input.Workspace, maxWorkspaceBytes); err != nil {
		return err
	}
	if err := validateText("scope", input.Scope, maxScopeBytes); err != nil || strings.Contains(input.Scope, "/") {
		return fmt.Errorf("scope is invalid")
	}
	if !validPartition(input.Partition) {
		return fmt.Errorf("AWS partition is invalid")
	}
	if !validAccount(input.Account) {
		return fmt.Errorf("AWS account is invalid")
	}
	if !validRegion(input.Partition, input.Region) {
		return fmt.Errorf("AWS region is invalid for partition")
	}
	if !validEKSName(input.ClusterName, maxClusterNameBytes) {
		return fmt.Errorf("EKS cluster name is invalid")
	}
	if !validEKSName(input.NodegroupName, maxNodegroupBytes) {
		return fmt.Errorf("EKS nodegroup name is invalid")
	}
	if input.ObservedAt.IsZero() || input.ObservedAt.Year() < 2000 || input.ObservedAt.Year() > 9999 {
		return fmt.Errorf("observation time is invalid")
	}
	if len(input.Response) == 0 {
		return fmt.Errorf("EKS DescribeNodegroup response is required")
	}
	if len(input.Response) > maxResponseBytes {
		return fmt.Errorf("EKS DescribeNodegroup response exceeds %d bytes", maxResponseBytes)
	}
	if !utf8.Valid(input.Response) {
		return fmt.Errorf("EKS DescribeNodegroup response must be valid UTF-8")
	}
	return nil
}

func validateNodegroup(input Projection, value *nodegroup) ([]string, error) {
	if value == nil || value.ARN == nil || value.ClusterName == nil || value.Name == nil ||
		value.Status == nil || value.CapacityType == nil || value.Scaling == nil || value.Health == nil {
		return nil, fmt.Errorf("EKS nodegroup identity, status, capacity, scaling, and health are required")
	}
	if *value.ClusterName != input.ClusterName || *value.Name != input.NodegroupName {
		return nil, fmt.Errorf("EKS nodegroup identity does not match trusted caller identity")
	}
	if err := validateNodegroupARN(input, *value.ARN); err != nil {
		return nil, err
	}
	if !validNodegroupStatus(*value.Status) {
		return nil, fmt.Errorf("EKS nodegroup status is invalid")
	}
	if !validCapacityType(*value.CapacityType) {
		return nil, fmt.Errorf("EKS nodegroup capacity type is invalid")
	}
	if value.Scaling.MinSize == nil || value.Scaling.MaxSize == nil || value.Scaling.DesiredSize == nil {
		return nil, fmt.Errorf("EKS nodegroup scaling values are required")
	}
	minimum := *value.Scaling.MinSize
	maximum := *value.Scaling.MaxSize
	desired := *value.Scaling.DesiredSize
	if minimum < 0 || maximum < 1 || desired < 0 || minimum > maxScalingSize ||
		maximum > maxScalingSize || desired > maxScalingSize || minimum > desired || desired > maximum {
		return nil, fmt.Errorf("EKS nodegroup scaling values are invalid")
	}
	if value.Health.Issues == nil {
		return nil, fmt.Errorf("EKS nodegroup health issues are required")
	}
	issues := *value.Health.Issues
	if len(issues) > maxHealthIssues {
		return nil, fmt.Errorf("EKS nodegroup health issue count exceeds %d", maxHealthIssues)
	}
	codes := make([]string, 0, len(issues))
	seen := make(map[string]bool, len(issues))
	for _, issue := range issues {
		if issue.Code == nil || !validHealthIssueCode(*issue.Code) {
			return nil, fmt.Errorf("EKS nodegroup health issue code is invalid")
		}
		if seen[*issue.Code] {
			return nil, fmt.Errorf("EKS nodegroup health issue codes must be unique")
		}
		seen[*issue.Code] = true
		codes = append(codes, *issue.Code)
	}
	sort.Strings(codes)
	return codes, nil
}

func validateNodegroupARN(input Projection, arn string) error {
	if err := validateText("EKS nodegroup ARN", arn, maxARNBytes); err != nil {
		return err
	}
	prefix := "arn:" + input.Partition + ":eks:" + input.Region + ":" + input.Account +
		":nodegroup/" + input.ClusterName + "/" + input.NodegroupName + "/"
	if !strings.HasPrefix(arn, prefix) || !validUUID(strings.TrimPrefix(arn, prefix)) {
		return fmt.Errorf("EKS nodegroup ARN does not match trusted caller identity")
	}
	return nil
}

func validPartition(value string) bool {
	switch value {
	case "aws", "aws-us-gov", "aws-cn":
		return true
	default:
		return false
	}
}

func validAccount(value string) bool {
	if len(value) != 12 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validRegion(partition, value string) bool {
	switch partition {
	case "aws":
		switch value {
		case "us-east-1", "us-east-2", "us-west-1", "us-west-2",
			"af-south-1",
			"ap-east-1", "ap-east-2", "ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
			"ap-south-1", "ap-south-2", "ap-southeast-1", "ap-southeast-2", "ap-southeast-3",
			"ap-southeast-4", "ap-southeast-5", "ap-southeast-6", "ap-southeast-7",
			"ca-central-1", "ca-west-1",
			"eu-central-1", "eu-central-2", "eu-north-1", "eu-south-1", "eu-south-2",
			"eu-west-1", "eu-west-2", "eu-west-3",
			"il-central-1", "me-central-1", "me-south-1", "mx-central-1", "sa-east-1":
			return true
		default:
			return false
		}
	case "aws-us-gov":
		return value == "us-gov-east-1" || value == "us-gov-west-1"
	case "aws-cn":
		return value == "cn-north-1" || value == "cn-northwest-1"
	default:
		return false
	}
}

func validEKSName(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') && (character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func validNodegroupStatus(value string) bool {
	switch value {
	case "CREATING", "ACTIVE", "UPDATING", "DELETING", "CREATE_FAILED", "DELETE_FAILED", "DEGRADED":
		return true
	default:
		return false
	}
}

func validCapacityType(value string) bool {
	switch value {
	case "ON_DEMAND", "SPOT", "CAPACITY_BLOCK":
		return true
	default:
		return false
	}
}

func validHealthIssueCode(value string) bool {
	switch value {
	case "AutoScalingGroupNotFound", "AutoScalingGroupInvalidConfiguration",
		"Ec2SecurityGroupNotFound", "Ec2SecurityGroupDeletionFailure",
		"Ec2LaunchTemplateNotFound", "Ec2LaunchTemplateVersionMismatch",
		"Ec2SubnetNotFound", "Ec2SubnetInvalidConfiguration", "IamInstanceProfileNotFound",
		"Ec2SubnetMissingIpv6Assignment", "IamLimitExceeded", "IamNodeRoleNotFound",
		"NodeCreationFailure", "AsgInstanceLaunchFailures", "InstanceLimitExceeded",
		"InsufficientFreeAddresses", "AccessDenied", "InternalFailure", "ClusterUnreachable",
		"AmiIdNotFound", "AutoScalingGroupOptInRequired", "AutoScalingGroupRateLimitExceeded",
		"Ec2LaunchTemplateDeletionFailure", "Ec2LaunchTemplateInvalidConfiguration",
		"Ec2LaunchTemplateMaxLimitExceeded", "Ec2SubnetListTooLong", "IamThrottling",
		"NodeTerminationFailure", "PodEvictionFailure", "SourceEc2LaunchTemplateNotFound",
		"LimitExceeded", "Unknown", "AutoScalingGroupInstanceRefreshActive",
		"KubernetesLabelInvalid", "Ec2LaunchTemplateVersionMaxLimitExceeded",
		"Ec2InstanceTypeDoesNotExist":
		return true
	default:
		return false
	}
}

func buildFact(input Projection, arn string, kind fleet.FactKind, observation any) (fleet.GraphFact, error) {
	encoded, err := json.Marshal(observation)
	if err != nil {
		return fleet.GraphFact{}, fmt.Errorf("encode EKS nodegroup fact")
	}
	if len(encoded) > maxFactPayloadBytes {
		return fleet.GraphFact{}, fmt.Errorf("EKS nodegroup fact exceeds %d encoded bytes", maxFactPayloadBytes)
	}
	digest := sha256.Sum256([]byte(arn + "#" + string(kind)))
	nativeID := "sha256:" + hex.EncodeToString(digest[:])
	entity := fleet.EntityRef{Cluster: input.Scope}
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind,
					Scope:      input.Scope,
					Kind:       "ManagedNodeGroup",
					Name:       input.NodegroupName,
				},
				Kind:       kind,
				Observed:   encoded,
				ObservedAt: input.ObservedAt.UTC(),
				Source:     input.Scope,
				Provenance: fleet.Provenance{Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: nativeID},
			},
			Workspace: input.Workspace,
		},
		Lens:   fleet.LensLive,
		Entity: &entity,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return fleet.GraphFact{}, fmt.Errorf("validate EKS nodegroup fact: %w", err)
	}
	return fact, nil
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
