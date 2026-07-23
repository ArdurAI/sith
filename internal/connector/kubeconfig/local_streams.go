// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/streaming/pkg/httpstream"

	"github.com/ArdurAI/sith/internal/localops"
)

const defaultContainerAnnotation = "kubectl.kubernetes.io/default-container"

type typedFactory func(config *rest.Config) (kubernetes.Interface, error)

type logStreamFactory func(
	ctx context.Context,
	client kubernetes.Interface,
	namespace, pod string,
	options *corev1.PodLogOptions,
) (io.ReadCloser, error)

type execFactory func(config *rest.Config, endpoint *url.URL) (remotecommand.Executor, error)

type portForwarder interface {
	ForwardPorts() error
	GetPorts() ([]portforward.ForwardedPort, error)
}

type portForwardFactory func(
	config *rest.Config,
	endpoint *url.URL,
	addresses, ports []string,
	stop <-chan struct{},
	ready chan struct{},
	out, errOut io.Writer,
) (portForwarder, error)

var _ localops.Client = (*Adapter)(nil)

// tableResponseGuard keeps untrusted API error bodies inside the adapter's reviewed HTTP boundary.
// Successful Table bodies remain stream-decoded under the tighter page and total budgets in table.go.
type tableResponseGuard struct {
	transport http.RoundTripper
}

func (guard tableResponseGuard) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := guard.transport.RoundTrip(request)
	if err != nil || response == nil ||
		(response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices) {
		return response, err
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}
	if response.StatusCode < 100 || response.StatusCode > 999 {
		return nil, fmt.Errorf("server table returned an invalid HTTP status code")
	}
	reason := metav1.StatusReasonUnknown
	statusCode := int32(response.StatusCode) // #nosec G115 -- net/http status codes are range checked immediately above.
	if response.StatusCode == http.StatusGone {
		reason = metav1.StatusReasonExpired
	}
	payload, marshalErr := json.Marshal(metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusFailure,
		Reason:   reason,
		Message:  "server table request failed",
		Code:     statusCode,
	})
	if marshalErr != nil {
		return nil, fmt.Errorf("construct bounded table error response: %w", marshalErr)
	}
	response.Body = io.NopCloser(bytes.NewReader(payload))
	response.ContentLength = int64(len(payload))
	response.Header.Set("Content-Type", "application/json")
	return response, nil
}

func guardTableErrorResponses(config *rest.Config) {
	previous := config.WrapTransport
	config.WrapTransport = func(transport http.RoundTripper) http.RoundTripper {
		if previous != nil {
			transport = previous(transport)
		}
		return tableResponseGuard{transport: transport}
	}
}

// Logs opens one pod log stream in the explicitly selected context.
func (adapter *Adapter) Logs(
	ctx context.Context,
	target localops.Target,
	options localops.LogOptions,
) (io.ReadCloser, error) {
	normalized, client, _, err := adapter.localTypedState(ctx, target)
	if err != nil {
		return nil, err
	}
	if normalized.Kind != "Pod" {
		return nil, fmt.Errorf("%w: logs require a Pod target", localops.ErrInvalidTarget)
	}
	if options.Since < 0 {
		return nil, fmt.Errorf("%w: log since duration cannot be negative", localops.ErrInvalidTarget)
	}
	if options.TailLines != nil && *options.TailLines < -1 {
		return nil, fmt.Errorf("%w: log tail must be -1 or greater", localops.ErrInvalidTarget)
	}
	pod, err := client.CoreV1().Pods(normalized.Namespace).Get(ctx, normalized.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read pod %s/%s for logs: %w", normalized.Namespace, normalized.Name, err)
	}
	container, err := selectContainer(pod, options.Container)
	if err != nil {
		return nil, err
	}
	request := &corev1.PodLogOptions{
		Container: container, Follow: options.Follow, Previous: options.Previous,
		Timestamps: options.Timestamps, TailLines: options.TailLines,
	}
	if options.Since > 0 {
		seconds := int64(options.Since.Seconds())
		if seconds == 0 {
			seconds = 1
		}
		request.SinceSeconds = &seconds
	}
	stream, err := adapter.settings.logs(ctx, client, normalized.Namespace, normalized.Name, request)
	if err != nil {
		return nil, fmt.Errorf("stream logs for %s/%s container %s: %w", normalized.Namespace, normalized.Name, container, err)
	}
	return stream, nil
}

// Exec streams one command to a pod through the local user's selected kubeconfig identity.
func (adapter *Adapter) Exec(
	ctx context.Context,
	target localops.Target,
	options localops.ExecOptions,
	streams localops.Streams,
) error {
	normalized, client, config, err := adapter.localTypedState(ctx, target)
	if err != nil {
		return err
	}
	if normalized.Kind != "Pod" {
		return fmt.Errorf("%w: exec requires a Pod target", localops.ErrInvalidTarget)
	}
	if len(options.Command) == 0 {
		return fmt.Errorf("%w: exec command is required", localops.ErrInvalidTarget)
	}
	for _, argument := range options.Command {
		if strings.ContainsRune(argument, '\x00') {
			return fmt.Errorf("%w: exec arguments cannot contain NUL bytes", localops.ErrInvalidTarget)
		}
	}
	pod, err := client.CoreV1().Pods(normalized.Namespace).Get(ctx, normalized.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read pod %s/%s for exec: %w", normalized.Namespace, normalized.Name, err)
	}
	container, err := selectContainer(pod, options.Container)
	if err != nil {
		return err
	}
	query := url.Values{
		"container": []string{container},
		"command":   append([]string(nil), options.Command...),
		"stdin":     []string{strconv.FormatBool(options.Stdin)},
		"stdout":    []string{"true"},
		"stderr":    []string{strconv.FormatBool(!options.TTY)},
		"tty":       []string{strconv.FormatBool(options.TTY)},
	}
	endpoint, err := podSubresourceURL(config, normalized.Namespace, normalized.Name, "exec", query)
	if err != nil {
		return err
	}
	executor, err := adapter.settings.exec(config, endpoint)
	if err != nil {
		return fmt.Errorf("create exec transport for %s/%s: %w", normalized.Namespace, normalized.Name, err)
	}
	stdin := streams.Stdin
	if !options.Stdin {
		stdin = nil
	}
	stderr := streams.Stderr
	if options.TTY {
		stderr = nil
	}
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin: stdin, Stdout: streams.Stdout, Stderr: stderr, Tty: options.TTY,
		TerminalSizeQueue: terminalSizeQueue{delegate: streams.Sizes},
	})
	if err != nil {
		return fmt.Errorf("exec in %s/%s container %s: %w", normalized.Namespace, normalized.Name, container, err)
	}
	return nil
}

// PortForward starts one loopback-only tunnel to a pod or service-selected pod.
func (adapter *Adapter) PortForward(
	ctx context.Context,
	request localops.ForwardRequest,
) (localops.ForwardSession, error) {
	normalized, client, config, err := adapter.localTypedState(ctx, request.Target)
	if err != nil {
		return nil, err
	}
	addresses, err := loopbackAddresses(request.Addresses)
	if err != nil {
		return nil, err
	}
	if len(request.Ports) == 0 {
		return nil, fmt.Errorf("%w: at least one port mapping is required", localops.ErrInvalidTarget)
	}
	pod, ports, err := resolveForwardTarget(ctx, client, normalized, request.Ports)
	if err != nil {
		return nil, err
	}
	endpoint, err := podSubresourceURL(config, normalized.Namespace, pod, "portforward", nil)
	if err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	ready := make(chan struct{})
	out := request.Out
	if out == nil {
		out = io.Discard
	}
	errOut := request.ErrOut
	if errOut == nil {
		errOut = io.Discard
	}
	forwarder, err := adapter.settings.forward(config, endpoint, addresses, ports, stop, ready, out, errOut)
	if err != nil {
		return nil, fmt.Errorf("create port-forward to pod %s/%s: %w", normalized.Namespace, pod, err)
	}
	session := &forwardSession{
		ready: ready, done: make(chan error, 1), stop: stop, finished: make(chan struct{}),
		forwarder: forwarder,
	}
	go session.run()
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-session.finished:
		}
	}()
	return session, nil
}

func (adapter *Adapter) localTypedState(
	ctx context.Context,
	target localops.Target,
) (localops.Target, kubernetes.Interface, *rest.Config, error) {
	normalized, _, _, config, err := adapter.localTargetState(ctx, target)
	if err != nil {
		return localops.Target{}, nil, nil, err
	}
	streamConfig := rest.CopyConfig(config)
	streamConfig.Timeout = 0
	client, err := adapter.settings.typed(streamConfig)
	if err != nil {
		return localops.Target{}, nil, nil, fmt.Errorf("create typed client for context %s: %w", normalized.Context, err)
	}
	return normalized, client, streamConfig, nil
}

func defaultTypedFactory(config *rest.Config) (kubernetes.Interface, error) {
	return kubernetes.NewForConfig(config)
}

func defaultLogStream(
	ctx context.Context,
	client kubernetes.Interface,
	namespace, pod string,
	options *corev1.PodLogOptions,
) (io.ReadCloser, error) {
	return client.CoreV1().Pods(namespace).GetLogs(pod, options).Stream(ctx)
}

func defaultExecFactory(config *rest.Config, endpoint *url.URL) (remotecommand.Executor, error) {
	websocketExecutor, err := remotecommand.NewWebSocketExecutor(config, http.MethodGet, endpoint.String())
	if err != nil {
		return nil, err
	}
	spdyExecutor, err := remotecommand.NewSPDYExecutor(config, http.MethodPost, endpoint)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(websocketExecutor, spdyExecutor, shouldFallback)
}

func defaultPortForwardFactory(
	config *rest.Config,
	endpoint *url.URL,
	addresses, ports []string,
	stop <-chan struct{},
	ready chan struct{},
	out, errOut io.Writer,
) (portForwarder, error) {
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return nil, err
	}
	spdyDialer := spdy.NewDialerForStreaming(upgrader, &http.Client{Transport: transport}, http.MethodPost, endpoint)
	websocketDialer, err := portforward.NewSPDYOverWebsocketDialerForStreaming(endpoint, config)
	if err != nil {
		return nil, err
	}
	dialer := portforward.NewFallbackDialerForStreaming(websocketDialer, spdyDialer, shouldFallback)
	return portforward.NewOnAddressesForStreaming(dialer, addresses, ports, stop, ready, out, errOut)
}

func shouldFallback(err error) bool {
	return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
}

func podSubresourceURL(
	config *rest.Config,
	namespace, pod, subresource string,
	query url.Values,
) (*url.URL, error) {
	endpoint, err := url.Parse(config.Host)
	if err != nil {
		return nil, fmt.Errorf("parse API server URL: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("parse API server URL: host must be absolute")
	}
	endpoint.Path = "/" + strings.TrimPrefix(
		path.Join(endpoint.Path, "api", "v1", "namespaces", namespace, "pods", pod, subresource),
		"/",
	)
	endpoint.RawQuery = query.Encode()
	return endpoint, nil
}

type terminalSizeQueue struct {
	delegate localops.TerminalSizeQueue
}

func (queue terminalSizeQueue) Next() *remotecommand.TerminalSize {
	if queue.delegate == nil {
		return nil
	}
	size := queue.delegate.Next()
	if size == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: size.Width, Height: size.Height}
}

func selectContainer(pod *corev1.Pod, requested string) (string, error) {
	names := podContainerNames(pod)
	if requested != "" {
		if slices.Contains(names, requested) {
			return requested, nil
		}
		return "", fmt.Errorf("%w: container %q not found in pod %s; choose one of %s",
			localops.ErrInvalidTarget, requested, pod.Name, strings.Join(names, ", "))
	}
	if annotated := pod.Annotations[defaultContainerAnnotation]; annotated != "" && slices.Contains(names, annotated) {
		return annotated, nil
	}
	if len(names) == 1 {
		return names[0], nil
	}
	if len(names) == 0 {
		return "", fmt.Errorf("%w: pod %s has no containers", localops.ErrInvalidTarget, pod.Name)
	}
	return "", fmt.Errorf("%w: pod %s has multiple containers; select one of %s",
		localops.ErrInvalidTarget, pod.Name, strings.Join(names, ", "))
}

func podContainerNames(pod *corev1.Pod) []string {
	names := make([]string, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers)+len(pod.Spec.EphemeralContainers))
	for _, container := range pod.Spec.Containers {
		names = append(names, container.Name)
	}
	for _, container := range pod.Spec.InitContainers {
		names = append(names, container.Name)
	}
	for _, container := range pod.Spec.EphemeralContainers {
		names = append(names, container.Name)
	}
	return names
}

func loopbackAddresses(addresses []string) ([]string, error) {
	if len(addresses) == 0 {
		return []string{"localhost"}, nil
	}
	result := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address != "localhost" && address != "127.0.0.1" && address != "::1" {
			return nil, fmt.Errorf("%w: port-forward address %q is not loopback", localops.ErrInvalidTarget, address)
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	return result, nil
}

func resolveForwardTarget(
	ctx context.Context,
	client kubernetes.Interface,
	target localops.Target,
	requested []string,
) (string, []string, error) {
	switch target.Kind {
	case "Pod":
		pod, err := client.CoreV1().Pods(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return "", nil, fmt.Errorf("read pod %s/%s for port-forward: %w", target.Namespace, target.Name, err)
		}
		ports, err := resolvePodPorts(pod, requested)
		return target.Name, ports, err
	case "Service":
		service, err := client.CoreV1().Services(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return "", nil, fmt.Errorf("read service %s/%s for port-forward: %w", target.Namespace, target.Name, err)
		}
		if len(service.Spec.Selector) == 0 {
			return "", nil, fmt.Errorf("%w: service %s has no pod selector", localops.ErrInvalidTarget, target.Name)
		}
		pods, err := client.CoreV1().Pods(target.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(service.Spec.Selector).String(),
		})
		if err != nil {
			return "", nil, fmt.Errorf("list pods for service %s/%s: %w", target.Namespace, target.Name, err)
		}
		pod, err := chooseForwardPod(pods.Items)
		if err != nil {
			return "", nil, fmt.Errorf("service %s/%s: %w", target.Namespace, target.Name, err)
		}
		ports, err := resolveServicePorts(service, pod, requested)
		return pod.Name, ports, err
	default:
		return "", nil, fmt.Errorf("%w: port-forward requires a Pod or Service target", localops.ErrInvalidTarget)
	}
}

func chooseForwardPod(pods []corev1.Pod) (*corev1.Pod, error) {
	candidates := make([]corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning && pod.DeletionTimestamp == nil {
			candidates = append(candidates, pod)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no running pod is available", localops.ErrInvalidTarget)
	}
	sort.Slice(candidates, func(left, right int) bool {
		leftReady, rightReady := podIsReady(&candidates[left]), podIsReady(&candidates[right])
		if leftReady != rightReady {
			return leftReady
		}
		return candidates[left].Name < candidates[right].Name
	})
	return &candidates[0], nil
}

func podIsReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

type requestedPort struct {
	local  string
	remote string
}

func parseRequestedPort(value string) (requestedPort, error) {
	parts := strings.Split(value, ":")
	if len(parts) > 2 || len(parts) == 0 || strings.TrimSpace(parts[len(parts)-1]) == "" {
		return requestedPort{}, fmt.Errorf("%w: invalid port mapping %q", localops.ErrInvalidTarget, value)
	}
	if len(parts) == 1 {
		return requestedPort{remote: parts[0]}, nil
	}
	if parts[0] != "" {
		if _, err := parseNumericPort(parts[0]); err != nil {
			return requestedPort{}, fmt.Errorf("%w: invalid local port in %q: %v", localops.ErrInvalidTarget, value, err)
		}
	}
	return requestedPort{local: parts[0], remote: parts[1]}, nil
}

func resolvePodPorts(pod *corev1.Pod, requested []string) ([]string, error) {
	result := make([]string, 0, len(requested))
	for _, value := range requested {
		mapping, err := parseRequestedPort(value)
		if err != nil {
			return nil, err
		}
		remote, err := resolvePodPort(pod, mapping.remote)
		if err != nil {
			return nil, err
		}
		local := mapping.local
		if local == "" && !strings.Contains(value, ":") {
			local = strconv.Itoa(remote)
		}
		result = append(result, local+":"+strconv.Itoa(remote))
	}
	return result, nil
}

func resolveServicePorts(service *corev1.Service, pod *corev1.Pod, requested []string) ([]string, error) {
	result := make([]string, 0, len(requested))
	for _, value := range requested {
		mapping, err := parseRequestedPort(value)
		if err != nil {
			return nil, err
		}
		servicePort, err := findServicePort(service, mapping.remote)
		if err != nil {
			return nil, err
		}
		if servicePort.Protocol != corev1.ProtocolTCP {
			return nil, fmt.Errorf("%w: service port %s uses %s, but port-forward requires TCP",
				localops.ErrInvalidTarget, mapping.remote, servicePort.Protocol)
		}
		remote := int(servicePort.TargetPort.IntVal)
		if servicePort.TargetPort.Type == intstr.String {
			remote, err = resolvePodPort(pod, servicePort.TargetPort.StrVal)
			if err != nil {
				return nil, err
			}
		}
		if remote == 0 {
			remote = int(servicePort.Port)
		}
		local := mapping.local
		if local == "" && !strings.Contains(value, ":") {
			local = strconv.Itoa(int(servicePort.Port))
		}
		result = append(result, local+":"+strconv.Itoa(remote))
	}
	return result, nil
}

func findServicePort(service *corev1.Service, value string) (*corev1.ServicePort, error) {
	if number, err := parseNumericPort(value); err == nil {
		for index := range service.Spec.Ports {
			if int(service.Spec.Ports[index].Port) == number {
				return &service.Spec.Ports[index], nil
			}
		}
	} else {
		for index := range service.Spec.Ports {
			if service.Spec.Ports[index].Name == value {
				return &service.Spec.Ports[index], nil
			}
		}
	}
	return nil, fmt.Errorf("%w: service %s has no port %q", localops.ErrInvalidTarget, service.Name, value)
}

func resolvePodPort(pod *corev1.Pod, value string) (int, error) {
	if number, err := parseNumericPort(value); err == nil {
		return number, nil
	}
	port := 0
	for _, container := range pod.Spec.Containers {
		for _, declared := range container.Ports {
			if declared.Name != value || declared.Protocol != corev1.ProtocolTCP {
				continue
			}
			if port != 0 && port != int(declared.ContainerPort) {
				return 0, fmt.Errorf("%w: named pod port %q is ambiguous", localops.ErrInvalidTarget, value)
			}
			port = int(declared.ContainerPort)
		}
	}
	if port == 0 {
		return 0, fmt.Errorf("%w: pod %s has no TCP port %q", localops.ErrInvalidTarget, pod.Name, value)
	}
	return port, nil
}

func parseNumericPort(value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}
	return number, nil
}

type forwardSession struct {
	ready     <-chan struct{}
	done      chan error
	stop      chan struct{}
	finished  chan struct{}
	forwarder portForwarder
	closeOnce sync.Once
}

func (session *forwardSession) run() {
	err := session.forwarder.ForwardPorts()
	session.done <- err
	close(session.finished)
}

func (session *forwardSession) Ready() <-chan struct{} { return session.ready }

func (session *forwardSession) Done() <-chan error { return session.done }

func (session *forwardSession) Ports() ([]localops.ForwardedPort, error) {
	ports, err := session.forwarder.GetPorts()
	if err != nil {
		return nil, err
	}
	result := make([]localops.ForwardedPort, 0, len(ports))
	for _, port := range ports {
		result = append(result, localops.ForwardedPort{Local: port.Local, Remote: port.Remote})
	}
	return result, nil
}

func (session *forwardSession) Close() error {
	session.closeOnce.Do(func() { close(session.stop) })
	return nil
}
