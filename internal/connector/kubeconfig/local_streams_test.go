// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/ArdurAI/sith/internal/localops"
)

func TestLocalLogsUsesExplicitContextAndSafeContainerSelection(t *testing.T) {
	t.Parallel()
	pod := testPod("apps", "api", "sidecar", "main")
	pod.Annotations = map[string]string{defaultContainerAnnotation: "main"}
	typed := kubernetesfake.NewSimpleClientset(pod)
	adapter := localStreamingTestAdapter(t, typed)
	var gotNamespace, gotPod string
	var gotOptions *corev1.PodLogOptions
	adapter.settings.logs = func(
		_ context.Context,
		_ kubernetes.Interface,
		namespace, pod string,
		options *corev1.PodLogOptions,
	) (io.ReadCloser, error) {
		gotNamespace, gotPod, gotOptions = namespace, pod, options.DeepCopy()
		return io.NopCloser(strings.NewReader("alpha log\n")), nil
	}
	tail := int64(25)
	stream, err := adapter.Logs(context.Background(), localops.Target{
		Context: "alpha", Namespace: "apps", Kind: "Pod", Name: "api",
	}, localops.LogOptions{Follow: true, Timestamps: true, TailLines: &tail, Since: 1500 * time.Millisecond})
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	defer stream.Close()
	payload, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}
	if string(payload) != "alpha log\n" || gotNamespace != "apps" || gotPod != "api" {
		t.Fatalf("stream = %q, target = %s/%s", payload, gotNamespace, gotPod)
	}
	if gotOptions.Container != "main" || !gotOptions.Follow || !gotOptions.Timestamps ||
		gotOptions.SinceSeconds == nil || *gotOptions.SinceSeconds != 1 {
		t.Fatalf("PodLogOptions = %#v", gotOptions)
	}

	_, err = adapter.Logs(context.Background(), localops.Target{
		Context: "alpha", Namespace: "apps", Kind: "Pod", Name: "api",
	}, localops.LogOptions{Container: "missing"})
	if err == nil || !strings.Contains(err.Error(), "choose one of") {
		t.Fatalf("Logs(missing container) error = %v", err)
	}
}

func TestLocalExecBuildsRequestWithoutShellInterpretation(t *testing.T) {
	t.Parallel()
	typed := kubernetesfake.NewSimpleClientset(testPod("apps", "api", "main"))
	adapter := localStreamingTestAdapter(t, typed)
	executor := &recordingExecutor{}
	var endpoint *url.URL
	adapter.settings.exec = func(_ *rest.Config, requestURL *url.URL) (remotecommand.Executor, error) {
		endpoint = requestURL
		return executor, nil
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := adapter.Exec(context.Background(), localops.Target{
		Context: "alpha", Namespace: "apps", Kind: "Pod", Name: "api",
	}, localops.ExecOptions{Container: "main", Command: []string{"printf", "%s", "$(not-a-shell)"}}, localops.Streams{
		Stdout: stdout, Stderr: stderr,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if endpoint.Path != "/api/v1/namespaces/apps/pods/api/exec" {
		t.Fatalf("exec endpoint path = %q", endpoint.Path)
	}
	if got := endpoint.Query()["command"]; !slices.Equal(got, []string{"printf", "%s", "$(not-a-shell)"}) {
		t.Fatalf("command query = %q", got)
	}
	if endpoint.Query().Get("container") != "main" || endpoint.Query().Get("stderr") != "true" {
		t.Fatalf("exec query = %q", endpoint.RawQuery)
	}
	if executor.options.Stdout != stdout || executor.options.Stderr != stderr || executor.options.Stdin != nil {
		t.Fatalf("stream options = %#v", executor.options)
	}
}

func TestLocalPortForwardIsLoopbackOnlyAndResolvesServiceTarget(t *testing.T) {
	t.Parallel()
	podA := testPod("apps", "api-a", "main")
	podA.Labels = map[string]string{"app": "api"}
	podA.Spec.Containers[0].Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}}
	podA.Status.Phase = corev1.PodRunning
	podB := podA.DeepCopy()
	podB.Name = "api-b"
	podB.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "api"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "api"},
			Ports:    []corev1.ServicePort{{Name: "web", Port: 80, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}},
		},
	}
	typed := kubernetesfake.NewSimpleClientset(podA, podB, service)
	adapter := localStreamingTestAdapter(t, typed)
	var gotEndpoint *url.URL
	var gotAddresses, gotPorts []string
	var fake *recordingPortForwarder
	adapter.settings.forward = func(
		_ *rest.Config,
		endpoint *url.URL,
		addresses, ports []string,
		stop <-chan struct{},
		ready chan struct{},
		_, _ io.Writer,
	) (portForwarder, error) {
		gotEndpoint = endpoint
		gotAddresses, gotPorts = append([]string(nil), addresses...), append([]string(nil), ports...)
		fake = &recordingPortForwarder{stop: stop, ready: ready, ports: []portforward.ForwardedPort{{Local: 18080, Remote: 8080}}}
		return fake, nil
	}
	target := localops.Target{Context: "alpha", Namespace: "apps", Kind: "Service", Name: "api"}
	session, err := adapter.PortForward(context.Background(), localops.ForwardRequest{
		Target: target, Addresses: []string{"127.0.0.1", "127.0.0.1"}, Ports: []string{"18080:web"},
	})
	if err != nil {
		t.Fatalf("PortForward() error = %v", err)
	}
	select {
	case <-session.Ready():
	case <-time.After(time.Second):
		t.Fatal("port-forward did not become ready")
	}
	if gotEndpoint.Path != "/api/v1/namespaces/apps/pods/api-b/portforward" {
		t.Fatalf("port-forward endpoint = %q", gotEndpoint.Path)
	}
	if !slices.Equal(gotAddresses, []string{"127.0.0.1"}) || !slices.Equal(gotPorts, []string{"18080:8080"}) {
		t.Fatalf("addresses = %v, ports = %v", gotAddresses, gotPorts)
	}
	forwarded, err := session.Ports()
	if err != nil || len(forwarded) != 1 || forwarded[0].Remote != 8080 {
		t.Fatalf("Ports() = %#v, %v", forwarded, err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-session.Done():
		if err != nil {
			t.Fatalf("Done() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("port-forward did not stop")
	}

	_, err = adapter.PortForward(context.Background(), localops.ForwardRequest{
		Target: target, Addresses: []string{"0.0.0.0"}, Ports: []string{"80"},
	})
	if err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("PortForward(non-loopback) error = %v", err)
	}
}

func TestLocalStreamingRejectsImplicitContextBeforeCreatingTypedClient(t *testing.T) {
	t.Parallel()
	adapter := localTestAdapter(t, localFakeClient())
	created := false
	adapter.settings.typed = func(_ *rest.Config) (kubernetes.Interface, error) {
		created = true
		return kubernetesfake.NewSimpleClientset(), nil
	}
	_, err := adapter.Logs(context.Background(), localops.Target{Kind: "Pod", Name: "api"}, localops.LogOptions{})
	if err == nil || !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("Logs(implicit context) error = %v", err)
	}
	if created {
		t.Fatal("typed client was created for an implicit context")
	}
}

func localStreamingTestAdapter(t *testing.T, client kubernetes.Interface) *Adapter {
	t.Helper()
	adapter := localTestAdapter(t, localFakeClient())
	adapter.settings.typed = func(_ *rest.Config) (kubernetes.Interface, error) { return client, nil }
	return adapter
}

func testPod(namespace, name string, containers ...string) *corev1.Pod {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	for _, name := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: name, Image: "example.invalid/test"})
	}
	return pod
}

type recordingExecutor struct {
	options remotecommand.StreamOptions
}

func (executor *recordingExecutor) Stream(options remotecommand.StreamOptions) error {
	executor.options = options
	return nil
}

func (executor *recordingExecutor) StreamWithContext(_ context.Context, options remotecommand.StreamOptions) error {
	executor.options = options
	return nil
}

type recordingPortForwarder struct {
	stop  <-chan struct{}
	ready chan struct{}
	ports []portforward.ForwardedPort
}

func (forwarder *recordingPortForwarder) ForwardPorts() error {
	close(forwarder.ready)
	<-forwarder.stop
	return nil
}

func (forwarder *recordingPortForwarder) GetPorts() ([]portforward.ForwardedPort, error) {
	return append([]portforward.ForwardedPort(nil), forwarder.ports...), nil
}
