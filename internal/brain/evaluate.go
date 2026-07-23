// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ArdurAI/sith/internal/fleet"
)

// Evaluate deterministically ranks rule matches and never performs I/O or mutation.
func Evaluate(input Investigation) (Result, error) {
	if err := input.Validate(); err != nil {
		return Result{}, err
	}
	byEntity := make(map[string][]Observation)
	for _, observation := range input.Observations {
		byEntity[entityKey(observation.Ref)] = append(byEntity[entityKey(observation.Ref)], observation)
	}
	verdicts := make([]Verdict, 0)
	for _, observations := range byEntity {
		entityVerdicts := make([]Verdict, 0)
		for _, candidate := range catalog {
			verdict, matched := evaluateRule(candidate, observations, input.Coverage)
			if matched {
				entityVerdicts = append(entityVerdicts, verdict)
			}
		}
		verdicts = append(verdicts, compose(entityVerdicts)...)
	}
	verdicts = append(correlateFleet(verdicts, byEntity), verdicts...)
	sortVerdicts(verdicts)
	return Result{Workspace: input.Workspace, Verdicts: verdicts}, nil
}

func compose(verdicts []Verdict) []Verdict {
	indexes := make(map[RuleID]int, len(verdicts))
	for index, verdict := range verdicts {
		indexes[verdict.Rule] = index
	}
	badDeploy, hasBadDeploy := indexes[RuleBadDeploy]
	_, hasCrashLoop := indexes[RuleCrashLoop]
	if hasBadDeploy && hasCrashLoop {
		verdicts[badDeploy].CauseOf = []RuleID{RuleCrashLoop}
		verdicts[badDeploy].Score += 100
		verdicts[badDeploy].Hypothesis += "; it is the likely root cause of the CrashLoopBackOff"
	}
	return verdicts
}

func evaluateRule(candidate rule, observations []Observation, coverage map[fleet.Lens]LensCoverage) (Verdict, bool) {
	observations = candidate.applicableObservations(observations)
	trigger, ok := match(candidate.trigger, observations, candidate.exactTrigger)
	if !ok {
		return Verdict{}, false
	}
	ref := trigger.Ref
	verdict := Verdict{
		Rule: candidate.id, FailureMode: candidate.failureMode, Status: StatusConfirmed,
		Hypothesis: candidate.rootCause, Scope: ref.Scope, Ref: ref, Advisory: renderAdvisory(candidate.advisory, ref),
		RemediationCandidate: remediationCandidateFor(candidate.remediation),
	}
	verdict.Citations = append(verdict.Citations, citation(candidate.trigger, trigger))
	verdict.Score += candidate.trigger.weight
	for _, signal := range candidate.signals {
		if observation, found := match(signal, observations, false); found {
			verdict.Citations = append(verdict.Citations, citation(signal, observation))
			verdict.Score += signal.weight
		}
	}
	for _, lens := range candidate.required {
		if unavailable(lens, coverage, observations) {
			verdict.MissingLenses = append(verdict.MissingLenses, lens)
		}
	}
	if len(verdict.MissingLenses) > 0 {
		verdict.Status = StatusUnconfirmed
	} else {
		for _, lens := range candidate.strengthener {
			if unavailable(lens, coverage, observations) {
				verdict.MissingLenses = append(verdict.MissingLenses, lens)
				verdict.Status = StatusDetected
			}
		}
	}
	sortCitations(verdict.Citations)
	return verdict, true
}

func (candidate rule) applicableObservations(observations []Observation) []Observation {
	if candidate.sourceKind == "" && candidate.resourceKind == "" {
		return observations
	}
	filtered := make([]Observation, 0, len(observations))
	for _, observation := range observations {
		if candidate.appliesTo(observation) {
			filtered = append(filtered, observation)
		}
	}
	return filtered
}

func (candidate rule) appliesTo(observation Observation) bool {
	if candidate.sourceKind != "" && observation.Ref.SourceKind != candidate.sourceKind {
		return false
	}
	return candidate.resourceKind == "" || observation.Ref.Kind == candidate.resourceKind
}

func match(predicate predicate, observations []Observation, exact bool) (Observation, bool) {
	var stale Observation
	staleMatched := false
	for _, observation := range observations {
		if observation.Lens != predicate.lens || observation.Key != predicate.key {
			continue
		}
		if len(predicate.values) == 0 {
			if !observation.Stale {
				return observation, true
			}
			stale, staleMatched = observation, true
			continue
		}
		for _, expected := range predicate.values {
			if observation.Value == expected || (!exact && strings.EqualFold(observation.Value, expected)) {
				if !observation.Stale {
					return observation, true
				}
				stale, staleMatched = observation, true
			}
		}
	}
	return stale, staleMatched
}

func unavailable(lens fleet.Lens, coverage map[fleet.Lens]LensCoverage, observations []Observation) bool {
	state, declared := coverage[lens]
	if !declared || !state.Available || state.Stale {
		return true
	}
	for _, observation := range observations {
		if observation.Lens == lens && !observation.Stale {
			return false
		}
	}
	return true
}

func citation(signal predicate, observation Observation) Citation {
	return Citation{Ref: observation.Ref, Lens: observation.Lens, Predicate: signal.key, Observed: observation.Value,
		Weight: signal.weight, ObservedAt: observation.ObservedAt, Source: observation.Source, Stale: observation.Stale}
}

func renderAdvisory(template Advisory, ref fleet.ResourceRef) Advisory {
	replacer := strings.NewReplacer(
		"{kind}", strings.ToLower(ref.Kind),
		"{name}", shellQuote(ref.Name),
		"{namespace}", shellQuote(ref.Namespace),
		"{context}", shellQuote(ref.Scope),
	)
	template.Command = replacer.Replace(template.Command)
	template.PRDiff = replacer.Replace(template.PRDiff)
	return template
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func correlateFleet(verdicts []Verdict, observations map[string][]Observation) []Verdict {
	type groupKey struct {
		rule   RuleID
		digest string
	}
	type groupMember struct {
		verdict Verdict
		digest  Observation
	}
	groups := make(map[groupKey]map[string]groupMember)
	for _, verdict := range verdicts {
		if !supportsFleetCorrelation(verdict.Rule) {
			continue
		}
		entity := entityKey(verdict.Ref)
		for _, observation := range observations[entity] {
			if observation.Key != fleet.OTelContainerImageRepoDigests || observation.Stale {
				continue
			}
			digest, err := fleet.ImageDigestFromRepoDigest(observation.Value)
			if err != nil {
				continue
			}
			key := groupKey{verdict.Rule, digest}
			if groups[key] == nil {
				groups[key] = make(map[string]groupMember)
			}
			current, exists := groups[key][entity]
			if !exists || preferCorrelationObservation(observation, current.digest) {
				groups[key][entity] = groupMember{verdict: verdict, digest: observation}
			}
		}
	}
	result := make([]Verdict, 0)
	for key, members := range groups {
		group := make([]Verdict, 0, len(members))
		for _, member := range members {
			group = append(group, member.verdict)
		}
		sortVerdicts(group)
		clusters := uniqueClusters(group)
		if len(clusters) < 2 {
			continue
		}
		correlated := group[0]
		correlated.RemediationCandidate = cloneRemediationCandidate(correlated.RemediationCandidate)
		correlated.FleetWide = true
		correlated.Scope = "fleet"
		correlated.Clusters = clusters
		correlated.Score += 1000 + len(clusters)
		correlated.Hypothesis = fmt.Sprintf("%s across %d clusters running image %s", correlated.Hypothesis, len(clusters), key.digest)
		correlated.Citations = nil
		for _, verdict := range group {
			correlated.Citations = append(correlated.Citations, verdict.Citations...)
			correlated.Citations = append(correlated.Citations, evidenceCitation(members[entityKey(verdict.Ref)].digest))
			correlated.MissingLenses = unionLenses(correlated.MissingLenses, verdict.MissingLenses)
			if confidenceRank(verdict.Status) < confidenceRank(correlated.Status) {
				correlated.Status = verdict.Status
			}
		}
		sortCitations(correlated.Citations)
		result = append(result, correlated)
	}
	return result
}

func supportsFleetCorrelation(ruleID RuleID) bool {
	switch ruleID {
	case RuleBadDeploy, RuleOOMKilled, RuleCrashLoop, RuleConfigDrift, RuleCertExpiry, RuleNodePressure:
		return true
	default:
		return false
	}
}

func preferCorrelationObservation(candidate, current Observation) bool {
	if !candidate.ObservedAt.Equal(current.ObservedAt) {
		return candidate.ObservedAt.After(current.ObservedAt)
	}
	if candidate.Source != current.Source {
		return candidate.Source < current.Source
	}
	if candidate.Value != current.Value {
		return candidate.Value < current.Value
	}
	if candidateRef, currentRef := correlationRefKey(candidate.Ref), correlationRefKey(current.Ref); candidateRef != currentRef {
		return candidateRef < currentRef
	}
	return candidate.Lens < current.Lens
}

func correlationRefKey(ref fleet.ResourceRef) string {
	parts := []string{ref.SourceKind, ref.Scope, ref.Kind, ref.Namespace, ref.Name}
	keys := make([]string, 0, len(ref.Attributes))
	for key := range ref.Attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key, ref.Attributes[key])
	}
	return strings.Join(parts, "\x00")
}

func evidenceCitation(observation Observation) Citation {
	return Citation{
		Ref: observation.Ref, Lens: observation.Lens, Predicate: observation.Key, Observed: observation.Value,
		ObservedAt: observation.ObservedAt, Source: observation.Source, Stale: observation.Stale,
	}
}

func sortCitations(citations []Citation) {
	sort.Slice(citations, func(left, right int) bool {
		if citations[left].Weight != citations[right].Weight {
			return citations[left].Weight > citations[right].Weight
		}
		if citations[left].Ref.String() != citations[right].Ref.String() {
			return citations[left].Ref.String() < citations[right].Ref.String()
		}
		if citations[left].Lens != citations[right].Lens {
			return citations[left].Lens < citations[right].Lens
		}
		if citations[left].Predicate != citations[right].Predicate {
			return citations[left].Predicate < citations[right].Predicate
		}
		if citations[left].Observed != citations[right].Observed {
			return citations[left].Observed < citations[right].Observed
		}
		if !citations[left].ObservedAt.Equal(citations[right].ObservedAt) {
			return citations[left].ObservedAt.Before(citations[right].ObservedAt)
		}
		if citations[left].Source != citations[right].Source {
			return citations[left].Source < citations[right].Source
		}
		return !citations[left].Stale && citations[right].Stale
	})
}

func unionLenses(left, right []fleet.Lens) []fleet.Lens {
	set := make(map[fleet.Lens]struct{}, len(left)+len(right))
	for _, lens := range append(append([]fleet.Lens(nil), left...), right...) {
		set[lens] = struct{}{}
	}
	result := make([]fleet.Lens, 0, len(set))
	for lens := range set {
		result = append(result, lens)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func confidenceRank(status Status) int {
	switch status {
	case StatusConfirmed:
		return 3
	case StatusDetected:
		return 2
	default:
		return 1
	}
}

func uniqueClusters(verdicts []Verdict) []string {
	set := make(map[string]struct{})
	for _, verdict := range verdicts {
		set[verdict.Ref.Scope] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for cluster := range set {
		result = append(result, cluster)
	}
	sort.Strings(result)
	return result
}

func entityKey(ref fleet.ResourceRef) string {
	return strings.Join([]string{ref.Scope, ref.Namespace, ref.Kind, ref.Name}, "\x00")
}
