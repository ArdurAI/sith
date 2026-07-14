// SPDX-License-Identifier: Apache-2.0

package hubocm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"
	konnectivity "sigs.k8s.io/apiserver-network-proxy/konnectivity-client/pkg/client"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	protocolVersion = "1.0.0"
	maxResources    = 500
	listPageSize    = 100

	maxVulnerabilityReports     = 100
	vulnerabilityReportPageSize = 10
	maxVulnerabilityReportPages = maxVulnerabilityReports / vulnerabilityReportPageSize
	maxVulnerabilityReportBytes = 64 * 1024
)

var (
	rolloutGVR             = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}
	vulnerabilityReportGVR = schema.GroupVersionResource{Group: "aquasecurity.github.io", Version: "v1alpha1", Resource: "vulnerabilityreports"}
)

// Config configures the direct ClusterProxy transport. ProxyTLSConfig must be constructed
// from deployment-mounted proxy mTLS material; it is cloned and never persisted by Sith.
type Config struct {
	CredentialReader  CredentialReader
	ProxyAddress      string
	ProxyTLSConfig    *tls.Config
	KubeAPIServerName string
	Now               func() time.Time
}

// Adapter implements hubfleet.Transport through OCM ClusterProxy's released direct
// Konnectivity client. It never forwards a caller Authorization header.
type Adapter struct {
	credentials       CredentialReader
	proxyAddress      string
	proxyTLS          *tls.Config
	kubeAPIServerName string
	now               func() time.Time
	tunnels           tunnelFactory
	clients           snapshotClientFactory
}

var _ hubfleet.Transport = (*Adapter)(nil)

// New constructs a fail-closed direct ClusterProxy adapter.
func New(config Config) (*Adapter, error) {
	if config.CredentialReader == nil {
		return nil, fmt.Errorf("new direct OCM transport: credential reader is required")
	}
	if err := validateProxyAddress(config.ProxyAddress); err != nil {
		return nil, fmt.Errorf("new direct OCM transport: %w", err)
	}
	if err := validateProxyTLS(config.ProxyTLSConfig); err != nil {
		return nil, fmt.Errorf("new direct OCM transport: %w", err)
	}
	if len(validation.IsDNS1123Subdomain(config.KubeAPIServerName)) != 0 {
		return nil, fmt.Errorf("new direct OCM transport: Kubernetes TLS server name is invalid")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	proxyTLS := config.ProxyTLSConfig.Clone()
	return &Adapter{
		credentials:       config.CredentialReader,
		proxyAddress:      config.ProxyAddress,
		proxyTLS:          proxyTLS,
		kubeAPIServerName: config.KubeAPIServerName,
		now:               config.Now,
		tunnels:           grpcTunnelFactory{address: config.ProxyAddress, tls: proxyTLS},
		clients:           defaultSnapshotClientFactory,
	}, nil
}

// Snapshot reads the bounded inventory and health projection for one registered spoke.
func (adapter *Adapter) Snapshot(
	ctx context.Context,
	workspaceID tenancy.WorkspaceID,
	spoke hubfleet.Spoke,
) (hubfleet.Snapshot, error) {
	if adapter == nil || adapter.credentials == nil || adapter.tunnels == nil || adapter.clients == nil || ctx == nil {
		return hubfleet.Snapshot{}, fmt.Errorf("direct OCM snapshot: adapter and context are required")
	}
	if err := tenancy.ValidateWorkspaceID(workspaceID); err != nil {
		return hubfleet.Snapshot{}, fmt.Errorf("direct OCM snapshot: workspace is invalid")
	}
	if err := spoke.Validate(); err != nil {
		return hubfleet.Snapshot{}, fmt.Errorf("direct OCM snapshot: spoke is invalid")
	}
	managedCluster, err := parseManagedClusterRef(spoke.ManagedClusterRef)
	if err != nil {
		return hubfleet.Snapshot{}, fmt.Errorf("direct OCM snapshot: managed cluster reference is invalid")
	}
	credential, err := adapter.credentials.Read(ctx, workspaceID, managedCluster)
	if err != nil {
		return hubfleet.Snapshot{}, contextOrGeneric(ctx, "read direct OCM credential")
	}
	defer clearCredential(&credential)

	config := adapter.restConfig(ctx, managedCluster, credential)
	defer func() {
		clear(config.CAData)
		config.BearerToken = ""
	}()
	client, err := adapter.clients(config)
	if err != nil {
		return hubfleet.Snapshot{}, contextOrGeneric(ctx, "construct direct OCM client")
	}
	defer client.Close()

	observedAt := adapter.now().UTC()
	facts, err := collectFacts(ctx, client, spoke, observedAt)
	if err != nil {
		return hubfleet.Snapshot{}, contextOrGeneric(ctx, "collect direct OCM snapshot")
	}
	return hubfleet.Snapshot{ObservedAt: observedAt, Facts: facts}, nil
}

func (adapter *Adapter) restConfig(ctx context.Context, managedCluster string, credential projectedCredential) *rest.Config {
	target := net.JoinHostPort(managedCluster, "443")
	return &rest.Config{
		Host:        "https://" + managedCluster,
		BearerToken: string(credential.token),
		TLSClientConfig: rest.TLSClientConfig{
			CAData:     append([]byte(nil), credential.ca...),
			ServerName: adapter.kubeAPIServerName,
		},
		Dial: adapter.dialContext(ctx, target),
	}
}

func (adapter *Adapter) dialContext(snapshotCtx context.Context, target string) func(context.Context, string, string) (net.Conn, error) {
	return func(requestCtx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != target {
			return nil, fmt.Errorf("direct OCM tunnel rejected an unpinned dial target")
		}
		if err := requestCtx.Err(); err != nil {
			return nil, err
		}
		tunnelCtx, cancel := context.WithCancel(snapshotCtx)
		tunnel, err := adapter.tunnels.Open(requestCtx, tunnelCtx)
		if err != nil {
			cancel()
			return nil, contextOrGeneric(requestCtx, "open direct OCM tunnel")
		}
		connection, err := tunnel.DialContext(requestCtx, network, target)
		if err != nil {
			cancel()
			return nil, contextOrGeneric(requestCtx, "dial direct OCM tunnel")
		}
		return &tunnelConnection{Conn: connection, cancel: cancel}, nil
	}
}

func validateProxyAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || strings.ContainsAny(host, "/\\@") {
		return fmt.Errorf("proxy address must be a host and port")
	}
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil || value == 0 {
		return fmt.Errorf("proxy address must use a valid port")
	}
	return nil
}

func validateProxyTLS(config *tls.Config) error {
	if config == nil || config.InsecureSkipVerify || config.MinVersion < tls.VersionTLS12 || config.ServerName == "" ||
		config.RootCAs == nil || len(config.Certificates) != 1 || len(config.Certificates[0].Certificate) == 0 ||
		config.Certificates[0].PrivateKey == nil || config.GetClientCertificate != nil {
		return fmt.Errorf("proxy TLS configuration must pin CA, server name, TLS 1.2+, and one client certificate")
	}
	return nil
}

type tunnelFactory interface {
	Open(createCtx, tunnelCtx context.Context) (konnectivity.Tunnel, error)
}

type grpcTunnelFactory struct {
	address string
	tls     *tls.Config
}

func (factory grpcTunnelFactory) Open(createCtx, tunnelCtx context.Context) (konnectivity.Tunnel, error) {
	return konnectivity.CreateSingleUseGrpcTunnelWithContext(
		createCtx,
		tunnelCtx,
		factory.address,
		//nolint:staticcheck // Konnectivity has no NewClient-compatible constructor; blocking preserves the caller-bounded creation deadline.
		grpc.WithBlock(),
		grpc.WithTransportCredentials(grpccredentials.NewTLS(factory.tls.Clone())),
	)
}

type tunnelConnection struct {
	net.Conn
	once   sync.Once
	cancel context.CancelFunc
}

func (connection *tunnelConnection) Close() error {
	connection.once.Do(connection.cancel)
	return connection.Conn.Close()
}

type snapshotClient interface {
	ListDeployments(context.Context, metav1.ListOptions) (*appsv1.DeploymentList, error)
	ListPods(context.Context, metav1.ListOptions) (*corev1.PodList, error)
	ListRollouts(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error)
	ListVulnerabilityReports(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error)
	Close()
}

type snapshotClientFactory func(*rest.Config) (snapshotClient, error)

type kubeSnapshotClient struct {
	kube    kubernetes.Interface
	dynamic dynamic.Interface
	http    *http.Client
}

func defaultSnapshotClientFactory(config *rest.Config) (snapshotClient, error) {
	transport, err := rest.TransportFor(config)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Transport: transport}
	kubeClient, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, err
	}
	dynamicClient, err := dynamic.NewForConfigAndClient(config, httpClient)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, err
	}
	return &kubeSnapshotClient{kube: kubeClient, dynamic: dynamicClient, http: httpClient}, nil
}

func (client *kubeSnapshotClient) ListDeployments(ctx context.Context, options metav1.ListOptions) (*appsv1.DeploymentList, error) {
	return client.kube.AppsV1().Deployments("").List(ctx, options)
}

func (client *kubeSnapshotClient) ListPods(ctx context.Context, options metav1.ListOptions) (*corev1.PodList, error) {
	return client.kube.CoreV1().Pods("").List(ctx, options)
}

func (client *kubeSnapshotClient) ListRollouts(ctx context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return client.dynamic.Resource(rolloutGVR).Namespace("").List(ctx, options)
}

func (client *kubeSnapshotClient) ListVulnerabilityReports(ctx context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return client.dynamic.Resource(vulnerabilityReportGVR).Namespace("").List(ctx, options)
}

func (client *kubeSnapshotClient) Close() {
	if client != nil && client.http != nil {
		client.http.CloseIdleConnections()
	}
}

func collectFacts(ctx context.Context, client snapshotClient, spoke hubfleet.Spoke, observedAt time.Time) ([]fleet.Evidence, error) {
	if client == nil {
		return nil, fmt.Errorf("snapshot client is required")
	}
	remaining := maxResources
	facts := make([]fleet.Evidence, 0, maxResources*2+maxVulnerabilityReports)
	deployments, err := listDeployments(ctx, client, &remaining)
	if err != nil {
		return nil, err
	}
	for index := range deployments {
		facts = append(facts, deploymentFacts(spoke.ID, deployments[index], observedAt)...)
	}
	pods, err := listPods(ctx, client, &remaining)
	if err != nil {
		return nil, err
	}
	for index := range pods {
		facts = append(facts, podFacts(spoke.ID, pods[index], observedAt)...)
	}
	rollouts, err := listRollouts(ctx, client, &remaining)
	if err != nil {
		return nil, err
	}
	for index := range rollouts {
		facts = append(facts, rolloutFacts(spoke.ID, rollouts[index], observedAt)...)
	}
	cveFacts, err := collectCVEFacts(ctx, client, spoke.ID, podImageDigestSet(pods), observedAt)
	if err != nil {
		return nil, err
	}
	facts = append(facts, cveFacts...)
	return facts, nil
}

func listDeployments(ctx context.Context, client snapshotClient, remaining *int) ([]appsv1.Deployment, error) {
	items := make([]appsv1.Deployment, 0)
	continueToken := ""
	for {
		page, err := client.ListDeployments(ctx, listOptions(continueToken, *remaining))
		if err != nil {
			return nil, contextOrGeneric(ctx, "list deployments")
		}
		if err := appendPage(&items, page.Items, page.Continue, remaining); err != nil {
			return nil, err
		}
		continueToken = page.Continue
		if continueToken == "" {
			return items, nil
		}
	}
}

func listPods(ctx context.Context, client snapshotClient, remaining *int) ([]corev1.Pod, error) {
	if *remaining <= 0 {
		return nil, fmt.Errorf("direct OCM snapshot exceeds the bounded resource limit")
	}
	items := make([]corev1.Pod, 0)
	continueToken := ""
	for {
		page, err := client.ListPods(ctx, listOptions(continueToken, *remaining))
		if err != nil {
			return nil, contextOrGeneric(ctx, "list pods")
		}
		if err := appendPage(&items, page.Items, page.Continue, remaining); err != nil {
			return nil, err
		}
		continueToken = page.Continue
		if continueToken == "" {
			return items, nil
		}
	}
}

func listRollouts(ctx context.Context, client snapshotClient, remaining *int) ([]unstructured.Unstructured, error) {
	if *remaining <= 0 {
		return nil, fmt.Errorf("direct OCM snapshot exceeds the bounded resource limit")
	}
	items := make([]unstructured.Unstructured, 0)
	continueToken := ""
	for {
		page, err := client.ListRollouts(ctx, listOptions(continueToken, *remaining))
		if apierrors.IsNotFound(err) && continueToken == "" {
			return items, nil
		}
		if err != nil {
			return nil, contextOrGeneric(ctx, "list rollouts")
		}
		if err := appendPage(&items, page.Items, page.GetContinue(), remaining); err != nil {
			return nil, err
		}
		continueToken = page.GetContinue()
		if continueToken == "" {
			return items, nil
		}
	}
}

func listOptions(continueToken string, remaining int) metav1.ListOptions {
	limit := remaining
	if limit > listPageSize {
		limit = listPageSize
	}
	return metav1.ListOptions{Limit: int64(limit), Continue: continueToken}
}

func vulnerabilityReportListOptions(continueToken string, remaining int) metav1.ListOptions {
	limit := remaining
	if limit > vulnerabilityReportPageSize {
		limit = vulnerabilityReportPageSize
	}
	return metav1.ListOptions{Limit: int64(limit), Continue: continueToken}
}

func appendPage[T any](items *[]T, page []T, continueToken string, remaining *int) error {
	if len(page) > *remaining || (len(page) == *remaining && continueToken != "") {
		return fmt.Errorf("direct OCM snapshot exceeds the bounded resource limit")
	}
	*items = append(*items, page...)
	*remaining -= len(page)
	return nil
}

func deploymentFacts(spokeID string, deployment appsv1.Deployment, observedAt time.Time) []fleet.Evidence {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	health := "Progressing"
	if deployment.Status.AvailableReplicas >= desired && deployment.Status.ObservedGeneration >= deployment.Generation {
		health = "Healthy"
	} else if deployment.Status.UnavailableReplicas > 0 {
		health = "Degraded"
	}
	return resourceFacts(spokeID, "Deployment", deployment.Namespace, deployment.Name, observedAt,
		map[string]any{"resource": "Deployment", "replicas": desired, "available_replicas": deployment.Status.AvailableReplicas, "generation": deployment.Generation}, health)
}

func podFacts(spokeID string, pod corev1.Pod, observedAt time.Time) []fleet.Evidence {
	ready := int32(0)
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			ready = 1
		}
	}
	health := podHealth(pod)
	inventory := map[string]any{"resource": "Pod", "ready": ready, "generation": pod.Generation}
	if digests := podImageDigests(pod); len(digests) > 0 {
		inventory["image_digests"] = digests
	}
	return resourceFacts(spokeID, "Pod", pod.Namespace, pod.Name, observedAt,
		inventory, health)
}

func podImageDigests(pod corev1.Pod) []string {
	seen := make(map[string]struct{}, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		digest, err := fleet.ImageDigestFromRuntimeImageID(status.ImageID)
		if err != nil {
			continue
		}
		seen[digest] = struct{}{}
	}
	digests := make([]string, 0, len(seen))
	for digest := range seen {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	return digests
}

func podImageDigestSet(pods []corev1.Pod) map[string]struct{} {
	digests := make(map[string]struct{})
	for index := range pods {
		for _, digest := range podImageDigests(pods[index]) {
			digests[digest] = struct{}{}
		}
	}
	return digests
}

type cveAggregate struct {
	identifiers map[string]struct{}
	severity    string
}

func collectCVEFacts(
	ctx context.Context,
	client snapshotClient,
	spokeID string,
	knownDigests map[string]struct{},
	observedAt time.Time,
) ([]fleet.Evidence, error) {
	if len(knownDigests) == 0 {
		return nil, nil
	}
	remaining := maxVulnerabilityReports
	continueToken := ""
	aggregates := make(map[string]cveAggregate)
	for pages := 0; ; pages++ {
		if pages >= maxVulnerabilityReportPages {
			return nil, fmt.Errorf("direct OCM snapshot exceeds the bounded vulnerability report page limit")
		}
		page, err := client.ListVulnerabilityReports(ctx, vulnerabilityReportListOptions(continueToken, remaining))
		if apierrors.IsNotFound(err) && continueToken == "" {
			return []fleet.Evidence{}, nil
		}
		if err != nil {
			return nil, contextOrGeneric(ctx, "list vulnerability reports")
		}
		if len(page.Items) > remaining || (len(page.Items) == remaining && page.GetContinue() != "") {
			return nil, fmt.Errorf("direct OCM snapshot exceeds the bounded vulnerability report limit")
		}
		for index := range page.Items {
			digest, identifiers, severity, found := reportCVEObservation(page.Items[index], knownDigests)
			if !found {
				continue
			}
			aggregate := aggregates[digest]
			if aggregate.identifiers == nil {
				aggregate.identifiers = make(map[string]struct{}, len(identifiers))
			}
			for _, identifier := range identifiers {
				aggregate.identifiers[identifier] = struct{}{}
			}
			if cveSeverityRank(severity) > cveSeverityRank(aggregate.severity) {
				aggregate.severity = severity
			}
			aggregates[digest] = aggregate
		}
		remaining -= len(page.Items)
		continueToken = page.GetContinue()
		if continueToken == "" {
			break
		}
	}
	digests := make([]string, 0, len(aggregates))
	for digest := range aggregates {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	facts := make([]fleet.Evidence, 0, len(digests))
	for _, digest := range digests {
		aggregate := aggregates[digest]
		identifiers := make([]string, 0, len(aggregate.identifiers))
		for identifier := range aggregate.identifiers {
			identifiers = append(identifiers, identifier)
		}
		observation, err := fleet.CanonicalCVEObservation(digest, identifiers, aggregate.severity)
		if err != nil {
			return nil, fmt.Errorf("normalize vulnerability report observation: %w", err)
		}
		payload, err := json.Marshal(observation)
		if err != nil {
			return nil, fmt.Errorf("encode normalized vulnerability report observation: %w", err)
		}
		facts = append(facts, fleet.Evidence{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: spokeID, Kind: "Image", Name: digest},
			Kind:       fleet.FactCVE,
			Observed:   payload,
			ObservedAt: observedAt,
			Source:     spokeID,
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: protocolVersion},
		})
	}
	return facts, nil
}

func reportCVEObservation(report unstructured.Unstructured, knownDigests map[string]struct{}) (string, []string, string, bool) {
	encoded, err := json.Marshal(report.Object)
	if err != nil || len(encoded) > maxVulnerabilityReportBytes {
		return "", nil, "", false
	}
	digest, found, err := unstructured.NestedString(report.Object, "report", "artifact", "digest")
	if err != nil || !found || fleet.ValidateImageDigest(digest) != nil {
		return "", nil, "", false
	}
	if _, exists := knownDigests[digest]; !exists {
		return "", nil, "", false
	}
	vulnerabilities, found, err := unstructured.NestedSlice(report.Object, "report", "vulnerabilities")
	if err != nil || !found || len(vulnerabilities) == 0 || len(vulnerabilities) > 256 {
		return "", nil, "", false
	}
	identifiers := make([]string, 0, len(vulnerabilities))
	seen := make(map[string]struct{}, len(vulnerabilities))
	severity := ""
	for _, raw := range vulnerabilities {
		vulnerability, ok := raw.(map[string]any)
		if !ok {
			return "", nil, "", false
		}
		identifier, found, err := unstructured.NestedString(vulnerability, "vulnerabilityID")
		if err != nil || !found {
			return "", nil, "", false
		}
		identifier, err = fleet.NormalizeCVEIdentifier(identifier)
		if err != nil {
			return "", nil, "", false
		}
		if _, exists := seen[identifier]; exists {
			return "", nil, "", false
		}
		seen[identifier] = struct{}{}
		reportedSeverity, found, err := unstructured.NestedString(vulnerability, "severity")
		if err != nil || !found {
			return "", nil, "", false
		}
		reportedSeverity, err = fleet.NormalizeCVESeverity(reportedSeverity)
		if err != nil {
			return "", nil, "", false
		}
		if cveSeverityRank(reportedSeverity) > cveSeverityRank(severity) {
			severity = reportedSeverity
		}
		identifiers = append(identifiers, identifier)
	}
	return digest, identifiers, severity, true
}

func cveSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

func rolloutFacts(spokeID string, rollout unstructured.Unstructured, observedAt time.Time) []fleet.Evidence {
	replicas, _, _ := unstructured.NestedInt64(rollout.Object, "status", "replicas")
	available, _, _ := unstructured.NestedInt64(rollout.Object, "status", "availableReplicas")
	phase, _, _ := unstructured.NestedString(rollout.Object, "status", "phase")
	health := rolloutHealth(phase, replicas, available)
	return resourceFacts(spokeID, "Rollout", rollout.GetNamespace(), rollout.GetName(), observedAt,
		map[string]any{"resource": "Rollout", "replicas": replicas, "available_replicas": available, "generation": rollout.GetGeneration()}, health)
}

func resourceFacts(
	spokeID, kind, namespace, name string,
	observedAt time.Time,
	inventory map[string]any,
	health string,
) []fleet.Evidence {
	ref := fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: spokeID, Kind: kind, Namespace: namespace, Name: name}
	provenance := fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: protocolVersion}
	return []fleet.Evidence{
		{Ref: ref, Kind: fleet.FactInventory, Observed: mustObserved(inventory), ObservedAt: observedAt, Source: spokeID, Provenance: provenance},
		{Ref: ref, Kind: fleet.FactHealth, Observed: mustObserved(map[string]any{"status": health}), ObservedAt: observedAt, Source: spokeID, Provenance: provenance},
	}
}

func mustObserved(value map[string]any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("direct OCM observed projection is not serializable")
	}
	return encoded
}

func podHealth(pod corev1.Pod) string {
	if pod.Status.Phase == corev1.PodFailed {
		return "Degraded"
	}
	for _, status := range append(append([]corev1.ContainerStatus(nil), pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if status.State.Waiting != nil && (status.State.Waiting.Reason == "CrashLoopBackOff" || status.State.Waiting.Reason == "ImagePullBackOff" || status.State.Waiting.Reason == "ErrImagePull") {
			return "Degraded"
		}
	}
	if pod.Status.Phase == corev1.PodRunning {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return "Healthy"
			}
		}
		return "Progressing"
	}
	if pod.Status.Phase == corev1.PodSucceeded {
		return "Healthy"
	}
	return "Unknown"
}

func rolloutHealth(phase string, replicas, available int64) string {
	switch strings.ToLower(phase) {
	case "healthy":
		return "Healthy"
	case "degraded", "error":
		return "Degraded"
	case "progressing", "paused":
		return "Progressing"
	}
	if replicas > 0 && available >= replicas {
		return "Healthy"
	}
	return "Unknown"
}

func clearCredential(credential *projectedCredential) {
	if credential == nil {
		return
	}
	clear(credential.token)
	clear(credential.ca)
}

func contextOrGeneric(ctx context.Context, operation string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%s failed", operation)
}
