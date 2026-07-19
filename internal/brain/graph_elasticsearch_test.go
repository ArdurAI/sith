// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	connectorelasticsearch "github.com/ArdurAI/sith/internal/connector/elasticsearch"
	"github.com/ArdurAI/sith/internal/fleet"
)

func TestFromGraphFactsProjectsReviewedElasticsearchLogCausesIntoR3(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 23, 30, 0, 123000000, time.UTC)
	tests := []struct {
		cause   string
		message string
	}{
		{cause: "panic", message: "panic: discard-raw-secret"},
		{cause: "missing-config", message: "missing required environment variable DISCARD_RAW_SECRET"},
		{cause: "dependency-failure", message: "failed to connect to discard-raw-dependency"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.cause, func(t *testing.T) {
			t.Parallel()
			facts := projectedElasticsearchLogCause(t, test.message, eventAt)
			input, err := FromGraphFacts(
				fleet.LocalWorkspace,
				facts,
				covered(fleet.LensLive, fleet.LensTelemetry),
			)
			if err != nil {
				t.Fatalf("FromGraphFacts() error = %v", err)
			}
			if len(input.Observations) != 1 {
				t.Fatalf("observations = %#v, want one sanitized log cause", input.Observations)
			}
			observation := input.Observations[0]
			wantRef := fleet.ResourceRef{
				SourceKind: "elasticsearch", Scope: "cluster-a", Kind: "Pod",
				Namespace: "payments", Name: "api-0",
			}
			if !reflect.DeepEqual(observation.Ref, wantRef) ||
				observation.Lens != fleet.LensTelemetry ||
				observation.Key != "logs.cause" ||
				observation.Value != test.cause ||
				!observation.ObservedAt.Equal(eventAt) ||
				observation.Source != "cluster-a" ||
				observation.Stale {
				t.Fatalf("observation = %#v, want exact Pod-scoped Elasticsearch cause", observation)
			}

			input.Observations = append(input.Observations, crashLoopObservation(eventAt.Add(time.Minute)))
			result, err := Evaluate(input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 1 {
				t.Fatalf("verdicts = %#v, want one R3 verdict", result.Verdicts)
			}
			verdict := result.Verdicts[0]
			if verdict.Rule != RuleCrashLoop || verdict.Status != StatusConfirmed ||
				verdict.Score != 6 || verdict.FleetWide {
				t.Fatalf("verdict = %#v, want confirmed entity-local R3", verdict)
			}
			citations := citationsForPredicate(verdict.Citations, "logs.cause")
			if len(citations) != 1 || citations[0].Observed != test.cause ||
				!citations[0].ObservedAt.Equal(eventAt) ||
				citations[0].Source != "cluster-a" ||
				citations[0].Stale {
				t.Fatalf("log citations = %#v, want one fresh closed cause", citations)
			}

			encoded, err := json.Marshal(struct {
				Input  Investigation `json:"input"`
				Result Result        `json:"result"`
			}{Input: input, Result: result})
			if err != nil {
				t.Fatalf("marshal projected result: %v", err)
			}
			for _, discarded := range []string{
				"discard-raw-secret",
				"DISCARD_RAW_SECRET",
				"discard-raw-dependency",
				`"count"`,
				`"container"`,
				`"first_event_at"`,
				`"last_event_at"`,
			} {
				if strings.Contains(string(encoded), discarded) {
					t.Fatalf("brain output retained discarded Elasticsearch data %q: %s", discarded, encoded)
				}
			}
		})
	}
}

func TestFromGraphFactsPreservesElasticsearchStalenessAndDeclaredCoverage(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 23, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		stale    bool
		coverage map[fleet.Lens]LensCoverage
	}{
		{
			name:     "stale fact",
			stale:    true,
			coverage: covered(fleet.LensLive, fleet.LensTelemetry),
		},
		{
			name:     "telemetry coverage omitted",
			coverage: covered(fleet.LensLive),
		},
		{
			name: "telemetry coverage unavailable",
			coverage: map[fleet.Lens]LensCoverage{
				fleet.LensLive:      {Available: true},
				fleet.LensTelemetry: {Reason: "log connector unavailable"},
			},
		},
		{
			name: "telemetry coverage stale",
			coverage: map[fleet.Lens]LensCoverage{
				fleet.LensLive:      {Available: true},
				fleet.LensTelemetry: {Available: true, Stale: true, Reason: "log search stale"},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := projectedElasticsearchLogCause(t, "panic: discard-me", eventAt)
			facts[0].Fact.Stale = test.stale
			input, err := FromGraphFacts(fleet.LocalWorkspace, facts, test.coverage)
			if err != nil {
				t.Fatalf("FromGraphFacts() error = %v", err)
			}
			input.Observations = append(input.Observations, crashLoopObservation(eventAt.Add(time.Minute)))
			result, err := Evaluate(input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleCrashLoop ||
				result.Verdicts[0].Status != StatusDetected ||
				!reflect.DeepEqual(result.Verdicts[0].MissingLenses, []fleet.Lens{fleet.LensTelemetry}) {
				t.Fatalf("verdicts = %#v, want coverage-honest detected R3", result.Verdicts)
			}
			citations := citationsForPredicate(result.Verdicts[0].Citations, "logs.cause")
			if len(citations) != 1 || citations[0].Stale != test.stale {
				t.Fatalf("log citations = %#v, stale = %t", citations, test.stale)
			}
		})
	}

	facts := projectedElasticsearchLogCause(t, "panic: discard-me", eventAt)
	input, err := FromGraphFacts(fleet.LocalWorkspace, facts, nil)
	if err != nil {
		t.Fatalf("FromGraphFacts() error = %v", err)
	}
	if len(input.Coverage) != 0 {
		t.Fatalf("coverage = %#v, fact presence must not infer coverage", input.Coverage)
	}
}

func TestFromGraphFactsFailsClosedOnAmbiguousElasticsearchLogCauseFacts(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 23, 30, 0, 0, time.UTC)
	baseFacts := projectedElasticsearchLogCause(t, "panic: discard-me", eventAt)
	if len(baseFacts) != 1 {
		t.Fatalf("base facts = %#v, want one log-cause fact", baseFacts)
	}
	base := baseFacts[0]
	payload := func(key, value string, count int, first, last time.Time, container, extra string) json.RawMessage {
		t.Helper()
		raw := `{"key":` + quotedJSON(t, key) +
			`,"value":` + quotedJSON(t, value) +
			`,"count":` + quotedJSON(t, count) +
			`,"first_event_at":` + quotedJSON(t, first.Format(time.RFC3339Nano)) +
			`,"last_event_at":` + quotedJSON(t, last.Format(time.RFC3339Nano))
		if container != "" {
			raw += `,"container":` + quotedJSON(t, container)
		}
		return json.RawMessage(raw + extra + `}`)
	}
	validPayload := func(extra string) json.RawMessage {
		t.Helper()
		return payload("logs.cause", "panic", 1, eventAt, eventAt, "api", extra)
	}

	tests := []struct {
		name   string
		mutate func(*fleet.GraphFact)
	}{
		{name: "workspace mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Workspace = "other"
		}},
		{name: "both source fields mismatch with exact protocol", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.SourceKind = "other"
			fact.Fact.Provenance.Adapter = "other"
		}},
		{name: "source kind mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.SourceKind = "other" }},
		{name: "provenance adapter mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Provenance.Adapter = "other" }},
		{name: "exact protocol on non-telemetry fact", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Kind = fleet.FactHealth
			fact.Lens = fleet.LensLive
		}},
		{name: "protocol mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.ProtocolV = "search/ecs-v2"
		}},
		{name: "unexpected provenance deep link", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.DeepLink = "https://private.example.invalid/logs"
		}},
		{name: "unexpected provenance collector", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.Collector = "unreviewed-collector"
		}},
		{name: "resource kind mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Kind = "OtherSignal" }},
		{name: "resource scope missing", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Scope = ""
			fact.Fact.Source = ""
			fact.Entity.Cluster = ""
		}},
		{name: "resource namespace missing", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Namespace = ""
			fact.Entity.Namespace = ""
		}},
		{name: "Pod entity missing", mutate: func(fact *fleet.GraphFact) { fact.Entity = nil }},
		{name: "entity cluster mismatch", mutate: func(fact *fleet.GraphFact) { fact.Entity.Cluster = "cluster-b" }},
		{name: "entity namespace mismatch", mutate: func(fact *fleet.GraphFact) { fact.Entity.Namespace = "other" }},
		{name: "entity Pod missing", mutate: func(fact *fleet.GraphFact) { fact.Entity.Pod = "" }},
		{name: "entity Pod retargeted", mutate: func(fact *fleet.GraphFact) { fact.Entity.Pod = "api-1" }},
		{name: "source scope retargeted consistently", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Scope = "cluster-b"
			fact.Fact.Source = "cluster-b"
			fact.Entity.Cluster = "cluster-b"
		}},
		{name: "namespace retargeted consistently", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Namespace = "other"
			fact.Entity.Namespace = "other"
		}},
		{name: "entity carries kind", mutate: func(fact *fleet.GraphFact) {
			fact.Entity.Kind = "Pod"
			fact.Entity.Name = "api-0"
		}},
		{name: "entity carries node", mutate: func(fact *fleet.GraphFact) { fact.Entity.Node = "worker-a" }},
		{name: "entity carries image digest", mutate: func(fact *fleet.GraphFact) {
			fact.Entity.ImageDigest = "sha256:" + strings.Repeat("a", 64)
		}},
		{name: "evidence source mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Source = "cluster-b" }},
		{name: "unexpected reference attributes", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Attributes = map[string]string{"message": "discard-me"}
		}},
		{name: "unexpected display field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Display = []fleet.DisplayField{{Name: "message", Value: "discard-me"}}
		}},
		{name: "native prefix mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.NativeID = "md5:" + strings.Repeat("a", 64)
		}},
		{name: "native digest length mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.NativeID = "sha256:" + strings.Repeat("a", 63)
		}},
		{name: "native digest case mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.NativeID = "sha256:" + strings.Repeat("A", 64)
		}},
		{name: "resource digest prefix mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Name = "log-" + strings.Repeat("b", 32)
		}},
		{name: "unknown payload field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = validPayload(`,"message":"discard-me"`)
		}},
		{name: "duplicate payload field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = validPayload(`,"value":"panic"`)
		}},
		{name: "mixed-case payload alias", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = validPayload(`,"VALUE":"panic"`)
		}},
		{name: "unsupported key", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.message", "panic", 1, eventAt, eventAt, "api", "")
		}},
		{name: "unsupported cause", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "credential-failure", 1, eventAt, eventAt, "api", "")
		}},
		{name: "supported cause retargeted without native identity", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "missing-config", 1, eventAt, eventAt, "api", "")
		}},
		{name: "valid count retargeted without native identity", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 2, eventAt, eventAt, "api", "")
		}},
		{name: "valid event interval retargeted without native identity", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 1, eventAt.Add(-time.Second), eventAt, "api", "")
		}},
		{name: "valid container retargeted without native identity", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 1, eventAt, eventAt, "other", "")
		}},
		{name: "collection time retargeted without native identity", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.ObservedAt = fact.Fact.ObservedAt.Add(time.Second)
		}},
		{name: "missing key", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(string(validPayload("")), `"key":"logs.cause",`, "", 1))
		}},
		{name: "missing value", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(string(validPayload("")), `,"value":"panic"`, "", 1))
		}},
		{name: "missing count", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(string(validPayload("")), `,"count":1`, "", 1))
		}},
		{name: "missing first event", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(
				string(validPayload("")),
				`,"first_event_at":"`+eventAt.Format(time.RFC3339Nano)+`"`,
				"",
				1,
			))
		}},
		{name: "missing last event", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(
				string(validPayload("")),
				`,"last_event_at":"`+eventAt.Format(time.RFC3339Nano)+`"`,
				"",
				1,
			))
		}},
		{name: "zero count", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 0, eventAt, eventAt, "api", "")
		}},
		{name: "negative count", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", -1, eventAt, eventAt, "api", "")
		}},
		{name: "count above projector bound", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", maxElasticsearchCauseCount+1, eventAt, eventAt, "api", "")
		}},
		{name: "count has wrong type", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(string(validPayload("")), `"count":1`, `"count":"1"`, 1))
		}},
		{name: "event order reversed", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 1, eventAt, eventAt.Add(-time.Second), "api", "")
		}},
		{name: "event interval above projector bound", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(
				"logs.cause", "panic", 1,
				eventAt.Add(-maxElasticsearchCauseWindow-time.Nanosecond), eventAt, "api", "",
			)
		}},
		{name: "last event exceeds clock skew", mutate: func(fact *fleet.GraphFact) {
			future := fact.Fact.ObservedAt.Add(maxElasticsearchClockSkew + time.Nanosecond)
			fact.Fact.Observed = payload("logs.cause", "panic", 1, future, future, "api", "")
		}},
		{name: "null event time", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(
				string(validPayload("")),
				`"first_event_at":"`+eventAt.Format(time.RFC3339Nano)+`"`,
				`"first_event_at":null`,
				1,
			))
		}},
		{name: "container null", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(string(validPayload("")), `"container":"api"`, `"container":null`, 1))
		}},
		{name: "container empty", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(string(validPayload("")), `"container":"api"`, `"container":""`, 1))
		}},
		{name: "container invalid control", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 1, eventAt, eventAt, "api\nother", "")
		}},
		{name: "container has surrounding whitespace", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 1, eventAt, eventAt, " api ", "")
		}},
		{name: "container oversized", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("logs.cause", "panic", 1, eventAt, eventAt, strings.Repeat("a", 254), "")
		}},
		{name: "container wrong type", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Replace(string(validPayload("")), `"container":"api"`, `"container":7`, 1))
		}},
		{name: "empty payload", mutate: func(fact *fleet.GraphFact) { fact.Fact.Observed = nil }},
		{name: "oversized payload", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Repeat(" ", maxElasticsearchLogCausePayload+1))
		}},
		{name: "multiple JSON values", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = append(append(json.RawMessage(nil), fact.Fact.Observed...), []byte(` {}`)...)
		}},
		{name: "malformed JSON", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(`{"key":`)
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fact := cloneBrainGraphFact(base)
			test.mutate(&fact)
			if _, err := FromGraphFacts(
				fleet.LocalWorkspace,
				[]fleet.GraphFact{fact},
				covered(fleet.LensTelemetry),
			); err == nil {
				t.Fatalf("FromGraphFacts() error = nil for %#v", fact)
			}
		})
	}
}

func TestDecodeElasticsearchLogCausePayloadRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()
	raw := append([]byte(`{"key":"logs.cause","value":"panic","count":1,"first_event_at":"2026-07-18T23:30:00Z","last_event_at":"2026-07-18T23:30:00Z","container":"`), 0xff)
	raw = append(raw, []byte(`"}`)...)

	if _, err := decodeElasticsearchLogCausePayload(raw); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("decodeElasticsearchLogCausePayload() error = %v, want invalid UTF-8", err)
	}
}

func TestFromGraphFactsDoesNotCorrelateElasticsearchCauseAcrossPods(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 23, 30, 0, 0, time.UTC)
	facts := projectedElasticsearchLogCause(t, "panic: discard-me", eventAt)
	input, err := FromGraphFacts(
		fleet.LocalWorkspace,
		facts,
		covered(fleet.LensLive, fleet.LensTelemetry),
	)
	if err != nil {
		t.Fatalf("FromGraphFacts() error = %v", err)
	}
	crashLoop := crashLoopObservation(eventAt.Add(time.Minute))
	crashLoop.Ref.Name = "api-1"
	input.Observations = append(input.Observations, crashLoop)

	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleCrashLoop ||
		result.Verdicts[0].Status != StatusDetected || result.Verdicts[0].Score != 3 ||
		len(citationsForPredicate(result.Verdicts[0].Citations, "logs.cause")) != 0 {
		t.Fatalf("verdicts = %#v, cross-Pod log evidence must not strengthen R3", result.Verdicts)
	}
}

func TestFromGraphFactsIgnoresUnrelatedDerivedTelemetryFacts(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 23, 30, 0, 0, time.UTC)
	facts := projectedElasticsearchLogCause(t, "panic: discard-me", eventAt)
	facts[0].Fact.Ref.SourceKind = "other"
	facts[0].Fact.Ref.Kind = "OtherSignal"
	facts[0].Fact.Provenance.Adapter = "other"
	facts[0].Fact.Provenance.ProtocolV = "other/v1"

	input, err := FromGraphFacts(fleet.LocalWorkspace, facts, covered(fleet.LensTelemetry))
	if err != nil {
		t.Fatalf("FromGraphFacts() error = %v", err)
	}
	if len(input.Observations) != 0 {
		t.Fatalf("observations = %#v, unrelated derived fact must be ignored", input.Observations)
	}
}

func FuzzFromGraphFactsElasticsearchLogCausePayload(f *testing.F) {
	eventAt := time.Date(2026, 7, 18, 23, 30, 0, 0, time.UTC)
	base := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: "elasticsearch", Scope: "cluster-a", Kind: "LogSignal",
					Namespace: "payments", Name: "log-" + strings.Repeat("0", 32),
				},
				Kind:       fleet.FactDerived,
				ObservedAt: eventAt.Add(time.Minute),
				Source:     "cluster-a",
				Provenance: fleet.Provenance{
					Adapter: "elasticsearch", ProtocolV: "search/ecs-v1",
					NativeID: "sha256:" + strings.Repeat("0", 64),
				},
			},
			Workspace: fleet.LocalWorkspace,
		},
		Lens: fleet.LensTelemetry,
		Entity: &fleet.EntityRef{
			Cluster: "cluster-a", Namespace: "payments", Pod: "api-0",
		},
	}
	for _, seed := range [][]byte{
		[]byte(`{"key":"logs.cause","value":"panic","count":1,"first_event_at":"2026-07-18T23:30:00Z","last_event_at":"2026-07-18T23:30:00Z","container":"api"}`),
		[]byte(`{"key":"logs.cause","value":"dependency-failure","count":2,"first_event_at":"2026-07-18T23:29:00Z","last_event_at":"2026-07-18T23:30:00Z"}`),
		[]byte(`{"KEY":"logs.cause"}`),
		[]byte(`null`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		fact := cloneBrainGraphFact(base)
		fact.Fact.Observed = append(json.RawMessage(nil), payload...)
		decoded, decodeErr := decodeElasticsearchLogCausePayload(fact.Fact.Observed)
		if decodeErr == nil {
			digest, digestErr := elasticsearchLogCauseDigest(fact, decoded)
			if digestErr != nil {
				t.Fatalf("elasticsearchLogCauseDigest() error = %v", digestErr)
			}
			fact.Fact.Provenance.NativeID = "sha256:" + digest
			fact.Fact.Ref.Name = "log-" + digest[:32]
		}
		input, err := FromGraphFacts(
			fleet.LocalWorkspace,
			[]fleet.GraphFact{fact},
			covered(fleet.LensTelemetry),
		)
		if err != nil {
			return
		}
		if len(input.Observations) != 1 {
			t.Fatalf("successful projection observations = %#v", input.Observations)
		}
		observation := input.Observations[0]
		if observation.Ref.SourceKind != "elasticsearch" ||
			observation.Ref.Scope != "cluster-a" ||
			observation.Ref.Kind != "Pod" ||
			observation.Ref.Namespace != "payments" ||
			observation.Ref.Name != "api-0" ||
			observation.Lens != fleet.LensTelemetry ||
			observation.Key != "logs.cause" ||
			(observation.Value != "panic" &&
				observation.Value != "missing-config" &&
				observation.Value != "dependency-failure") ||
			observation.Source != "cluster-a" {
			t.Fatalf("successful projection escaped closed contract: %#v", observation)
		}
	})
}

func projectedElasticsearchLogCause(t *testing.T, message string, eventAt time.Time) []fleet.GraphFact {
	t.Helper()
	response, err := json.Marshal(map[string]any{
		"timed_out": false,
		"_shards": map[string]any{
			"total": 1, "successful": 1, "skipped": 0, "failed": 0,
		},
		"hits": map[string]any{
			"hits": []any{
				map[string]any{
					"fields": map[string]any{
						"@timestamp":                []string{eventAt.Format(time.RFC3339Nano)},
						"message":                   []string{message},
						"orchestrator.cluster.name": []string{"cluster-a"},
						"kubernetes.namespace":      []string{"payments"},
						"kubernetes.pod.name":       []string{"api-0"},
						"kubernetes.container.name": []string{"api"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal Elasticsearch response: %v", err)
	}
	facts, err := connectorelasticsearch.ProjectLogCauses(connectorelasticsearch.Projection{
		Workspace:   fleet.LocalWorkspace,
		Scope:       "cluster-a",
		Namespace:   "payments",
		Pod:         "api-0",
		Container:   "api",
		WindowStart: eventAt.Add(-time.Minute),
		WindowEnd:   eventAt,
		ObservedAt:  eventAt.Add(time.Minute),
		Response:    response,
	})
	if err != nil {
		t.Fatalf("ProjectLogCauses() error = %v", err)
	}
	return facts
}

func crashLoopObservation(observedAt time.Time) Observation {
	return Observation{
		Ref: fleet.ResourceRef{
			SourceKind: "kubeconfig", Scope: "cluster-a", Kind: "Pod",
			Namespace: "payments", Name: "api-0",
		},
		Lens:       fleet.LensLive,
		Key:        "pod.failure",
		Value:      "CrashLoopBackOff",
		ObservedAt: observedAt,
		Source:     "cluster-a",
	}
}
