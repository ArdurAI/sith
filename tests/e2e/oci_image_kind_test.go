// SPDX-License-Identifier: Apache-2.0
//go:build e2e && kind

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func TestKindOCIImageContract(t *testing.T) {
	kindBinary := environmentOr("KIND_BIN", "kind")
	if _, err := exec.LookPath(kindBinary); err != nil {
		t.Fatalf("find kind binary %q: %v", kindBinary, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	assertContainerfileContract(t, root)
	image := buildOCIImage(ctx, t, root, runtime.GOARCH)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	clusters := []string{"sith-oci-a-" + suffix, "sith-oci-b-" + suffix}
	created := make([]string, 0, len(clusters))
	t.Cleanup(func() {
		for _, name := range created {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_ = exec.CommandContext(cleanupCtx, kindBinary, "delete", "cluster", "--name", name).Run()
			cleanupCancel()
		}
	})

	for _, name := range clusters {
		created = append(created, name)
		runCommand(ctx, t, "", kindBinary, "create", "cluster", "--name", name, "--image", defaultKindNodeImage, "--wait", "180s")
		runCommand(ctx, t, "", kindBinary, "load", "docker-image", image, "--name", name)
		assertOCIImageJob(ctx, t, kindBinary, name, image)
	}
}

func assertOCIImageJob(ctx context.Context, t *testing.T, kindBinary, clusterName, image string) {
	t.Helper()
	kubeconfig := runCommand(ctx, t, "", kindBinary, "get", "kubeconfig", "--name", clusterName)
	configuration, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		t.Fatalf("parse kind kubeconfig for %s: %v", clusterName, err)
	}
	client, err := kubernetes.NewForConfig(configuration)
	if err != nil {
		t.Fatalf("construct kind client for %s: %v", clusterName, err)
	}
	nonRoot, readOnly, noPrivilegeEscalation, automountToken := true, true, false, false
	runAsUser := int64(65532)
	backoffLimit := int32(0)
	job, err := client.BatchV1().Jobs("default").Create(ctx, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "sith-oci-contract"},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				AutomountServiceAccountToken: &automountToken,
				RestartPolicy:                corev1.RestartPolicyNever,
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: &nonRoot, RunAsUser: &runAsUser,
					SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
				Containers: []corev1.Container{{
					Name: "sith", Image: image, ImagePullPolicy: corev1.PullNever,
					Args: []string{"version", "--output", "json"},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: &noPrivilegeEscalation, ReadOnlyRootFilesystem: &readOnly,
						Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					},
				}},
			}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create hardened OCI Job on %s: %v", clusterName, err)
	}

	if err := wait.PollUntilContextTimeout(ctx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := client.BatchV1().Jobs("default").Get(ctx, job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if current.Status.Failed > 0 {
			return false, fmt.Errorf("OCI Job failed on %s", clusterName)
		}
		return current.Status.Succeeded == 1, nil
	}); err != nil {
		t.Fatalf("wait for hardened OCI Job on %s: %v", clusterName, err)
	}
	pods, err := client.CoreV1().Pods("default").List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + job.Name})
	if err != nil {
		t.Fatalf("list OCI Job Pods on %s: %v", clusterName, err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("find OCI Job Pod on %s: %#v", clusterName, pods.Items)
	}
	output, err := client.CoreV1().Pods("default").GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).Do(ctx).Raw()
	if err != nil || !json.Valid(output) || !strings.Contains(string(output), "\"version\"") {
		t.Fatalf("OCI Job output on %s = %q / %v", clusterName, output, err)
	}
}
