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
	trigger, ok := match(candidate.trigger, observations)
	if !ok {
		return Verdict{}, false
	}
	ref := trigger.Ref
	verdict := Verdict{
		Rule: candidate.id, FailureMode: candidate.failureMode, Status: StatusConfirmed,
		Hypothesis: candidate.rootCause, Scope: ref.Scope, Ref: ref, Advisory: renderAdvisory(candidate.advisory, ref),
	}
	verdict.Citations = append(verdict.Citations, citation(candidate.trigger, trigger))
	verdict.Score += candidate.trigger.weight
	for _, signal := range candidate.signals {
		if observation, found := match(signal, observations); found {
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
	sort.Slice(verdict.Citations, func(i, j int) bool { return verdict.Citations[i].Weight > verdict.Citations[j].Weight })
	return verdict, true
}

func match(predicate predicate, observations []Observation) (Observation, bool) {
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
			if strings.EqualFold(strings.TrimSpace(observation.Value), expected) {
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
	if declared && (!state.Available || state.Stale) {
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
	groups := make(map[groupKey][]Verdict)
	for _, verdict := range verdicts {
		for _, observation := range observations[entityKey(verdict.Ref)] {
			if observation.Key == "container.image.digest" && strings.HasPrefix(observation.Value, "sha256:") {
				key := groupKey{verdict.Rule, observation.Value}
				groups[key] = append(groups[key], verdict)
			}
		}
	}
	result := make([]Verdict, 0)
	for key, group := range groups {
		clusters := uniqueClusters(group)
		if len(clusters) < 2 {
			continue
		}
		correlated := group[0]
		correlated.FleetWide = true
		correlated.Scope = "fleet"
		correlated.Clusters = clusters
		correlated.Score += 1000 + len(clusters)
		correlated.Hypothesis = fmt.Sprintf("%s across %d clusters running image %s", correlated.Hypothesis, len(clusters), key.digest)
		correlated.Citations = nil
		for _, verdict := range group {
			correlated.Citations = append(correlated.Citations, verdict.Citations...)
			correlated.MissingLenses = unionLenses(correlated.MissingLenses, verdict.MissingLenses)
			if confidenceRank(verdict.Status) < confidenceRank(correlated.Status) {
				correlated.Status = verdict.Status
			}
		}
		result = append(result, correlated)
	}
	return result
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
