// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/localops"
)

func TestLocalPanelsRunExplicitIOAsynchronouslyAndRestoreFleetPosition(t *testing.T) {
	t.Parallel()
	client := &tuiLocalClient{
		view: localops.ObjectView{YAML: []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: pod-0000\n")},
		logs: "alpha log line\n",
	}
	model, err := NewModelWithLocal(context.Background(), populatedStore(t, 2), &countingSyncer{}, client)
	if err != nil {
		t.Fatalf("NewModelWithLocal() error = %v", err)
	}
	model.cursor = 1

	_, command := model.Update(keyMessage("y"))
	if command == nil || client.calls() != 0 {
		t.Fatalf("yaml command/calls = %v/%d, want deferred I/O", command, client.calls())
	}
	message := command()
	_, _ = model.Update(message)
	if !strings.Contains(model.View().Content, "kind: Pod") {
		t.Fatalf("YAML panel = %q", model.View().Content)
	}
	if client.target.Context != "beta" || client.target.Name != "pod-0001" {
		t.Fatalf("local target = %#v", client.target)
	}
	_, _ = model.Update(specialKey(27))
	if model.panel != nil || model.cursor != 1 || len(model.scopes) != 0 {
		t.Fatalf("return state panel/cursor/scopes = %#v/%d/%v", model.panel, model.cursor, model.scopes)
	}

	_, command = model.Update(keyMessage("l"))
	if command == nil {
		t.Fatal("logs command = nil")
	}
	_, readCommand := model.Update(command())
	if readCommand == nil {
		t.Fatal("log reader command = nil")
	}
	_, next := model.Update(readCommand())
	if next != nil {
		_, _ = model.Update(next())
	}
	if !strings.Contains(model.View().Content, "alpha log line") {
		t.Fatalf("logs panel = %q", model.View().Content)
	}
	_, _ = model.Update(specialKey(27))
	if model.cursor != 1 || len(model.scopes) != 0 {
		t.Fatalf("logs return cursor/scopes = %d/%v", model.cursor, model.scopes)
	}
}

func TestTUIForwardPromptCreatesPersistentManagedSession(t *testing.T) {
	t.Parallel()
	client := &tuiLocalClient{}
	model, err := NewModelWithLocal(context.Background(), populatedStore(t, 2), &countingSyncer{}, client)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = model.Update(keyMessage("f"))
	_, _ = model.Update(keyMessage("18080:8080"))
	_, command := model.Update(specialKey(13))
	if command == nil {
		t.Fatal("port-forward command = nil")
	}
	_, waitCommand := model.Update(command())
	if waitCommand == nil || !strings.Contains(model.View().Content, "18080→8080") {
		t.Fatalf("port-forward panel/command = %q/%v", model.View().Content, waitCommand)
	}
	_, _ = model.Update(specialKey(27))
	if len(model.forwards) != 1 || model.panel != nil {
		t.Fatalf("persistent forwards/panel = %d/%#v", len(model.forwards), model.panel)
	}
	_, _ = model.Update(keyMessage(":"))
	_, _ = model.Update(keyMessage("pf"))
	_, _ = model.Update(specialKey(13))
	if !strings.Contains(model.View().Content, "PORT-FORWARDS") {
		t.Fatalf(":pf panel = %q", model.View().Content)
	}
	_, _ = model.Update(keyMessage("x"))
	_, _ = model.Update(waitCommand())
	if !model.forwards[0].done {
		t.Fatal("closed port-forward was not marked ended")
	}
}

func TestTUIEditCommandShowsDryRunDiffBeforeApply(t *testing.T) {
	t.Setenv("KUBE_EDITOR", "true")
	client := &tuiLocalClient{
		view: localops.ObjectView{YAML: []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: pod-0000\n  namespace: apps\n")},
		preview: localops.ApplyPreview{
			CurrentYAML: []byte("data:\n  mode: old\n"),
			DryRunYAML:  []byte("data:\n  mode: new\n"),
		},
	}
	var stdout, stderr bytes.Buffer
	command := &localEditCommand{
		ctx: context.Background(), client: client,
		target: localops.Target{Context: "alpha", Namespace: "apps", Kind: "Pod", Name: "pod-0000"},
		stdin:  &stagedConfirmReader{confirmation: []byte("yes\n")}, stdout: &stdout, stderr: &stderr,
	}
	if err := command.Run(); err != nil {
		t.Fatalf("edit Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "server dry-run") || !strings.Contains(stdout.String(), "mode: new") {
		t.Fatalf("edit output = %q", stdout.String())
	}
	if !slices.Equal(client.order, []string{"view", "preview", "apply"}) {
		t.Fatalf("edit order = %v", client.order)
	}
}

func TestTUISecretEditRefusesPlaintextTemporaryFile(t *testing.T) {
	t.Parallel()
	client := &tuiLocalClient{}
	command := &localEditCommand{
		ctx: t.Context(), client: client,
		target: localops.Target{Context: "alpha", Namespace: "apps", Kind: "Secret", Name: "credentials"},
		stdin:  strings.NewReader("yes\n"), stdout: io.Discard, stderr: io.Discard,
	}
	if err := command.Run(); err == nil || !strings.Contains(err.Error(), "interactive Secret edit is refused") {
		t.Fatalf("secret edit error = %v", err)
	}
	if len(client.order) != 0 {
		t.Fatalf("secret edit operations = %v", client.order)
	}
}

type stagedConfirmReader struct {
	mu           sync.Mutex
	editorRead   bool
	confirmation []byte
}

func (reader *stagedConfirmReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if !reader.editorRead {
		reader.editorRead = true
		return 0, io.EOF
	}
	if len(reader.confirmation) == 0 {
		return 0, io.EOF
	}
	count := copy(buffer, reader.confirmation)
	reader.confirmation = reader.confirmation[count:]
	return count, nil
}

type tuiLocalClient struct {
	mu      sync.Mutex
	count   int
	target  localops.Target
	view    localops.ObjectView
	logs    string
	preview localops.ApplyPreview
	order   []string
}

func (client *tuiLocalClient) record(target localops.Target, operation string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.count++
	client.target = target
	client.order = append(client.order, operation)
}

func (client *tuiLocalClient) calls() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.count
}

func (client *tuiLocalClient) View(_ context.Context, target localops.Target, _ bool) (localops.ObjectView, error) {
	client.record(target, "view")
	return client.view, nil
}

func (client *tuiLocalClient) Describe(_ context.Context, target localops.Target) (localops.Description, error) {
	client.record(target, "describe")
	return localops.Description{Object: client.view}, nil
}

func (client *tuiLocalClient) Logs(
	_ context.Context,
	target localops.Target,
	_ localops.LogOptions,
) (io.ReadCloser, error) {
	client.record(target, "logs")
	return io.NopCloser(strings.NewReader(client.logs)), nil
}

func (client *tuiLocalClient) Exec(
	_ context.Context,
	target localops.Target,
	_ localops.ExecOptions,
	_ localops.Streams,
) error {
	client.record(target, "exec")
	return nil
}

func (client *tuiLocalClient) PortForward(
	_ context.Context,
	request localops.ForwardRequest,
) (localops.ForwardSession, error) {
	client.record(request.Target, "port-forward")
	ready := make(chan struct{})
	close(ready)
	return &tuiForwardSession{ready: ready, done: make(chan error, 1)}, nil
}

func (client *tuiLocalClient) PreviewApply(
	_ context.Context,
	target localops.Target,
	_ []byte,
) (localops.ApplyPreview, error) {
	client.record(target, "preview")
	return client.preview, nil
}

func (client *tuiLocalClient) Apply(
	_ context.Context,
	target localops.Target,
	_ []byte,
) (fleet.Evidence, error) {
	client.record(target, "apply")
	return fleet.Evidence{Ref: fleet.ResourceRef{Scope: target.Context, Kind: target.Kind, Name: target.Name}}, nil
}

type tuiForwardSession struct {
	ready     <-chan struct{}
	done      chan error
	closeOnce sync.Once
}

func (session *tuiForwardSession) Ready() <-chan struct{} { return session.ready }
func (session *tuiForwardSession) Done() <-chan error     { return session.done }
func (*tuiForwardSession) Ports() ([]localops.ForwardedPort, error) {
	return []localops.ForwardedPort{{Local: 18080, Remote: 8080}}, nil
}
func (session *tuiForwardSession) Close() error {
	session.closeOnce.Do(func() { session.done <- nil })
	return nil
}
