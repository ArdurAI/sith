// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/keychain"
	"github.com/ArdurAI/sith/internal/logging"
)

func TestServeMCPRunsLoopbackProtocolAndStopsCleanly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := &lockedBuffer{}
	command := mcpTestCommand(ctx, t, stdout)
	done := make(chan error, 1)
	go func() {
		done <- runMCPServer(ctx, command, &cacheReader{}, nil, &serveOptions{
			mcp: true, address: "127.0.0.1", port: 0, tokenTTL: 15 * time.Minute,
		})
	}()
	endpoint := waitForMCPEndpoint(t, stdout)
	session := connectCLIMCPClient(t, endpoint, "")
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 4 {
		t.Fatalf("ListTools() = %#v, %v", listed, err)
	}
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "fleet.correlate", Arguments: map[string]any{"expression": "deploy/payments status!=Healthy"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMCPServer() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MCP server did not stop after cancellation")
	}
}

func TestServeMCPUsesAndDeletesKeychainTokenWithoutPrintingIt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := &lockedBuffer{}
	secrets := newFakeKeychain()
	command := mcpTestCommand(ctx, t, stdout)
	done := make(chan error, 1)
	go func() {
		done <- runMCPServer(ctx, command, &cacheReader{}, secrets, &serveOptions{
			mcp: true, address: "127.0.0.1", port: 0, requireToken: true, tokenTTL: 15 * time.Minute,
		})
	}()
	endpoint := waitForMCPEndpoint(t, stdout)
	key, token := secrets.onlyEntry(t)
	if strings.Contains(stdout.String(), token) || !strings.Contains(stdout.String(), key) {
		t.Fatalf("server output exposed token or omitted key: %q", stdout.String())
	}
	session := connectCLIMCPClient(t, endpoint, token)
	if _, err := session.ListTools(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Get(t.Context(), key); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("token cleanup error = %v", err)
	}
}

func TestMCPTokenCommandRequiresExplicitKeyAndRevealsOnlyThatSecret(t *testing.T) {
	t.Parallel()
	secrets := newFakeKeychain()
	if err := secrets.Put(t.Context(), "mcp/session/example", []byte("expected-token")); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	command := newMCPTokenCommand(secrets)
	command.SetOut(&stdout)
	command.SetArgs([]string{"--key", "mcp/session/example"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "expected-token\n" {
		t.Fatalf("token output = %q", stdout.String())
	}
}

func TestServeMCPRefusesNonLoopbackAndMissingMode(t *testing.T) {
	t.Parallel()
	command := newServeCommand(&cacheReader{}, nil)
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs(nil)
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "requires --mcp") {
		t.Fatalf("missing mode error = %v", err)
	}
	command = newServeCommand(&cacheReader{}, nil)
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--mcp", "--address", "0.0.0.0"})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("external bind error = %v", err)
	}
}

func mcpTestCommand(ctx context.Context, t *testing.T, output io.Writer) *cobra.Command {
	t.Helper()
	logger, err := logging.New(io.Discard, "error", "text")
	if err != nil {
		t.Fatal(err)
	}
	command := &cobra.Command{}
	command.SetOut(output)
	command.SetErr(io.Discard)
	command.SetContext(context.WithValue(ctx, runtimeKey{}, runtimeState{logger: logger}))
	return command
}

var endpointPattern = regexp.MustCompile(`sith MCP listening on (http://[^\s]+/mcp)`)

func waitForMCPEndpoint(t *testing.T, output *lockedBuffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if match := endpointPattern.FindStringSubmatch(output.String()); len(match) == 2 {
			return match[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("MCP endpoint was not printed: %q", output.String())
	return ""
}

func connectCLIMCPClient(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	transport := http.RoundTripper(&http.Transport{Proxy: nil})
	if token != "" {
		base := transport
		transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			clone := request.Clone(request.Context())
			clone.Header.Set("Authorization", "Bearer "+token)
			return base.RoundTrip(clone)
		})
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: &http.Client{Transport: transport, Timeout: 5 * time.Second},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(payload)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type fakeKeychain struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newFakeKeychain() *fakeKeychain {
	return &fakeKeychain{values: make(map[string][]byte)}
}

func (store *fakeKeychain) Put(_ context.Context, key string, secret []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[key] = append([]byte(nil), secret...)
	return nil
}

func (store *fakeKeychain) Get(_ context.Context, key string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	secret, exists := store.values[key]
	if !exists {
		return nil, keychain.ErrNotFound
	}
	return append([]byte(nil), secret...), nil
}

func (store *fakeKeychain) Delete(_ context.Context, key string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.values[key]; !exists {
		return keychain.ErrNotFound
	}
	delete(store.values, key)
	return nil
}

func (store *fakeKeychain) onlyEntry(t *testing.T) (string, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		for key, value := range store.values {
			store.mu.Unlock()
			return key, string(value)
		}
		store.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("keychain token was not stored")
	return "", ""
}
