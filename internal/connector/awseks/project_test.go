// SPDX-License-Identifier: Apache-2.0

package awseks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

var testObservedAt = time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)

func TestProjectNodegroupFactsEmitsSanitizedLiveFacts(t *testing.T) {
	t.Parallel()
	secret := "arn:aws:iam::123456789012:role/private-node-role"
	input := validProjection(t)
	input.Response = responseJSON(t, input, map[string]any{
		"status":       "DEGRADED",
		"capacityType": "SPOT",
		"health": map[string]any{"issues": []any{
			map[string]any{"code": "NodeCreationFailure", "message": "credential=" + secret, "resourceIds": []string{"i-secret"}},
			map[string]any{"code": "AccessDenied", "message": "do not retain"},
		}},
		"nodeRole":       secret,
		"resources":      map[string]any{"autoScalingGroups": []any{map[string]any{"name": "secret-asg"}}, "remoteAccessSecurityGroup": "sg-secret"},
		"subnets":        []string{"subnet-secret"},
		"labels":         map[string]any{"password": "secret"},
		"taints":         []any{map[string]any{"key": "secret", "value": "secret", "effect": "NO_SCHEDULE"}},
		"tags":           map[string]any{"token": "secret"},
		"launchTemplate": map[string]any{"id": "lt-secret", "name": "secret", "version": "7"},
		"remoteAccess":   map[string]any{"ec2SshKey": "secret", "sourceSecurityGroups": []string{"sg-secret"}},
		"version":        "1.99-secret",
		"releaseVersion": "ami-secret",
		"updateConfig":   map[string]any{"maxUnavailable": 1},
		"instanceTypes":  []string{"m7i.private"},
		"diskSize":       100,
		"amiType":        "AL2023_x86_64_STANDARD",
	})

	facts, err := ProjectNodegroupFacts(input)
	if err != nil {
		t.Fatalf("ProjectNodegroupFacts() error = %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("ProjectNodegroupFacts() returned %d facts; want 2", len(facts))
	}

	for index, fact := range facts {
		if err := fact.Validate(input.Workspace); err != nil {
			t.Fatalf("fact %d invalid: %v", index, err)
		}
		if fact.Lens != fleet.LensLive || fact.Fact.Ref.SourceKind != Kind || fact.Fact.Ref.Scope != input.Scope ||
			fact.Fact.Ref.Kind != "ManagedNodeGroup" || fact.Fact.Ref.Name != input.NodegroupName ||
			fact.Fact.Source != input.Scope || fact.Fact.ObservedAt != input.ObservedAt ||
			fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion ||
			!strings.HasPrefix(fact.Fact.Provenance.NativeID, "sha256:") {
			t.Fatalf("fact %d has unexpected envelope: %#v", index, fact)
		}
		if fact.Entity == nil || *fact.Entity != (fleet.EntityRef{Cluster: input.Scope}) {
			t.Fatalf("fact %d entity = %#v; want trusted cluster root", index, fact.Entity)
		}
	}
	if facts[0].Fact.Kind != fleet.FactInventory || facts[1].Fact.Kind != fleet.FactHealth {
		t.Fatalf("fact kinds = %q, %q; want inventory, health", facts[0].Fact.Kind, facts[1].Fact.Kind)
	}

	var inventory inventoryObservation
	if err := json.Unmarshal(facts[0].Fact.Observed, &inventory); err != nil {
		t.Fatalf("decode inventory observation: %v", err)
	}
	if inventory != (inventoryObservation{
		NodegroupName: input.NodegroupName, CapacityType: "SPOT", MinSize: 1, DesiredSize: 3, MaxSize: 5,
	}) {
		t.Fatalf("inventory observation = %#v", inventory)
	}
	var health healthObservation
	if err := json.Unmarshal(facts[1].Fact.Observed, &health); err != nil {
		t.Fatalf("decode health observation: %v", err)
	}
	if health.ProviderStatus != "DEGRADED" || !reflect.DeepEqual(health.IssueCodes, []string{"AccessDenied", "NodeCreationFailure"}) {
		t.Fatalf("health observation = %#v", health)
	}

	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("encode facts: %v", err)
	}
	for _, forbidden := range []string{
		secret, input.Account, input.Region, "nodegroupArn", "credential=", "i-secret", "secret-asg",
		"subnet-secret", "sg-secret", "lt-secret", "password", "ami-secret", "m7i.private",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("facts retain forbidden provider data %q: %s", forbidden, encoded)
		}
	}
	if bytes.Contains(facts[1].Fact.Observed, []byte(`"health":"Healthy"`)) {
		t.Fatalf("provider status must not become a Kubernetes health claim: %s", facts[1].Fact.Observed)
	}
}

func TestProjectNodegroupFactsIsDeterministicAcrossDiscardedDataAndIssueOrder(t *testing.T) {
	t.Parallel()
	first := validProjection(t)
	first.Response = responseJSON(t, first, map[string]any{
		"health": map[string]any{"issues": []any{
			map[string]any{"code": "PodEvictionFailure", "message": "first"},
			map[string]any{"code": "AccessDenied", "resourceIds": []string{"i-first"}},
		}},
		"tags": map[string]any{"first": "secret"},
	})
	second := first
	second.Response = responseJSON(t, second, map[string]any{
		"health": map[string]any{"issues": []any{
			map[string]any{"code": "AccessDenied", "message": "changed"},
			map[string]any{"code": "PodEvictionFailure", "resourceIds": []string{"i-second"}},
		}},
		"tags": map[string]any{"second": "different"},
	})

	firstFacts, err := ProjectNodegroupFacts(first)
	if err != nil {
		t.Fatalf("first projection: %v", err)
	}
	secondFacts, err := ProjectNodegroupFacts(second)
	if err != nil {
		t.Fatalf("second projection: %v", err)
	}
	if !reflect.DeepEqual(firstFacts, secondFacts) {
		t.Fatalf("discarded provider data changed facts:\nfirst=%#v\nsecond=%#v", firstFacts, secondFacts)
	}
}

func TestProjectNodegroupFactsSupportsReviewedPartitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		partition string
		region    string
	}{
		{name: "commercial", partition: "aws", region: "us-west-2"},
		{name: "govcloud", partition: "aws-us-gov", region: "us-gov-west-1"},
		{name: "china", partition: "aws-cn", region: "cn-north-1"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validProjection(t)
			input.Partition = test.partition
			input.Region = test.region
			input.Response = responseJSON(t, input, nil)
			if _, err := ProjectNodegroupFacts(input); err != nil {
				t.Fatalf("ProjectNodegroupFacts() error = %v", err)
			}
		})
	}
}

func TestProjectNodegroupFactsRejectsInvalidTrustedProjection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{name: "empty workspace", mutate: func(input *Projection) { input.Workspace = "" }},
		{name: "scope separator", mutate: func(input *Projection) { input.Scope = "cluster/other" }},
		{name: "partition", mutate: func(input *Projection) { input.Partition = "aws-iso" }},
		{name: "account", mutate: func(input *Projection) { input.Account = "1234-secret" }},
		{name: "commercial china region", mutate: func(input *Projection) { input.Region = "cn-north-1" }},
		{name: "gov commercial region", mutate: func(input *Projection) { input.Partition = "aws-us-gov" }},
		{name: "malformed region", mutate: func(input *Projection) { input.Region = "US WEST 2" }},
		{name: "cluster starts separator", mutate: func(input *Projection) { input.ClusterName = "-cluster" }},
		{name: "nodegroup slash", mutate: func(input *Projection) { input.NodegroupName = "ng/other" }},
		{name: "zero observation", mutate: func(input *Projection) { input.ObservedAt = time.Time{} }},
		{name: "pre-contract observation", mutate: func(input *Projection) { input.ObservedAt = time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{name: "empty response", mutate: func(input *Projection) { input.Response = nil }},
		{name: "invalid utf8", mutate: func(input *Projection) { input.Response = []byte{0xff} }},
		{name: "oversized response", mutate: func(input *Projection) { input.Response = bytes.Repeat([]byte(" "), maxResponseBytes+1) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validProjection(t)
			test.mutate(&input)
			if _, err := ProjectNodegroupFacts(input); err == nil {
				t.Fatalf("ProjectNodegroupFacts() succeeded")
			}
		})
	}
}

func TestProjectNodegroupFactsRejectsMismatchedOrUnsafeResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing nodegroup", mutate: func(response map[string]any) { delete(response, "nodegroup") }},
		{name: "null nodegroup", mutate: func(response map[string]any) { response["nodegroup"] = nil }},
		{name: "cluster mismatch", mutate: mutateNodegroup("clusterName", "other")},
		{name: "name mismatch", mutate: mutateNodegroup("nodegroupName", "other")},
		{name: "partition ARN mismatch", mutate: mutateNodegroup("nodegroupArn", "arn:aws-cn:eks:us-west-2:123456789012:nodegroup/prod/workers/a8c75f2f-df78-a72f-4063-4b69af3de5b1")},
		{name: "ARN UUID invalid", mutate: mutateNodegroup("nodegroupArn", "arn:aws:eks:us-west-2:123456789012:nodegroup/prod/workers/not-a-uuid")},
		{name: "unknown status", mutate: mutateNodegroup("status", "READY")},
		{name: "null status", mutate: mutateNodegroup("status", nil)},
		{name: "unknown capacity", mutate: mutateNodegroup("capacityType", "FREE")},
		{name: "missing scaling", mutate: func(response map[string]any) { delete(response["nodegroup"].(map[string]any), "scalingConfig") }},
		{name: "negative minimum", mutate: mutateScaling("minSize", -1)},
		{name: "zero maximum", mutate: mutateScaling("maxSize", 0)},
		{name: "minimum above desired", mutate: mutateScaling("minSize", 4)},
		{name: "desired above maximum", mutate: mutateScaling("desiredSize", 6)},
		{name: "oversized maximum", mutate: mutateScaling("maxSize", maxScalingSize+1)},
		{name: "fractional scaling", mutate: mutateScaling("desiredSize", 1.5)},
		{name: "missing health", mutate: func(response map[string]any) { delete(response["nodegroup"].(map[string]any), "health") }},
		{name: "missing issues", mutate: mutateNodegroup("health", map[string]any{})},
		{name: "null issues", mutate: mutateNodegroup("health", map[string]any{"issues": nil})},
		{name: "unknown issue", mutate: mutateNodegroup("health", map[string]any{"issues": []any{map[string]any{"code": "NewUnreviewedCode"}}})},
		{name: "null issue", mutate: mutateNodegroup("health", map[string]any{"issues": []any{map[string]any{"code": nil}}})},
		{name: "duplicate issue", mutate: mutateNodegroup("health", map[string]any{"issues": []any{map[string]any{"code": "AccessDenied"}, map[string]any{"code": "AccessDenied"}}})},
		{name: "too many issues", mutate: mutateNodegroup("health", healthWithIssueCount(maxHealthIssues+1))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validProjection(t)
			response := responseMap(input)
			test.mutate(response)
			input.Response = marshalJSON(t, response)
			if _, err := ProjectNodegroupFacts(input); err == nil {
				t.Fatalf("ProjectNodegroupFacts() succeeded")
			}
		})
	}
}

func TestProjectNodegroupFactsRejectsJSONAttacksWithoutEchoingValues(t *testing.T) {
	t.Parallel()
	input := validProjection(t)
	secret := "do-not-echo-credential"
	tests := []struct {
		name     string
		response []byte
	}{
		{name: "duplicate", response: []byte(`{"nodegroup":{"status":"ACTIVE","status":"` + secret + `"}}`)},
		{name: "trailing", response: append(input.Response, []byte(` {"token":"`+secret+`"}`)...)},
		{name: "malformed", response: []byte(`{"nodegroup":"` + secret)},
		{name: "deep", response: []byte(strings.Repeat("[", maxJSONDepth+1) + `"` + secret + `"` + strings.Repeat("]", maxJSONDepth+1))},
		{name: "untrusted enum", response: responseJSON(t, input, map[string]any{"status": secret})},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := input
			candidate.Response = test.response
			_, err := ProjectNodegroupFacts(candidate)
			if err == nil {
				t.Fatalf("ProjectNodegroupFacts() succeeded")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error echoed attacker value: %v", err)
			}
		})
	}
}

func TestClosedAWSNodegroupTaxonomies(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"CREATING", "ACTIVE", "UPDATING", "DELETING", "CREATE_FAILED", "DELETE_FAILED", "DEGRADED"} {
		if !validNodegroupStatus(status) {
			t.Errorf("reviewed status %q rejected", status)
		}
	}
	for _, capacity := range []string{"ON_DEMAND", "SPOT", "CAPACITY_BLOCK"} {
		if !validCapacityType(capacity) {
			t.Errorf("reviewed capacity type %q rejected", capacity)
		}
	}
	for _, code := range []string{"AutoScalingGroupNotFound", "Ec2InstanceTypeDoesNotExist", "Unknown"} {
		if !validHealthIssueCode(code) {
			t.Errorf("reviewed issue code %q rejected", code)
		}
	}
	for _, value := range []string{"", "HEALTHY", "unknown", "AccessDenied\nsecret"} {
		if validNodegroupStatus(value) || validCapacityType(value) || validHealthIssueCode(value) {
			t.Errorf("unreviewed taxonomy value %q accepted", value)
		}
	}
}

func TestAWSIdentityValidatorsRejectAmbiguousForms(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		account string
		want    bool
	}{
		{account: "123456789012", want: true},
		{account: "123"},
		{account: "12345678901x"},
	} {
		if got := validAccount(test.account); got != test.want {
			t.Errorf("validAccount(%q) = %t; want %t", test.account, got, test.want)
		}
	}
	for _, test := range []struct {
		partition string
		region    string
		want      bool
	}{
		{partition: "aws", region: "us-west-2", want: true},
		{partition: "aws-cn", region: "cn-north-1", want: true},
		{partition: "aws-us-gov", region: "us-gov-west-1", want: true},
		{partition: "aws", region: "short"},
		{partition: "aws", region: strings.Repeat("a", maxRegionBytes+1)},
		{partition: "aws", region: "-us-west-2"},
		{partition: "aws", region: "us-west-2-"},
		{partition: "aws", region: "us_West-2"},
		{partition: "aws", region: "us-2"},
		{partition: "aws", region: "us-west-final"},
		{partition: "aws", region: "us--west-2"},
		{partition: "aws", region: "cn-north-1"},
		{partition: "aws-cn", region: "us-west-2"},
		{partition: "aws-us-gov", region: "us-west-2"},
		{partition: "unreviewed", region: "us-west-2"},
	} {
		if got := validRegion(test.partition, test.region); got != test.want {
			t.Errorf("validRegion(%q, %q) = %t; want %t", test.partition, test.region, got, test.want)
		}
	}
	for _, test := range []struct {
		value string
		max   int
		want  bool
	}{
		{value: "A_0-name", max: 100, want: true},
		{value: "", max: 100},
		{value: "abcd", max: 3},
		{value: "-leading", max: 100},
		{value: "bad/name", max: 100},
		{value: "é", max: 100},
	} {
		if got := validEKSName(test.value, test.max); got != test.want {
			t.Errorf("validEKSName(%q) = %t; want %t", test.value, got, test.want)
		}
	}
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "A8C75F2F-DF78-A72F-4063-4B69AF3DE5B1", want: true},
		{value: "short"},
		{value: "a8c75f2f_df78-a72f-4063-4b69af3de5b1"},
		{value: "g8c75f2f-df78-a72f-4063-4b69af3de5b1"},
	} {
		if got := validUUID(test.value); got != test.want {
			t.Errorf("validUUID(%q) = %t; want %t", test.value, got, test.want)
		}
	}
}

func TestInternalFactAndTextGuardsFailClosed(t *testing.T) {
	t.Parallel()
	input := validProjection(t)
	arn := responseMap(input)["nodegroup"].(map[string]any)["nodegroupArn"].(string)
	for _, test := range []struct {
		name        string
		input       Projection
		kind        fleet.FactKind
		observation any
	}{
		{name: "unencodable observation", input: input, kind: fleet.FactHealth, observation: make(chan int)},
		{name: "oversized observation", input: input, kind: fleet.FactHealth, observation: strings.Repeat("x", maxFactPayloadBytes+1)},
		{name: "invalid graph scope", input: func() Projection { candidate := input; candidate.Scope = "bad/scope"; return candidate }(), kind: fleet.FactHealth, observation: healthObservation{}},
		{name: "invalid fact kind", input: input, kind: fleet.FactKind("unreviewed"), observation: healthObservation{}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := buildFact(test.input, arn, test.kind, test.observation); err == nil {
				t.Fatalf("buildFact() succeeded")
			}
		})
	}
	for _, test := range []struct {
		name  string
		value string
		max   int
		want  bool
	}{
		{name: "valid", value: "safe", max: 4, want: true},
		{name: "empty", value: "", max: 4},
		{name: "too long", value: "longer", max: 4},
		{name: "invalid utf8", value: string([]byte{0xff}), max: 4},
		{name: "whitespace", value: " safe", max: 8},
		{name: "control", value: "bad\n", max: 8},
	} {
		err := validateText(test.name, test.value, test.max)
		if (err == nil) != test.want {
			t.Errorf("validateText(%q) error = %v; want valid=%t", test.value, err, test.want)
		}
	}

	candidate := input
	candidate.Response = responseJSON(t, candidate, map[string]any{"nodegroupArn": ""})
	if _, err := ProjectNodegroupFacts(candidate); err == nil {
		t.Fatalf("ProjectNodegroupFacts() accepted empty ARN")
	}
}

func FuzzProjectNodegroupFacts(f *testing.F) {
	seed := validProjection(f)
	f.Add(seed.Response)
	f.Add([]byte(`{"nodegroup":null}`))
	f.Add([]byte(`{"nodegroup":{"status":"ACTIVE","status":"DEGRADED"}}`))
	f.Fuzz(func(t *testing.T, response []byte) {
		input := seed
		input.Response = response
		facts, err := ProjectNodegroupFacts(input)
		if err != nil {
			return
		}
		if len(facts) != 2 {
			t.Fatalf("successful projection returned %d facts", len(facts))
		}
		for _, fact := range facts {
			if err := fact.Validate(input.Workspace); err != nil {
				t.Fatalf("successful projection emitted invalid fact: %v", err)
			}
			if len(fact.Fact.Observed) > maxFactPayloadBytes {
				t.Fatalf("successful projection emitted oversized fact")
			}
		}
	})
}

type testingHelper interface {
	Helper()
	Fatalf(string, ...any)
}

func validProjection(t testingHelper) Projection {
	t.Helper()
	input := Projection{
		Workspace:     "workspace-a",
		Scope:         "prod-cluster",
		Partition:     "aws",
		Account:       "123456789012",
		Region:        "us-west-2",
		ClusterName:   "prod",
		NodegroupName: "workers",
		ObservedAt:    testObservedAt,
	}
	input.Response = responseJSON(t, input, nil)
	return input
}

func responseMap(input Projection) map[string]any {
	arn := fmt.Sprintf("arn:%s:eks:%s:%s:nodegroup/%s/%s/a8c75f2f-df78-a72f-4063-4b69af3de5b1",
		input.Partition, input.Region, input.Account, input.ClusterName, input.NodegroupName)
	return map[string]any{"nodegroup": map[string]any{
		"nodegroupArn":  arn,
		"clusterName":   input.ClusterName,
		"nodegroupName": input.NodegroupName,
		"status":        "ACTIVE",
		"capacityType":  "ON_DEMAND",
		"scalingConfig": map[string]any{"minSize": 1, "desiredSize": 3, "maxSize": 5},
		"health":        map[string]any{"issues": []any{}},
	}}
}

func responseJSON(t testingHelper, input Projection, overrides map[string]any) []byte {
	t.Helper()
	response := responseMap(input)
	nodegroup := response["nodegroup"].(map[string]any)
	for name, value := range overrides {
		nodegroup[name] = value
	}
	return marshalJSON(t, response)
}

func marshalJSON(t testingHelper, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return encoded
}

func mutateNodegroup(name string, value any) func(map[string]any) {
	return func(response map[string]any) {
		response["nodegroup"].(map[string]any)[name] = value
	}
}

func mutateScaling(name string, value any) func(map[string]any) {
	return func(response map[string]any) {
		response["nodegroup"].(map[string]any)["scalingConfig"].(map[string]any)[name] = value
	}
}

func healthWithIssueCount(count int) map[string]any {
	issues := make([]any, count)
	for index := range issues {
		issues[index] = map[string]any{"code": "Unknown"}
	}
	return map[string]any{"issues": issues}
}
