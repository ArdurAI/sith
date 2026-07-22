// SPDX-License-Identifier: Apache-2.0

package remediation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/brain"
	"github.com/ArdurAI/sith/internal/connector"
	githubconnector "github.com/ArdurAI/sith/internal/connector/github"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const testWorkspace tenancy.WorkspaceID = "workspace-a"

const (
	testBaseSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testBlobSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var testNow = time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)

func TestGitOpsResolverProducesDeterministicR2AndR4Arguments(t *testing.T) {
	t.Parallel()
	for _, rule := range []brain.RuleID{brain.RuleOOMKilled, brain.RuleConfigDrift} {
		rule := rule
		t.Run(string(rule), func(t *testing.T) {
			t.Parallel()
			fixture := newGitOpsFixture(t, rule)
			first, err := fixture.resolver.Resolve(context.Background(), testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			assertReadyResolution(t, first)

			secondInput := validGitOpsInput(fixture.bundle.handler)
			slices.Reverse(secondInput.EvidenceRefs)
			secondBundle, err := NewGitOpsProvenanceBundle(secondInput)
			if err != nil {
				t.Fatalf("construct reordered bundle: %v", err)
			}
			second, err := fixture.resolver.Resolve(context.Background(), testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{secondBundle})
			if err != nil {
				t.Fatalf("second Resolve() error = %v", err)
			}
			if !slices.Equal(first.Arguments, second.Arguments) || first.ArgumentsDigest != second.ArgumentsDigest ||
				!slices.EqualFunc(first.EvidenceRefs, second.EvidenceRefs, sameResourceRef) {
				t.Fatalf("resolution changed with evidence ordering:\nfirst=%#v\nsecond=%#v", first, second)
			}
		})
	}
}

func TestGitOpsProvenanceAndResolutionAreMutationIsolated(t *testing.T) {
	t.Parallel()
	planner := newGitHubPlanner(t)
	contract, err := HandlerContractFor(planner)
	if err != nil {
		t.Fatal(err)
	}
	input := validGitOpsInput(contract)
	bundle, err := NewGitOpsProvenanceBundle(input)
	if err != nil {
		t.Fatal(err)
	}

	input.Subject.Name = "mutated-subject"
	input.Sources[0].NativeID = "github.com/other/repository"
	input.EvidenceRefs[0].Name = "mutated-evidence"
	resolver, err := NewGitOpsResolver(planner, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	verdict := validGitOpsVerdict(brain.RuleConfigDrift)
	first, err := resolver.Resolve(context.Background(), testWorkspace, verdict, []GitOpsProvenanceBundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	assertReadyResolution(t, first)

	first.Arguments[0] = '!'
	first.Target.Name = "mutated-target"
	first.Target.Attributes = map[string]string{"token": "forbidden"}
	first.EvidenceRefs[0].Name = "mutated-output"
	first.Reasons = append(first.Reasons, ReasonCandidateInvalid)

	second, err := resolver.Resolve(context.Background(), testWorkspace, verdict, []GitOpsProvenanceBundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	assertReadyResolution(t, second)
	if len(second.Arguments) == 0 || second.Arguments[0] != '{' || second.Target.Name != "sith" ||
		len(second.Target.Attributes) != 0 || second.EvidenceRefs[0].Name == "mutated-output" || len(second.Reasons) != 0 {
		t.Fatalf("second resolution retained caller mutation: %#v", second)
	}
}

func TestGitOpsResolverAbstainsClosed(t *testing.T) {
	fixture := newGitOpsFixture(t, brain.RuleConfigDrift)
	tests := []struct {
		name   string
		want   AbstentionReason
		mutate func(*brain.Verdict, *[]GitOpsProvenanceBundle)
	}{
		{"candidate missing", ReasonCandidateMissing, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) { verdict.RemediationCandidate = nil }},
		{"candidate mutated", ReasonCandidateInvalid, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) {
			verdict.RemediationCandidate.RequiredProvenance[0] = brain.ProvenanceArgoRevision
		}},
		{"verdict ref attributes", ReasonVerdictInvalid, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) {
			verdict.Ref.Attributes = map[string]string{"native": "untrusted"}
		}},
		{"stale citation", ReasonVerdictInvalid, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) { verdict.Citations[0].Stale = true }},
		{"unattached citation", ReasonVerdictInvalid, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) { verdict.Citations[0].Ref.Name = "other" }},
		{"unsupported R1", ReasonCandidateUnsupported, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) {
			verdict.Rule = brain.RuleBadDeploy
			verdict.RemediationCandidate = &brain.RemediationCandidate{Verb: intent.VerbArgoCDRollback, RequiredProvenance: []brain.ProvenanceRequirement{
				brain.ProvenanceArgoApplicationTarget, brain.ProvenanceArgoRevision,
			}}
		}},
		{"fleet verdict", ReasonFleetAmbiguous, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) { verdict.FleetWide = true }},
		{"unconfirmed verdict", ReasonVerdictUnconfirmed, func(verdict *brain.Verdict, _ *[]GitOpsProvenanceBundle) { verdict.Status = brain.StatusUnconfirmed }},
		{"missing provenance", ReasonProvenanceMissing, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { *bundles = nil }},
		{"ambiguous provenance", ReasonProvenanceAmbiguous, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { *bundles = append(*bundles, (*bundles)[0]) }},
		{"invalid provenance", ReasonProvenanceInvalid, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { (*bundles)[0].version = "forged/v9" }},
		{"future provenance", ReasonProvenanceFuture, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) {
			(*bundles)[0].observedAt = testNow.Add(time.Second)
			(*bundles)[0].validUntil = testNow.Add(2 * time.Minute)
		}},
		{"stale provenance", ReasonProvenanceStale, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { (*bundles)[0].validUntil = testNow }},
		{"foreign workspace", ReasonWorkspaceMismatch, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { (*bundles)[0].workspace = "workspace-b" }},
		{"unattached subject", ReasonSubjectMismatch, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { (*bundles)[0].subject.Name = "other" }},
		{"source contract forged", ReasonProvenanceInvalid, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) {
			(*bundles)[0].source.AdapterVersion = "forged/v1"
		}},
		{"handler adapter drift", ReasonHandlerContractDrift, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) {
			(*bundles)[0].handler.AdapterVersion = "gitops-open-pr/2099-01-01"
		}},
		{"handler schema drift", ReasonHandlerContractDrift, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) {
			(*bundles)[0].handler.SchemaDigest = "sha256:" + strings.Repeat("c", 64)
		}},
		{"unsafe path rejected by handler", ReasonHandlerRejected, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { (*bundles)[0].filePath = "../secret.yaml" }},
		{"configured base mismatch rejected by handler", ReasonHandlerRejected, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) { (*bundles)[0].baseRef = "main" }},
		{"oversized content rejected by handler", ReasonHandlerRejected, func(_ *brain.Verdict, bundles *[]GitOpsProvenanceBundle) {
			(*bundles)[0].desiredContent = strings.Repeat("x", (16<<10)+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict := cloneVerdict(fixture.verdict)
			bundles := []GitOpsProvenanceBundle{cloneBundle(fixture.bundle)}
			test.mutate(&verdict, &bundles)
			got, err := fixture.resolver.Resolve(context.Background(), testWorkspace, verdict, bundles)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			assertAbstention(t, got, test.want)
		})
	}
}

func TestNewGitOpsProvenanceBundleRejectsInvalidClaims(t *testing.T) {
	planner := newGitHubPlanner(t)
	contract, err := HandlerContractFor(planner)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*GitOpsProvenanceInput)
	}{
		{"no source", func(input *GitOpsProvenanceInput) { input.Sources = nil }},
		{"multiple sources", func(input *GitOpsProvenanceInput) { input.Sources = append(input.Sources, input.Sources[0]) }},
		{"invalid workspace", func(input *GitOpsProvenanceInput) { input.Workspace = " workspace-a" }},
		{"subject attributes", func(input *GitOpsProvenanceInput) { input.Subject.Attributes = map[string]string{"uid": "private"} }},
		{"source kind", func(input *GitOpsProvenanceInput) { input.Sources[0].Kind = "gitlab" }},
		{"source adapter", func(input *GitOpsProvenanceInput) { input.Sources[0].AdapterVersion = "future/v2" }},
		{"source repository mismatch", func(input *GitOpsProvenanceInput) { input.Sources[0].NativeID = "github.com/ArdurAI/other" }},
		{"invalid repository", func(input *GitOpsProvenanceInput) { input.Repository.Repository = "sith.git" }},
		{"zero observation", func(input *GitOpsProvenanceInput) { input.ObservedAt = time.Time{} }},
		{"reversed validity", func(input *GitOpsProvenanceInput) { input.ValidUntil = input.ObservedAt }},
		{"unbounded validity", func(input *GitOpsProvenanceInput) {
			input.ValidUntil = input.ObservedAt.Add(maxBundleValidity + time.Nanosecond)
		}},
		{"symbolic base", func(input *GitOpsProvenanceInput) { input.BaseRef = "HEAD" }},
		{"full base ref", func(input *GitOpsProvenanceInput) { input.BaseRef = "refs/heads/dev" }},
		{"commit-shaped base", func(input *GitOpsProvenanceInput) { input.BaseRef = testBaseSHA }},
		{"invalid base commit", func(input *GitOpsProvenanceInput) { input.BaseCommit = strings.ToUpper(testBaseSHA) }},
		{"invalid blob", func(input *GitOpsProvenanceInput) { input.ObservedBlobSHA = "not-a-blob" }},
		{"empty title", func(input *GitOpsProvenanceInput) { input.Title = "" }},
		{"NUL content", func(input *GitOpsProvenanceInput) { input.DesiredContent = "secret\x00value" }},
		{"unbounded content", func(input *GitOpsProvenanceInput) {
			input.DesiredContent = strings.Repeat("x", maxBundleContentBytes+1)
		}},
		{"no evidence", func(input *GitOpsProvenanceInput) { input.EvidenceRefs = nil }},
		{"duplicate evidence", func(input *GitOpsProvenanceInput) {
			input.EvidenceRefs = append(input.EvidenceRefs, input.EvidenceRefs[0])
		}},
		{"unsafe evidence", func(input *GitOpsProvenanceInput) { input.EvidenceRefs[0].Name = "commit\nforged" }},
		{"noncanonical digest", func(input *GitOpsProvenanceInput) { input.Handler.SchemaDigest = "sha256:" + strings.Repeat("A", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validGitOpsInput(contract)
			test.mutate(&input)
			bundle, err := NewGitOpsProvenanceBundle(input)
			if err == nil || bundle.Version() != "" {
				t.Fatalf("NewGitOpsProvenanceBundle() = %#v, %v, want rejection", bundle, err)
			}
			if strings.Contains(err.Error(), "secret") || len(err.Error()) > 160 {
				t.Fatalf("constructor leaked or returned unbounded error: %q", err)
			}
		})
	}
}

func TestGitOpsResolverRejectsHandlerMismatchAndDrift(t *testing.T) {
	tests := []struct {
		name  string
		want  AbstentionReason
		proxy func(GitOpsHandler) GitOpsHandler
	}{
		{"target mismatch", ReasonHandlerTargetMismatch, func(base GitOpsHandler) GitOpsHandler {
			return &handlerProxy{base: base, canonicalize: func(target fleet.ResourceRef, document json.RawMessage) (fleet.ResourceRef, json.RawMessage) {
				target.Name = "other"
				return target, document
			}}
		}},
		{"output commit mismatch", ReasonHandlerOutputMismatch, func(base GitOpsHandler) GitOpsHandler {
			return &handlerProxy{base: base, canonicalize: func(target fleet.ResourceRef, document json.RawMessage) (fleet.ResourceRef, json.RawMessage) {
				var args openPRArguments
				if err := json.Unmarshal(document, &args); err != nil {
					panic(err)
				}
				args.ExpectedBaseSHA = strings.Repeat("c", 40)
				encoded, err := json.Marshal(args)
				if err != nil {
					panic(err)
				}
				return target, encoded
			}}
		}},
		{"output blob mismatch", ReasonHandlerOutputMismatch, func(base GitOpsHandler) GitOpsHandler {
			return &handlerProxy{base: base, canonicalize: func(target fleet.ResourceRef, document json.RawMessage) (fleet.ResourceRef, json.RawMessage) {
				var args openPRArguments
				if err := json.Unmarshal(document, &args); err != nil {
					panic(err)
				}
				blob := strings.Repeat("d", 40)
				args.Changes[0].ExpectedBlobSHA = &blob
				encoded, err := json.Marshal(args)
				if err != nil {
					panic(err)
				}
				return target, encoded
			}}
		}},
		{"repository mismatch", ReasonHandlerTargetMismatch, func(base GitOpsHandler) GitOpsHandler {
			return &handlerProxy{base: base, canonicalize: func(target fleet.ResourceRef, document json.RawMessage) (fleet.ResourceRef, json.RawMessage) {
				target.Namespace = "Other"
				return target, document
			}}
		}},
		{"post-canonicalization descriptor drift", ReasonHandlerContractDrift, func(base GitOpsHandler) GitOpsHandler {
			return &handlerProxy{base: base, mutateDescriptor: func(descriptor connector.Descriptor, call int) connector.Descriptor {
				if call >= 3 {
					descriptor.AdapterVersion += "/drifted"
				}
				return descriptor
			}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := newGitHubPlanner(t)
			contract, err := HandlerContractFor(base)
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := NewGitOpsProvenanceBundle(validGitOpsInput(contract))
			if err != nil {
				t.Fatal(err)
			}
			resolver, err := NewGitOpsResolver(test.proxy(base), func() time.Time { return testNow })
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolver.Resolve(context.Background(), testWorkspace, validGitOpsVerdict(brain.RuleConfigDrift), []GitOpsProvenanceBundle{bundle})
			if err != nil {
				t.Fatal(err)
			}
			assertAbstention(t, got, test.want)
		})
	}
}

func TestGitOpsResolverRejectsInvalidDependenciesAndCancellation(t *testing.T) {
	t.Parallel()
	var typedNil *handlerProxy
	if resolver, err := NewGitOpsResolver(typedNil, func() time.Time { return testNow }); err == nil || resolver != nil {
		t.Fatalf("NewGitOpsResolver(typed nil) = %#v, %v", resolver, err)
	}
	if resolver, err := NewGitOpsResolver(newGitHubPlanner(t), nil); err == nil || resolver != nil {
		t.Fatalf("NewGitOpsResolver(nil clock) = %#v, %v", resolver, err)
	}
	if _, err := HandlerContractFor(typedNil); err == nil {
		t.Fatal("HandlerContractFor() accepted typed nil")
	}

	fixture := newGitOpsFixture(t, brain.RuleConfigDrift)
	var nilResolver *GitOpsResolver
	if _, err := nilResolver.Resolve(context.Background(), testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle}); err == nil {
		t.Fatal("Resolve() accepted a nil resolver")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.resolver.Resolve(ctx, testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Resolve(canceled) error = %v", err)
	}
	var missingContext context.Context
	if _, err := fixture.resolver.Resolve(missingContext, testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle}); err == nil {
		t.Fatal("Resolve(nil context) succeeded")
	}
	if _, err := fixture.resolver.Resolve(context.Background(), " invalid", fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle}); err == nil {
		t.Fatal("Resolve(invalid workspace) succeeded")
	}
	zeroClock, err := NewGitOpsResolver(newGitHubPlanner(t), func() time.Time { return time.Time{} })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zeroClock.Resolve(context.Background(), testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle}); err == nil {
		t.Fatal("Resolve(zero clock) succeeded")
	}
}

func TestNewGitOpsResolverRejectsNoncanonicalHandlerDescriptors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(connector.Descriptor) connector.Descriptor
	}{
		{"wrong owner", func(descriptor connector.Descriptor) connector.Descriptor {
			descriptor.Owner = "caller"
			return descriptor
		}},
		{"wire version missing", func(descriptor connector.Descriptor) connector.Descriptor {
			descriptor.WireVersions = nil
			return descriptor
		}},
		{"execute capability", func(descriptor connector.Descriptor) connector.Descriptor {
			descriptor.Capabilities = append(descriptor.Capabilities, connector.CapExecute)
			return descriptor
		}},
		{"extra verb", func(descriptor connector.Descriptor) connector.Descriptor {
			descriptor.Verbs = append(descriptor.Verbs, intent.VerbDeploymentRestart)
			return descriptor
		}},
		{"invalid schema", func(descriptor connector.Descriptor) connector.Descriptor {
			descriptor.ArgSchemas[intent.VerbGitOpsOpenPR] = json.RawMessage(`{"type":"not-a-type"}`)
			return descriptor
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proxy := &handlerProxy{base: newGitHubPlanner(t), mutateDescriptor: func(descriptor connector.Descriptor, _ int) connector.Descriptor {
				return test.mutate(descriptor)
			}}
			if resolver, err := NewGitOpsResolver(proxy, func() time.Time { return testNow }); err == nil || resolver != nil {
				t.Fatalf("NewGitOpsResolver() = %#v, %v, want descriptor rejection", resolver, err)
			}
		})
	}
}

func TestGitOpsResolverHonorsCancellationAfterHandlerValidation(t *testing.T) {
	t.Parallel()
	base := newGitHubPlanner(t)
	contract, err := HandlerContractFor(base)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := NewGitOpsProvenanceBundle(validGitOpsInput(contract))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	proxy := &handlerProxy{base: base, canonicalize: func(target fleet.ResourceRef, document json.RawMessage) (fleet.ResourceRef, json.RawMessage) {
		cancel()
		return target, document
	}}
	resolver, err := NewGitOpsResolver(proxy, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx, testWorkspace, validGitOpsVerdict(brain.RuleConfigDrift), []GitOpsProvenanceBundle{bundle}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Resolve() error = %v, want cancellation", err)
	}
}

func TestGitOpsResolverHonorsCancellationAfterFinalContractCheck(t *testing.T) {
	t.Parallel()
	base := newGitHubPlanner(t)
	contract, err := HandlerContractFor(base)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := NewGitOpsProvenanceBundle(validGitOpsInput(contract))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	proxy := &handlerProxy{base: base, mutateDescriptor: func(descriptor connector.Descriptor, call int) connector.Descriptor {
		if call >= 3 {
			cancel()
		}
		return descriptor
	}}
	resolver, err := NewGitOpsResolver(proxy, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx, testWorkspace, validGitOpsVerdict(brain.RuleConfigDrift), []GitOpsProvenanceBundle{bundle}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Resolve() error = %v, want final-check cancellation", err)
	}
}

func TestGitOpsResolverRechecksFreshnessBeforeReturningReady(t *testing.T) {
	t.Parallel()
	planner := newGitHubPlanner(t)
	contract, err := HandlerContractFor(planner)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := NewGitOpsProvenanceBundle(validGitOpsInput(contract))
	if err != nil {
		t.Fatal(err)
	}
	clockCalls := 0
	resolver, err := NewGitOpsResolver(planner, func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return testNow
		}
		return testNow.Add(2 * time.Minute)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Resolve(context.Background(), testWorkspace, validGitOpsVerdict(brain.RuleConfigDrift), []GitOpsProvenanceBundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	assertAbstention(t, got, ReasonProvenanceStale)
}

func TestGitOpsResolverIsConcurrentAndDeterministic(t *testing.T) {
	t.Parallel()
	fixture := newGitOpsFixture(t, brain.RuleConfigDrift)
	want, err := fixture.resolver.Resolve(context.Background(), testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle})
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 64
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, resolveErr := fixture.resolver.Resolve(context.Background(), testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{fixture.bundle})
			if resolveErr != nil {
				errors <- resolveErr
				return
			}
			encoded, marshalErr := json.Marshal(got)
			if marshalErr != nil {
				errors <- marshalErr
				return
			}
			if !slices.Equal(encoded, wantJSON) {
				errors <- fmt.Errorf("nondeterministic resolution")
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func FuzzGitOpsResolverNeverPanics(f *testing.F) {
	fixture := newGitOpsFixture(f, brain.RuleConfigDrift)
	f.Add("deploy/payments.yaml", "replicas: 4\n", "dev")
	f.Add("../secret", "marker\x00value", "HEAD")
	f.Fuzz(func(t *testing.T, path, content, baseRef string) {
		bundle := cloneBundle(fixture.bundle)
		bundle.filePath = path
		bundle.desiredContent = content
		bundle.baseRef = baseRef
		got, err := fixture.resolver.Resolve(context.Background(), testWorkspace, fixture.verdict, []GitOpsProvenanceBundle{bundle})
		if err != nil {
			if len(err.Error()) > 160 {
				t.Fatalf("unbounded error length %d", len(err.Error()))
			}
			return
		}
		encoded, marshalErr := json.Marshal(got)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if len(encoded) > maxBundleContentBytes+(16<<10) {
			t.Fatalf("unbounded resolution length %d", len(encoded))
		}
	})
}

type gitOpsFixture struct {
	resolver *GitOpsResolver
	verdict  brain.Verdict
	bundle   GitOpsProvenanceBundle
}

func newGitOpsFixture(t testing.TB, rule brain.RuleID) gitOpsFixture {
	t.Helper()
	planner := newGitHubPlanner(t)
	contract, err := HandlerContractFor(planner)
	if err != nil {
		t.Fatalf("HandlerContractFor() error = %v", err)
	}
	bundle, err := NewGitOpsProvenanceBundle(validGitOpsInput(contract))
	if err != nil {
		t.Fatalf("NewGitOpsProvenanceBundle() error = %v", err)
	}
	resolver, err := NewGitOpsResolver(planner, func() time.Time { return testNow })
	if err != nil {
		t.Fatalf("NewGitOpsResolver() error = %v", err)
	}
	return gitOpsFixture{resolver: resolver, verdict: validGitOpsVerdict(rule), bundle: bundle}
}

func newGitHubPlanner(t testing.TB) *githubconnector.OpenPRPlanner {
	t.Helper()
	planner, err := githubconnector.NewOpenPRPlanner(githubconnector.OpenPRPlannerConfig{
		Host: "github.com", Owner: "ArdurAI", Repository: "sith", BaseRef: "dev",
	})
	if err != nil {
		t.Fatalf("NewOpenPRPlanner() error = %v", err)
	}
	return planner
}

func validGitOpsInput(contract HandlerContract) GitOpsProvenanceInput {
	subject := testSubjectRef()
	return GitOpsProvenanceInput{
		Workspace: testWorkspace,
		Subject:   subject,
		Sources: []SourceIdentity{{
			Kind: gitHubSourceKind, AdapterVersion: GitOpsSourceAdapterVersion, NativeID: "github.com/ArdurAI/sith",
		}},
		ObservedAt: testNow.Add(-time.Minute), ValidUntil: testNow.Add(time.Minute), Handler: contract,
		Repository: RepositoryIdentity{Host: "github.com", Owner: "ArdurAI", Repository: "sith"},
		BaseRef:    "dev", BaseCommit: testBaseSHA, FilePath: "deploy/payments.yaml", ObservedBlobSHA: testBlobSHA,
		DesiredContent: "replicas: 4\n", Title: "Reconcile payments resources", Body: "Source-owned drift remediation\n\nEvidence is attached.",
		CommitMessage: "Reconcile payments resources",
		EvidenceRefs: []fleet.ResourceRef{
			subject,
			{SourceKind: gitHubSourceKind, Scope: "github.com", Kind: "Blob", Namespace: "ArdurAI/sith", Name: testBlobSHA},
		},
	}
}

func validGitOpsVerdict(rule brain.RuleID) brain.Verdict {
	ref := testSubjectRef()
	lens, predicate, observed := fleet.LensDesired, "desired.drift", "OutOfSync"
	if rule == brain.RuleOOMKilled {
		lens, predicate, observed = fleet.LensLive, "pod.reason", "OOMKilled"
	}
	return brain.Verdict{
		Rule: rule, Status: brain.StatusConfirmed, Ref: ref,
		Citations: []brain.Citation{{
			Ref: ref, Lens: lens, Predicate: predicate, Observed: observed,
			Weight: 60, ObservedAt: testNow.Add(-2 * time.Minute), Source: "fixture",
		}},
		RemediationCandidate: &brain.RemediationCandidate{
			Verb: intent.VerbGitOpsOpenPR,
			RequiredProvenance: []brain.ProvenanceRequirement{
				brain.ProvenanceGitRepository, brain.ProvenanceGitBaseRef, brain.ProvenanceGitBaseCommit,
				brain.ProvenanceGitFilePath, brain.ProvenanceGitObservedBlob, brain.ProvenanceGitDesiredContent,
			},
		},
	}
}

func testSubjectRef() fleet.ResourceRef {
	return fleet.ResourceRef{SourceKind: "kubeconfig", Scope: "alpha", Kind: "Deployment", Namespace: "prod", Name: "payments"}
}

func assertReadyResolution(t testing.TB, got Resolution) {
	t.Helper()
	if got.Status != ResolutionReady || len(got.Reasons) != 0 || got.ArgumentsDigest == "" || len(got.EvidenceRefs) != 2 {
		t.Fatalf("resolution = %#v, want ready", got)
	}
	wantTarget := fleet.ResourceRef{SourceKind: gitHubSourceKind, Scope: "github.com", Kind: openPRTargetKind, Namespace: "ArdurAI", Name: "sith"}
	if !sameResourceRef(got.Target, wantTarget) {
		t.Fatalf("target = %#v, want %#v", got.Target, wantTarget)
	}
	var args openPRArguments
	if err := json.Unmarshal(got.Arguments, &args); err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	if args.BaseRef != "dev" || args.ExpectedBaseSHA != testBaseSHA || len(args.Changes) != 1 ||
		args.Changes[0].Path != "deploy/payments.yaml" || args.Changes[0].Content == nil ||
		*args.Changes[0].Content != "replicas: 4\n" || args.Changes[0].ExpectedBlobSHA == nil ||
		*args.Changes[0].ExpectedBlobSHA != testBlobSHA {
		t.Fatalf("arguments = %#v, want exact source-owned values", args)
	}
	digest := sha256.Sum256(got.Arguments)
	wantDigest := "sha256:" + hex.EncodeToString(digest[:])
	if got.ArgumentsDigest != wantDigest {
		t.Fatalf("digest = %q, want %q", got.ArgumentsDigest, wantDigest)
	}
	if got.EvidenceRefs[0].String() > got.EvidenceRefs[1].String() {
		t.Fatalf("evidence refs are not canonical: %#v", got.EvidenceRefs)
	}
}

func assertAbstention(t testing.TB, got Resolution, want AbstentionReason) {
	t.Helper()
	if got.Status != ResolutionAbstained || !slices.Equal(got.Reasons, []AbstentionReason{want}) ||
		!zeroResourceRef(got.Target) || len(got.Arguments) != 0 || got.ArgumentsDigest != "" || len(got.EvidenceRefs) != 0 {
		t.Fatalf("resolution = %#v, want closed abstention %q", got, want)
	}
}

func zeroResourceRef(ref fleet.ResourceRef) bool {
	return ref.SourceKind == "" && ref.Scope == "" && ref.Kind == "" && ref.Namespace == "" && ref.Name == "" && len(ref.Attributes) == 0
}

func cloneVerdict(verdict brain.Verdict) brain.Verdict {
	cloned := verdict
	cloned.Ref = cloneResourceRef(verdict.Ref)
	cloned.Citations = slices.Clone(verdict.Citations)
	for index := range cloned.Citations {
		cloned.Citations[index].Ref = cloneResourceRef(cloned.Citations[index].Ref)
	}
	if verdict.RemediationCandidate != nil {
		candidate := *verdict.RemediationCandidate
		candidate.RequiredProvenance = slices.Clone(verdict.RemediationCandidate.RequiredProvenance)
		cloned.RemediationCandidate = &candidate
	}
	return cloned
}

func cloneBundle(bundle GitOpsProvenanceBundle) GitOpsProvenanceBundle {
	cloned := bundle
	cloned.subject = cloneResourceRef(bundle.subject)
	cloned.evidenceRefs = cloneResourceRefs(bundle.evidenceRefs)
	return cloned
}

type handlerProxy struct {
	base             GitOpsHandler
	descriptorCalls  int
	mutateDescriptor func(connector.Descriptor, int) connector.Descriptor
	canonicalize     func(fleet.ResourceRef, json.RawMessage) (fleet.ResourceRef, json.RawMessage)
}

func (proxy *handlerProxy) Descriptor() connector.Descriptor {
	proxy.descriptorCalls++
	descriptor := cloneDescriptor(proxy.base.Descriptor())
	if proxy.mutateDescriptor != nil {
		return proxy.mutateDescriptor(descriptor, proxy.descriptorCalls)
	}
	return descriptor
}

func (proxy *handlerProxy) CanonicalizeOpenPRArgs(arguments json.RawMessage) (fleet.ResourceRef, json.RawMessage, error) {
	target, document, err := proxy.base.CanonicalizeOpenPRArgs(arguments)
	if err != nil || proxy.canonicalize == nil {
		return target, document, err
	}
	target, document = proxy.canonicalize(target, append(json.RawMessage(nil), document...))
	return target, document, nil
}

func cloneDescriptor(descriptor connector.Descriptor) connector.Descriptor {
	cloned := descriptor
	cloned.WireVersions = slices.Clone(descriptor.WireVersions)
	cloned.Capabilities = slices.Clone(descriptor.Capabilities)
	cloned.Verbs = slices.Clone(descriptor.Verbs)
	cloned.ArgSchemas = make(map[intent.Verb]json.RawMessage, len(descriptor.ArgSchemas))
	for verb, schema := range descriptor.ArgSchemas {
		cloned.ArgSchemas[verb] = append(json.RawMessage(nil), schema...)
	}
	return cloned
}
