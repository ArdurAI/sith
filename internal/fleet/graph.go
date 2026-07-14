// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	maxGraphFacts = 10_000

	// OTelK8sClusterName is the stable OpenTelemetry cluster identity attribute.
	OTelK8sClusterName = "k8s.cluster.name"
	// OTelK8sNamespaceName is the stable OpenTelemetry namespace identity attribute.
	OTelK8sNamespaceName = "k8s.namespace.name"
	// OTelK8sPodName is the stable OpenTelemetry Pod identity attribute.
	OTelK8sPodName = "k8s.pod.name"
	// OTelK8sNodeName is the stable OpenTelemetry node identity attribute.
	OTelK8sNodeName = "k8s.node.name"
	// OTelContainerImageRepoDigests is the stable OpenTelemetry source attribute for OCI repo digests.
	OTelContainerImageRepoDigests = "container.image.repo_digests"
)

// Valid reports whether a lens belongs to the closed operational graph taxonomy.
func (lens Lens) Valid() bool {
	switch lens {
	case LensLive, LensDesired, LensTimeline, LensTelemetry:
		return true
	default:
		return false
	}
}

// Allows reports whether a fact kind can belong to this lens without changing its meaning.
func (lens Lens) Allows(kind FactKind) bool {
	switch lens {
	case LensLive:
		return kind == FactInventory || kind == FactHealth
	case LensDesired:
		return kind == FactDesired || kind == FactDrift
	case LensTimeline:
		return kind == FactChange
	case LensTelemetry:
		return kind == FactAlert || kind == FactCVE || kind == FactCost || kind == FactDerived
	default:
		return false
	}
}

// EntityRef is a validated correlation address. Cluster plus namespace is always the default
// boundary. ImageDigest is a normalized sha256 digest parsed from OTelContainerImageRepoDigests.
type EntityRef struct {
	Cluster     string `json:"k8s.cluster.name,omitempty"`
	Namespace   string `json:"k8s.namespace.name,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Name        string `json:"name,omitempty"`
	Pod         string `json:"k8s.pod.name,omitempty"`
	Node        string `json:"k8s.node.name,omitempty"`
	ImageDigest string `json:"image_digest,omitempty"`
}

// Validate rejects ambiguous identifiers before they can become a graph join key.
func (ref EntityRef) Validate() error {
	if ref.isGlobalImage() {
		return validateImageDigest(ref.ImageDigest)
	}
	if err := validateEntityText("cluster", ref.Cluster); err != nil {
		return err
	}
	for label, value := range map[string]string{
		"namespace": ref.Namespace, "kind": ref.Kind, "name": ref.Name, "pod": ref.Pod, "node": ref.Node,
	} {
		if value != "" {
			if err := validateEntityText(label, value); err != nil {
				return err
			}
		}
	}
	if (ref.Kind == "") != (ref.Name == "") {
		return fmt.Errorf("entity kind and name must be specified together")
	}
	if ref.Kind == "" && ref.Pod == "" && ref.Node == "" && ref.ImageDigest == "" {
		return nil // a cluster node is a valid local root.
	}
	if ref.ImageDigest != "" {
		return validateImageDigest(ref.ImageDigest)
	}
	return nil
}

// Key returns the deterministic local identity key. The only global key is a standalone image
// digest; a workload that happens to use that digest remains scoped to its cluster and namespace.
// Every populated local dimension is represented so a partial or more-specific reference cannot
// silently coalesce with a different entity.
func (ref EntityRef) Key() string {
	if ref.isGlobalImage() {
		return "image/" + ref.ImageDigest
	}
	parts := []string{"cluster", ref.Cluster}
	if ref.Namespace != "" {
		parts = append(parts, "namespace", ref.Namespace)
	}
	if ref.Kind != "" {
		parts = append(parts, strings.ToLower(ref.Kind), ref.Name)
	}
	if ref.Pod != "" {
		parts = append(parts, "pod", ref.Pod)
	}
	if ref.Node != "" {
		parts = append(parts, "node", ref.Node)
	}
	if ref.ImageDigest != "" {
		parts = append(parts, "image", ref.ImageDigest)
	}
	return strings.Join(parts, "/")
}

func (ref EntityRef) isGlobalImage() bool {
	return ref.Cluster == "" && ref.Namespace == "" && ref.Kind == "" && ref.Name == "" && ref.Pod == "" && ref.Node == "" && ref.ImageDigest != ""
}

// ImageDigestFromRepoDigest extracts one canonical sha256 digest from a stable OTel
// OTelContainerImageRepoDigests value. Tags, image IDs, and malformed digests are rejected.
func ImageDigestFromRepoDigest(repoDigest string) (string, error) {
	repo, digest, ok := strings.Cut(repoDigest, "@")
	if !ok || !validRepository(repo) || strings.Contains(repo, "@") {
		return "", fmt.Errorf("image repo digest must contain one repository and digest")
	}
	if err := validateImageDigest(digest); err != nil {
		return "", err
	}
	return digest, nil
}

// ImageDigestFromRuntimeImageID extracts one immutable digest from a Kubernetes runtime-resolved
// ContainerStatus.ImageID. It accepts an exact digest, an optional runtime scheme, or a
// repository@digest form, and deliberately rejects mutable image references.
func ImageDigestFromRuntimeImageID(imageID string) (string, error) {
	if imageID == "" || strings.TrimSpace(imageID) != imageID {
		return "", fmt.Errorf("runtime image ID is required")
	}
	value := imageID
	if runtimeName, remainder, found := strings.Cut(value, "://"); found {
		if !validRuntimeScheme(runtimeName) || remainder == "" {
			return "", fmt.Errorf("runtime image ID has an invalid runtime scheme")
		}
		value = remainder
	}
	if strings.Contains(value, "@") {
		return ImageDigestFromRepoDigest(value)
	}
	if err := ValidateImageDigest(value); err != nil {
		return "", fmt.Errorf("runtime image ID: %w", err)
	}
	return value, nil
}

func validRepository(repo string) bool {
	if repo == "" || strings.TrimSpace(repo) != repo || len(repo) > 255 || strings.ContainsAny(repo, "\x00\r\n@") {
		return false
	}
	lastPathSeparator := strings.LastIndex(repo, "/")
	return !strings.Contains(repo[lastPathSeparator+1:], ":")
}

// ValidateImageDigest rejects every image reference other than one lowercase immutable sha256 digest.
func ValidateImageDigest(digest string) error {
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("image digest must be one immutable sha256 digest")
	}
	for _, runeValue := range digest[len("sha256:"):] {
		if (runeValue < '0' || runeValue > '9') && (runeValue < 'a' || runeValue > 'f') {
			return fmt.Errorf("image digest must use lowercase hexadecimal")
		}
	}
	return nil
}

func validateImageDigest(digest string) error { return ValidateImageDigest(digest) }

func validRuntimeScheme(value string) bool {
	switch value {
	case "containerd", "docker-pullable", "cri-o", "docker":
		return true
	default:
		return false
	}
}

func validateEntityText(label, value string) error {
	if trimmed := strings.TrimSpace(value); trimmed == "" || trimmed != value || len(value) > 253 || strings.ContainsAny(value, "/\x00\r\n") {
		return fmt.Errorf("entity %s is invalid", label)
	}
	return nil
}

// GraphFact is one lens-stamped observation. Entity is nil only when the producer cannot safely
// resolve the identity; NewGraph preserves such facts in Graph.Unattached instead of guessing.
type GraphFact struct {
	Fact   Fact       `json:"fact"`
	Lens   Lens       `json:"lens"`
	Entity *EntityRef `json:"entity,omitempty"`
}

// Validate ensures a graph fact cannot cross its source cluster or namespace boundary.
func (fact GraphFact) Validate(workspace string) error {
	if strings.TrimSpace(workspace) == "" || fact.Fact.Workspace != workspace {
		return fmt.Errorf("graph fact must belong to the requested workspace")
	}
	if !fact.Fact.Kind.Valid() || !fact.Lens.Valid() || !fact.Lens.Allows(fact.Fact.Kind) {
		return fmt.Errorf("graph fact has an invalid kind or lens")
	}
	for label, value := range map[string]string{
		"source kind":   fact.Fact.Ref.SourceKind,
		"source scope":  fact.Fact.Ref.Scope,
		"resource kind": fact.Fact.Ref.Kind,
		"resource name": fact.Fact.Ref.Name,
	} {
		if err := validateEntityText(label, value); err != nil {
			return err
		}
	}
	if fact.Fact.Ref.Namespace != "" {
		if err := validateEntityText("resource namespace", fact.Fact.Ref.Namespace); err != nil {
			return err
		}
	}
	if fact.Fact.ObservedAt.IsZero() {
		return fmt.Errorf("graph fact observation time is required")
	}
	if fact.Entity == nil {
		return nil
	}
	if err := fact.Entity.Validate(); err != nil {
		return err
	}
	if !fact.Entity.isGlobalImage() && fact.Entity.Cluster != fact.Fact.Ref.Scope {
		return fmt.Errorf("entity cluster does not match fact source scope")
	}
	if fact.Entity.Namespace != "" && fact.Fact.Ref.Namespace != "" && fact.Entity.Namespace != fact.Fact.Ref.Namespace {
		return fmt.Errorf("entity namespace does not match fact source namespace")
	}
	return nil
}

// ImageCorrelation is an explicit fleet-wide relationship between local entities sharing one
// immutable digest. It is never inferred from a name, tag, or provider-specific attribute.
type ImageCorrelation struct {
	Digest   string      `json:"digest"`
	Entities []EntityRef `json:"entities"`
}

// NewGraph assembles a deterministic, workspace-bounded graph. It never joins unattached facts.
func NewGraph(workspace string, facts []GraphFact) (Graph, error) {
	if strings.TrimSpace(workspace) == "" {
		return Graph{}, fmt.Errorf("graph workspace is required")
	}
	if len(facts) > maxGraphFacts {
		return Graph{}, fmt.Errorf("graph fact limit exceeds %d", maxGraphFacts)
	}
	nodes := make(map[string]*Node, len(facts))
	unattached := make([]GraphFact, 0)
	for index, fact := range facts {
		if err := fact.Validate(workspace); err != nil {
			return Graph{}, fmt.Errorf("graph fact %d: %w", index, err)
		}
		cloned := cloneGraphFact(fact)
		if cloned.Entity == nil {
			unattached = append(unattached, cloned)
			continue
		}
		key := cloned.Entity.Key()
		node := nodes[key]
		if node == nil {
			node = &Node{Entity: *cloned.Entity}
			nodes[key] = node
		}
		node.Facts = append(node.Facts, cloned)
	}
	graph := Graph{Workspace: workspace, Nodes: make([]Node, 0, len(nodes)), Unattached: unattached, Edges: []Edge{}}
	for _, node := range nodes {
		sort.Slice(node.Facts, func(left, right int) bool { return graphFactKey(node.Facts[left]) < graphFactKey(node.Facts[right]) })
		graph.Nodes = append(graph.Nodes, *node)
	}
	sort.Slice(graph.Nodes, func(left, right int) bool { return graph.Nodes[left].Entity.Key() < graph.Nodes[right].Entity.Key() })
	sort.Slice(graph.Unattached, func(left, right int) bool {
		return graphFactKey(graph.Unattached[left]) < graphFactKey(graph.Unattached[right])
	})
	return graph, nil
}

// ImageCorrelations returns only exact-digest correlations spanning at least two local entities.
func (graph Graph) ImageCorrelations() []ImageCorrelation {
	byDigest := make(map[string]map[string]EntityRef)
	for _, node := range graph.Nodes {
		if node.Entity.ImageDigest == "" || node.Entity.isGlobalImage() {
			continue
		}
		if byDigest[node.Entity.ImageDigest] == nil {
			byDigest[node.Entity.ImageDigest] = make(map[string]EntityRef)
		}
		byDigest[node.Entity.ImageDigest][node.Entity.Key()] = node.Entity
	}
	result := make([]ImageCorrelation, 0, len(byDigest))
	for digest, entities := range byDigest {
		if len(entities) < 2 {
			continue
		}
		correlation := ImageCorrelation{Digest: digest, Entities: make([]EntityRef, 0, len(entities))}
		for _, entity := range entities {
			correlation.Entities = append(correlation.Entities, entity)
		}
		sort.Slice(correlation.Entities, func(left, right int) bool {
			return correlation.Entities[left].Key() < correlation.Entities[right].Key()
		})
		result = append(result, correlation)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Digest < result[right].Digest })
	return result
}

func cloneGraphFact(fact GraphFact) GraphFact {
	cloned := fact
	cloned.Fact.Observed = append([]byte(nil), fact.Fact.Observed...)
	cloned.Fact.Display = append([]DisplayField(nil), fact.Fact.Display...)
	if fact.Fact.Ref.Attributes != nil {
		cloned.Fact.Ref.Attributes = make(map[string]string, len(fact.Fact.Ref.Attributes))
		for key, value := range fact.Fact.Ref.Attributes {
			cloned.Fact.Ref.Attributes[key] = value
		}
	}
	if fact.Entity != nil {
		entity := *fact.Entity
		cloned.Entity = &entity
	}
	return cloned
}

func graphFactKey(fact GraphFact) string {
	entity := "unattached"
	if fact.Entity != nil {
		entity = fact.Entity.Key()
	}
	return strings.Join([]string{entity, string(fact.Lens), string(fact.Fact.Kind), fact.Fact.Ref.String(), fact.Fact.ObservedAt.UTC().Format(time.RFC3339Nano)}, "\x00")
}
