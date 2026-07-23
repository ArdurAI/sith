// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

type fixtureSeries struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

func TestProjectGPUUtilizationProjectsBoundedAttribution(t *testing.T) {
	t.Parallel()
	input := validProjection(t,
		validSeries("3", "GPU-dddddddd-1111-2222-3333-444444444444", "node-c", "25.2500", map[string]string{
			"GPU_I_ID": "2", "GPU_I_PROFILE": "1g.10gb",
			"namespace": "ml", "pod": "trainer-0", "container": "trainer",
		}),
		validSeries("1", "GPU-bbbbbbbb-1111-2222-3333-444444444444", "node-a", "37.5000", map[string]string{
			"GPU_I_ID": "1", "GPU_I_PROFILE": "2g.20gb",
		}),
		validSeries("2", "GPU-cccccccc-1111-2222-3333-444444444444", "node-b", "100.000", map[string]string{
			"namespace": "inference", "pod": "server-6d8f8f7d9f-abcde", "container": "server",
			"pod_label_team": "sensitive-team", "job": "dcgm-exporter", "instance": "10.0.0.8:9400",
		}),
		validSeries("0", "GPU-aaaaaaaa-1111-2222-3333-444444444444", "node-a", "0.000", nil),
	)

	facts, err := ProjectGPUUtilization(input)
	if err != nil {
		t.Fatalf("project GPU utilization: %v", err)
	}
	if len(facts) != 4 {
		t.Fatalf("fact count = %d, want 4", len(facts))
	}

	seen := make(map[string]gpuUtilizationObservation, len(facts))
	for index, fact := range facts {
		if err := fact.Validate(input.Workspace); err != nil {
			t.Fatalf("fact %d invalid: %v", index, err)
		}
		if fact.Fact.Kind != fleet.FactDerived || fact.Lens != fleet.LensTelemetry {
			t.Fatalf("fact %d has kind/lens %q/%q", index, fact.Fact.Kind, fact.Lens)
		}
		if fact.Fact.Ref.SourceKind != Kind || fact.Fact.Ref.Kind != "GPUUtilization" ||
			fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion {
			t.Fatalf("fact %d has unexpected source contract: %#v", index, fact)
		}
		if !fact.Fact.ObservedAt.Equal(input.Query.EvaluatedAt) || fact.Fact.Source != input.Scope {
			t.Fatalf("fact %d has unexpected time/source", index)
		}
		if !strings.HasPrefix(fact.Fact.Provenance.NativeID, "sha256:") ||
			!strings.HasSuffix(fact.Fact.Ref.Name, strings.TrimPrefix(fact.Fact.Provenance.NativeID, "sha256:")) {
			t.Fatalf("fact %d does not bind its hashed native identity", index)
		}
		if index > 0 && facts[index-1].Fact.Provenance.NativeID >= fact.Fact.Provenance.NativeID {
			t.Fatalf("facts are not sorted by native identity")
		}
		var observation gpuUtilizationObservation
		if err := json.Unmarshal(fact.Fact.Observed, &observation); err != nil {
			t.Fatalf("decode observation %d: %v", index, err)
		}
		key := observation.Attribution + "/" + observation.DeviceScope
		seen[key] = observation
		if observation.Attribution == attributionWorkload {
			if fact.Entity == nil || fact.Entity.Namespace != observation.Namespace || fact.Entity.Pod != observation.Pod ||
				fact.Fact.Ref.Namespace != observation.Namespace {
				t.Fatalf("workload fact %d has mismatched graph identity", index)
			}
		} else if fact.Entity == nil || fact.Entity.Key() != "cluster/cluster-a" || fact.Fact.Ref.Namespace != "" {
			t.Fatalf("device fact %d was attached beyond the cluster root", index)
		}
	}

	assertObservation(t, seen[attributionPhysicalGPU+"/"+attributionPhysicalGPU], gpuUtilizationObservation{
		UtilizationPercent: "0", Attribution: attributionPhysicalGPU,
		DeviceScope: attributionPhysicalGPU, ModelName: "NVIDIA H100 80GB HBM3",
	})
	assertObservation(t, seen[attributionMIGInstance+"/"+attributionMIGInstance], gpuUtilizationObservation{
		UtilizationPercent: "37.5", Attribution: attributionMIGInstance,
		DeviceScope: attributionMIGInstance, ModelName: "NVIDIA H100 80GB HBM3",
		MIGInstanceID: "1", MIGProfile: "2g.20gb",
	})
	assertObservation(t, seen[attributionWorkload+"/"+attributionPhysicalGPU], gpuUtilizationObservation{
		UtilizationPercent: "100", Attribution: attributionWorkload,
		DeviceScope: attributionPhysicalGPU, ModelName: "NVIDIA H100 80GB HBM3",
		Namespace: "inference", Pod: "server-6d8f8f7d9f-abcde", Container: "server",
	})
	assertObservation(t, seen[attributionWorkload+"/"+attributionMIGInstance], gpuUtilizationObservation{
		UtilizationPercent: "25.25", Attribution: attributionWorkload,
		DeviceScope: attributionMIGInstance, ModelName: "NVIDIA H100 80GB HBM3",
		MIGInstanceID: "2", MIGProfile: "1g.10gb", Namespace: "ml", Pod: "trainer-0", Container: "trainer",
	})

	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("encode facts: %v", err)
	}
	for _, secret := range []string{
		"GPU-aaaaaaaa-1111-2222-3333-444444444444", "GPU-bbbbbbbb-1111-2222-3333-444444444444",
		"GPU-cccccccc-1111-2222-3333-444444444444", "GPU-dddddddd-1111-2222-3333-444444444444",
		"node-a", "node-b", "node-c", "10.0.0.8:9400", "sensitive-team", "pod_label_team", "pci_bus_id",
	} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("projected facts retain private source identity %q", secret)
		}
	}
}

func TestProjectGPUUtilizationIsDeterministicAndDropsUnknownLabels(t *testing.T) {
	t.Parallel()
	first := validSeries("0", "GPU-aaaaaaaa-1111-2222-3333-444444444444", "node-a", "42.500", map[string]string{
		"job": "first", "external_label": "private-a",
	})
	second := validSeries("1", "GPU-bbbbbbbb-1111-2222-3333-444444444444", "node-b", "7", map[string]string{
		"GPU_I_ID": "0", "GPU_I_PROFILE": "1g.10gb", "instance": "private-b",
	})
	forward := validProjection(t, first, second)
	reverse := validProjection(t, second, first)
	forwardFacts, err := ProjectGPUUtilization(forward)
	if err != nil {
		t.Fatalf("project forward: %v", err)
	}
	reverseFacts, err := ProjectGPUUtilization(reverse)
	if err != nil {
		t.Fatalf("project reverse: %v", err)
	}
	if !reflect.DeepEqual(forwardFacts, reverseFacts) {
		t.Fatalf("input permutation changed projected facts")
	}

	changedUnknown := validSeries("0", "GPU-aaaaaaaa-1111-2222-3333-444444444444", "node-a", "42.500", map[string]string{
		"job": "changed", "external_label": "private-c",
	})
	changedFacts, err := ProjectGPUUtilization(validProjection(t, changedUnknown))
	if err != nil {
		t.Fatalf("project changed unknown labels: %v", err)
	}
	index := projectedIndex(forwardFacts, "42.5")
	if index < 0 {
		t.Fatal("projected physical-GPU fact is missing")
	}
	if !reflect.DeepEqual(forwardFacts[index], changedFacts[0]) {
		t.Fatalf("unknown labels changed retained identity or payload")
	}
}

func TestProjectGPUUtilizationSuccessfulEmptyAbstains(t *testing.T) {
	t.Parallel()
	facts, err := ProjectGPUUtilization(validProjection(t))
	if err != nil {
		t.Fatalf("project empty vector: %v", err)
	}
	if facts == nil || len(facts) != 0 {
		t.Fatalf("empty vector facts = %#v, want non-nil empty slice", facts)
	}
}

func TestProjectGPUUtilizationRejectsInvalidInputAtomically(t *testing.T) {
	t.Parallel()
	evaluation := fixtureEvaluationTime()
	valid := validSeries("0", "GPU-aaaaaaaa-1111-2222-3333-444444444444", "node-a", "50", nil)

	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{name: "workspace missing", mutate: func(input *Projection) { input.Workspace = "" }},
		{name: "workspace control", mutate: func(input *Projection) { input.Workspace = "local\nother" }},
		{name: "scope slash", mutate: func(input *Projection) { input.Scope = "cluster/a" }},
		{name: "wrong expression", mutate: func(input *Projection) { input.Query.Expression = "avg(DCGM_FI_DEV_GPU_UTIL)" }},
		{name: "series limit", mutate: func(input *Projection) { input.Query.Limit = 4_096 }},
		{name: "lookback override", mutate: func(input *Projection) { input.Query.LookbackDelta = time.Minute }},
		{name: "zero evaluation", mutate: func(input *Projection) { input.Query.EvaluatedAt = time.Time{} }},
		{name: "non UTC evaluation", mutate: func(input *Projection) {
			input.Query.EvaluatedAt = input.Query.EvaluatedAt.In(time.FixedZone("offset", 3600))
		}},
		{name: "zero collection", mutate: func(input *Projection) { input.CollectedAt = time.Time{} }},
		{name: "evaluation too far ahead", mutate: func(input *Projection) {
			input.Query.EvaluatedAt = input.CollectedAt.Add(maxClockSkew + time.Nanosecond)
		}},
		{name: "response missing", mutate: func(input *Projection) { input.Response = nil }},
		{name: "response oversized", mutate: func(input *Projection) { input.Response = bytes.Repeat([]byte(" "), maxResponseBytes+1) }},
		{name: "response invalid utf8", mutate: func(input *Projection) { input.Response = []byte{'{', 0xff, '}'} }},
		{name: "duplicate json", mutate: func(input *Projection) {
			input.Response = []byte(`{"status":"success","status":"success","data":{"resultType":"vector","result":[]}}`)
		}},
		{name: "trailing json", mutate: func(input *Projection) { input.Response = append(input.Response, []byte(` {}`)...) }},
		{name: "deep json", mutate: func(input *Projection) {
			input.Response = []byte(`{"status":"success","deep":` + strings.Repeat("[", maxJSONDepth+1) + `0` + strings.Repeat("]", maxJSONDepth+1) + `}`)
		}},
		{name: "malformed json", mutate: func(input *Projection) { input.Response = []byte(`{"status":`) }},
		{name: "error status", mutate: func(input *Projection) {
			input.Response = responseDocument(t, "error", "vector", []fixtureSeries{}, nil, nil, "execution", "failed")
		}},
		{name: "success with error", mutate: func(input *Projection) {
			input.Response = responseDocument(t, "success", "vector", []fixtureSeries{}, nil, nil, "execution", "failed")
		}},
		{name: "missing data", mutate: func(input *Projection) { input.Response = []byte(`{"status":"success"}`) }},
		{name: "null result", mutate: func(input *Projection) {
			input.Response = []byte(`{"status":"success","data":{"resultType":"vector","result":null}}`)
		}},
		{name: "wrong result type", mutate: func(input *Projection) {
			input.Response = responseDocument(t, "success", "matrix", []fixtureSeries{}, nil, nil, "", "")
		}},
		{name: "warning", mutate: func(input *Projection) {
			input.Response = responseDocument(t, "success", "vector", []fixtureSeries{}, []string{"partial"}, nil, "", "")
		}},
		{name: "info", mutate: func(input *Projection) {
			input.Response = responseDocument(t, "success", "vector", []fixtureSeries{}, nil, []string{"annotation"}, "", "")
		}},
		{name: "too many series", mutate: func(input *Projection) {
			series := make([]fixtureSeries, maxSeries+1)
			for index := range series {
				series[index] = validSeries(stringIndex(index), "GPU-series-"+stringIndex(index), "node-a", "1", nil)
			}
			input.Response = responseDocument(t, "success", "vector", series, nil, nil, "", "")
		}},
		{name: "labels missing", mutate: mutateOnlySeries(t, fixtureSeries{Metric: map[string]string{}, Value: valid.Value})},
		{name: "too many labels", mutate: func(input *Projection) {
			series := cloneFixtureSeries(valid)
			for index := 0; index <= maxLabelsPerSeries; index++ {
				series.Metric["extra_"+stringIndex(index)] = "value"
			}
			input.Response = responseDocument(t, "success", "vector", []fixtureSeries{series}, nil, nil, "", "")
		}},
		{name: "invalid label name", mutate: mutateSeriesLabel(t, valid, "bad-label", "value")},
		{name: "invalid label value", mutate: mutateSeriesLabel(t, valid, "job", "bad\nvalue")},
		{name: "missing metric name", mutate: deleteSeriesLabel(t, valid, "__name__")},
		{name: "wrong metric name", mutate: mutateSeriesLabel(t, valid, "__name__", "up")},
		{name: "invalid gpu", mutate: mutateSeriesLabel(t, valid, "gpu", "00")},
		{name: "invalid uuid", mutate: mutateSeriesLabel(t, valid, "UUID", "MIG-private")},
		{name: "mismatched device", mutate: mutateSeriesLabel(t, valid, "device", "nvidia1")},
		{name: "empty model", mutate: mutateSeriesLabel(t, valid, "modelName", "")},
		{name: "invalid hostname", mutate: mutateSeriesLabel(t, valid, "hostname", "node/a")},
		{name: "partial mig id", mutate: mutateSeriesLabel(t, valid, "GPU_I_ID", "0")},
		{name: "partial mig profile", mutate: mutateSeriesLabel(t, valid, "GPU_I_PROFILE", "1g.10gb")},
		{name: "invalid mig id", mutate: mutateSeriesLabels(t, valid, map[string]string{"GPU_I_ID": "01", "GPU_I_PROFILE": "1g.10gb"})},
		{name: "invalid mig profile", mutate: mutateSeriesLabels(t, valid, map[string]string{"GPU_I_ID": "0", "GPU_I_PROFILE": "1g/10gb"})},
		{name: "partial workload", mutate: mutateSeriesLabel(t, valid, "namespace", "ml")},
		{name: "invalid namespace", mutate: mutateSeriesLabels(t, valid, map[string]string{"namespace": "ML", "pod": "trainer-0", "container": "trainer"})},
		{name: "invalid pod", mutate: mutateSeriesLabels(t, valid, map[string]string{"namespace": "ml", "pod": "trainer/0", "container": "trainer"})},
		{name: "invalid container", mutate: mutateSeriesLabels(t, valid, map[string]string{"namespace": "ml", "pod": "trainer-0", "container": "Trainer"})},
		{name: "value arity", mutate: func(input *Projection) {
			series := cloneFixtureSeries(valid)
			series.Value = []any{json.Number(unixTimestampLiteral(evaluation))}
			input.Response = responseDocument(t, "success", "vector", []fixtureSeries{series}, nil, nil, "", "")
		}},
		{name: "timestamp string", mutate: mutateSeriesValue(t, valid, []any{unixTimestampLiteral(evaluation), "50"})},
		{name: "timestamp exponent", mutate: mutateSeriesValue(t, valid, []any{json.Number("1.721322e9"), "50"})},
		{name: "timestamp precision", mutate: mutateSeriesValue(t, valid, []any{json.Number("1721323200.1234567890"), "50"})},
		{name: "timestamp mismatch", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation.Add(time.Second))), "50"})},
		{name: "sample number", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation)), json.Number("50")})},
		{name: "sample nan", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation)), "NaN"})},
		{name: "sample infinity", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation)), "+Inf"})},
		{name: "sample negative", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation)), "-0"})},
		{name: "sample above range", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation)), "100.0001"})},
		{name: "sample leading zero", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation)), "01"})},
		{name: "sample exponent", mutate: mutateSeriesValue(t, valid, []any{json.Number(unixTimestampLiteral(evaluation)), "1e2"})},
		{name: "duplicate projected identity", mutate: func(input *Projection) {
			other := cloneFixtureSeries(valid)
			other.Metric["job"] = "other"
			input.Response = responseDocument(t, "success", "vector", []fixtureSeries{valid, other}, nil, nil, "", "")
		}},
		{name: "late invalid series", mutate: func(input *Projection) {
			late := cloneFixtureSeries(valid)
			delete(late.Metric, "UUID")
			input.Response = responseDocument(t, "success", "vector", []fixtureSeries{valid, late}, nil, nil, "", "")
		}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validProjection(t, valid)
			test.mutate(&input)
			facts, err := ProjectGPUUtilization(input)
			if err == nil {
				t.Fatalf("ProjectGPUUtilization accepted invalid input; facts=%#v", facts)
			}
			if facts != nil {
				t.Fatalf("ProjectGPUUtilization returned partial facts on error: %#v", facts)
			}
		})
	}
}

func TestCanonicalPercent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "0", want: "0", ok: true},
		{input: "0.000", want: "0", ok: true},
		{input: "7.5000", want: "7.5", ok: true},
		{input: "99.999999999999999999", want: "99.999999999999999999", ok: true},
		{input: "100.000", want: "100", ok: true},
		{input: "", ok: false},
		{input: ".5", ok: false},
		{input: "5.", ok: false},
		{input: "00", ok: false},
		{input: "-1", ok: false},
		{input: "100.1", ok: false},
		{input: "101", ok: false},
		{input: "NaN", ok: false},
		{input: "1e2", ok: false},
	} {
		got, err := canonicalPercent(test.input)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("canonicalPercent(%q) = %q, %v; want %q, ok=%v", test.input, got, err, test.want, test.ok)
		}
	}
}

func FuzzProjectGPUUtilization(f *testing.F) {
	input := validProjection(f, validSeries("0", "GPU-aaaaaaaa-1111-2222-3333-444444444444", "node-a", "50", nil))
	f.Add(input.Response)
	f.Add(responseDocument(f, "success", "vector", []fixtureSeries{}, nil, nil, "", ""))
	f.Add([]byte(`{"status":"success","data":{"resultType":"vector","result":null}}`))
	f.Add([]byte(`{"status":"success","status":"error"}`))
	f.Fuzz(func(t *testing.T, response []byte) {
		projection := input
		projection.Response = append([]byte(nil), response...)
		facts, err := ProjectGPUUtilization(projection)
		if err != nil {
			if facts != nil {
				t.Fatalf("error returned with partial facts: %#v", facts)
			}
			return
		}
		if len(facts) > maxSeries {
			t.Fatalf("successful projection exceeded series bound: %d", len(facts))
		}
		for index, fact := range facts {
			if err := fact.Validate(projection.Workspace); err != nil {
				t.Fatalf("fact %d invalid: %v", index, err)
			}
			if !fact.Fact.ObservedAt.Equal(projection.Query.EvaluatedAt) {
				t.Fatalf("fact %d changed observation time", index)
			}
			var observation map[string]any
			if err := json.Unmarshal(fact.Fact.Observed, &observation); err != nil {
				t.Fatalf("fact %d payload invalid: %v", index, err)
			}
			for _, forbidden := range []string{"UUID", "uuid", "hostname", "job", "instance", "pci_bus_id"} {
				if _, exists := observation[forbidden]; exists {
					t.Fatalf("fact %d retained forbidden field %q", index, forbidden)
				}
			}
		}
	})
}

func validProjection(t testing.TB, series ...fixtureSeries) Projection {
	t.Helper()
	evaluation := fixtureEvaluationTime()
	return Projection{
		Workspace: "local", Scope: "cluster-a",
		Query:       InstantQuery{Expression: GPUUtilizationMetric, EvaluatedAt: evaluation},
		CollectedAt: evaluation.Add(2 * time.Second),
		Response:    responseDocument(t, "success", "vector", series, nil, nil, "", ""),
	}
}

func validSeries(gpu, uuid, hostname, percent string, extra map[string]string) fixtureSeries {
	labels := map[string]string{
		"__name__": GPUUtilizationMetric,
		"gpu":      gpu, "UUID": uuid, "device": "nvidia" + gpu,
		"modelName": "NVIDIA H100 80GB HBM3", "hostname": hostname,
		"pci_bus_id": "00000000:01:00.0",
	}
	for name, value := range extra {
		labels[name] = value
	}
	return fixtureSeries{
		Metric: labels,
		Value:  []any{json.Number(unixTimestampLiteral(fixtureEvaluationTime())), percent},
	}
}

func responseDocument(t testing.TB, status, resultType string, series []fixtureSeries, warnings, infos []string, errorType, sourceError string) []byte {
	t.Helper()
	if series == nil {
		series = make([]fixtureSeries, 0)
	}
	document := map[string]any{
		"status": status,
		"data":   map[string]any{"resultType": resultType, "result": series},
	}
	if warnings != nil {
		document["warnings"] = warnings
	}
	if infos != nil {
		document["infos"] = infos
	}
	if errorType != "" {
		document["errorType"] = errorType
	}
	if sourceError != "" {
		document["error"] = sourceError
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return encoded
}

func fixtureEvaluationTime() time.Time {
	return time.Date(2026, 7, 18, 20, 0, 0, 123456000, time.UTC)
}

func unixTimestampLiteral(value time.Time) string {
	seconds := value.Unix()
	nanoseconds := value.Nanosecond()
	if nanoseconds == 0 {
		return stringIndex64(seconds)
	}
	fraction := strings.TrimRight(stringIndex64(int64(nanoseconds) + 1_000_000_000)[1:], "0")
	return stringIndex64(seconds) + "." + fraction
}

func stringIndex(value int) string { return stringIndex64(int64(value)) }

func stringIndex64(value int64) string { return strconv.FormatInt(value, 10) }

func cloneFixtureSeries(source fixtureSeries) fixtureSeries {
	clone := fixtureSeries{Metric: make(map[string]string, len(source.Metric)), Value: append([]any(nil), source.Value...)}
	for name, value := range source.Metric {
		clone.Metric[name] = value
	}
	return clone
}

func mutateOnlySeries(t testing.TB, series fixtureSeries) func(*Projection) {
	return func(input *Projection) {
		input.Response = responseDocument(t, "success", "vector", []fixtureSeries{series}, nil, nil, "", "")
	}
}

func mutateSeriesLabel(t testing.TB, source fixtureSeries, name, value string) func(*Projection) {
	return mutateSeriesLabels(t, source, map[string]string{name: value})
}

func mutateSeriesLabels(t testing.TB, source fixtureSeries, labels map[string]string) func(*Projection) {
	series := cloneFixtureSeries(source)
	for name, value := range labels {
		series.Metric[name] = value
	}
	return mutateOnlySeries(t, series)
}

func deleteSeriesLabel(t testing.TB, source fixtureSeries, name string) func(*Projection) {
	series := cloneFixtureSeries(source)
	delete(series.Metric, name)
	return mutateOnlySeries(t, series)
}

func mutateSeriesValue(t testing.TB, source fixtureSeries, value []any) func(*Projection) {
	series := cloneFixtureSeries(source)
	series.Value = value
	return mutateOnlySeries(t, series)
}

func projectedIndex(facts []fleet.GraphFact, percent string) int {
	for index, fact := range facts {
		var observation gpuUtilizationObservation
		if json.Unmarshal(fact.Fact.Observed, &observation) == nil && observation.UtilizationPercent == percent {
			return index
		}
	}
	return -1
}

func assertObservation(t testing.TB, got, want gpuUtilizationObservation) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("observation = %#v, want %#v", got, want)
	}
}
