// SPDX-License-Identifier: Apache-2.0

package brain

import "github.com/ArdurAI/sith/internal/fleet"

type predicate struct {
	lens   fleet.Lens
	key    string
	values []string
	weight int
}

type rule struct {
	id           RuleID
	failureMode  string
	rootCause    string
	sourceKind   string
	resourceKind string
	exactTrigger bool
	trigger      predicate
	signals      []predicate
	required     []fleet.Lens
	strengthener []fleet.Lens
	advisory     Advisory
}

var catalog = []rule{
	{
		id: RuleBadDeploy, failureMode: "bad deploy", rootCause: "a recent workload change introduced a regression",
		trigger:  predicate{fleet.LensLive, "workload.status", []string{"degraded", "progressing"}, 2},
		signals:  []predicate{{fleet.LensTimeline, "change.kind", []string{"deploy", "rollout", "argocd-sync", "image-change"}, 3}, {fleet.LensDesired, "desired.changed", []string{"true"}, 1}},
		required: []fleet.Lens{fleet.LensLive, fleet.LensTimeline}, strengthener: []fleet.Lens{fleet.LensDesired, fleet.LensTelemetry},
		advisory: Advisory{Command: "kubectl --context {context} rollout undo {kind}/{name} -n {namespace}"},
	},
	{
		id: RuleOOMKilled, failureMode: "OOMKilled", rootCause: "the container was terminated for exceeding available memory",
		trigger:  predicate{fleet.LensLive, "pod.reason", []string{"oomkilled"}, 3},
		signals:  []predicate{{fleet.LensLive, "pod.restarts", nil, 1}, {fleet.LensTelemetry, "memory.variant", nil, 2}, {fleet.LensTimeline, "change.kind", nil, 1}},
		required: []fleet.Lens{fleet.LensLive}, strengthener: []fleet.Lens{fleet.LensTelemetry},
		advisory: Advisory{PRDiff: "increase spec.template.spec.containers[].resources.limits.memory after validating measured usage"},
	},
	{
		id: RuleCrashLoop, failureMode: "CrashLoopBackOff", rootCause: "the container repeatedly exits and Kubernetes is backing off restarts",
		trigger:  predicate{fleet.LensLive, "pod.failure", []string{"crashloopbackoff", "repeated-error"}, 3},
		signals:  []predicate{{fleet.LensLive, "pod.restarts", nil, 1}, {fleet.LensTelemetry, "logs.cause", nil, 3}, {fleet.LensTimeline, "change.kind", nil, 2}},
		required: []fleet.Lens{fleet.LensLive}, strengthener: []fleet.Lens{fleet.LensTelemetry},
		advisory: Advisory{Command: "kubectl --context {context} logs {name} -n {namespace} --previous"},
	},
	{
		id: RuleConfigDrift, failureMode: "config drift", rootCause: "live state differs from the declared desired state",
		trigger:  predicate{fleet.LensDesired, "desired.drift", []string{"true", "outofsync"}, 3},
		signals:  []predicate{{fleet.LensTimeline, "change.kind", []string{"kubectl-edit", "kubectl-patch", "sync-failed"}, 2}},
		required: []fleet.Lens{fleet.LensLive, fleet.LensDesired}, strengthener: []fleet.Lens{fleet.LensTimeline},
		advisory: Advisory{PRDiff: "reconcile the cited field delta in Git, or revert the out-of-band live mutation"},
	},
	{
		id: RuleCertExpiry, failureMode: "certificate expiry", rootCause: "a certificate is expired or inside the renewal safety window",
		trigger: predicate{fleet.LensLive, "certificate.expiry", []string{"expired", "lt-7d"}, 3},
		signals: []predicate{{fleet.LensTelemetry, "certificate.renewal", nil, 2}}, required: []fleet.Lens{fleet.LensLive},
		strengthener: []fleet.Lens{fleet.LensTelemetry}, advisory: Advisory{Command: "inspect the issuer and renewal controller for {name} in {namespace}", Sensitive: true},
	},
	{
		id: RuleNodePressure, failureMode: "node pressure", rootCause: "node capacity or readiness is disrupting scheduled workloads",
		trigger:  predicate{fleet.LensLive, "node.condition", []string{"memorypressure", "diskpressure", "pidpressure", "notready"}, 3},
		signals:  []predicate{{fleet.LensLive, "event.reason", []string{"evicted", "failedscheduling"}, 2}, {fleet.LensTelemetry, "node.saturation", nil, 2}},
		required: []fleet.Lens{fleet.LensLive}, strengthener: []fleet.Lens{fleet.LensTelemetry},
		advisory: Advisory{Command: "inspect capacity and autoscaler state for node {name} before any cordon or drain", Sensitive: true},
	},
	{
		id: RuleImagePull, failureMode: "image pull failure", rootCause: "Kubernetes reports that it cannot pull a Pod image; the waiting reason does not identify the underlying registry, reference, network, rate-limit, or platform cause",
		trigger:  predicate{fleet.LensLive, "pod.reason", []string{"imagepullbackoff", "errimagepull"}, 3},
		required: []fleet.Lens{fleet.LensLive},
		advisory: Advisory{Command: "kubectl --context {context} describe pod {name} -n {namespace}", Sensitive: true},
	},
	{
		id: RuleArgoSyncFail, failureMode: "Argo CD sync failure", rootCause: "Argo CD reports that the Application sync operation failed; the normalized operation phase does not identify whether the underlying cause is rendering, validation, authorization, a hook, network access, the Kubernetes API, a resource, or another failure",
		sourceKind: argoGraphSource, resourceKind: "Application",
		exactTrigger: true,
		trigger:      predicate{fleet.LensTimeline, "change.kind", []string{"sync-failed"}, 3},
		required:     []fleet.Lens{fleet.LensTimeline},
		advisory:     Advisory{Command: "kubectl --context {context} describe application.argoproj.io {name} -n {namespace}", Sensitive: true},
	},
}
