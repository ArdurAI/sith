// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestDirectoryImportPreservesDuplicateContextNames(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	writeDirectoryConfig(t, filepath.Join(directory, "alpha.yaml"), "shared", "https://alpha.invalid")
	writeDirectoryConfig(t, filepath.Join(directory, "nested", "beta.yaml"), "shared", "https://beta.invalid")

	var mu sync.Mutex
	probed := make([]string, 0, 2)
	adapter, err := New(
		WithDirectory(directory),
		withProbe(func(_ context.Context, config *rest.Config) error {
			mu.Lock()
			defer mu.Unlock()
			probed = append(probed, config.Host)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	discovery, err := adapter.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.Scopes) != 2 || len(discovery.Unreachable) != 0 || len(discovery.Diagnostics) != 0 {
		t.Fatalf("Discovery = %#v, want two clean imported scopes", discovery)
	}
	slices.Sort(probed)
	if !slices.Equal(probed, []string{"https://alpha.invalid", "https://beta.invalid"}) {
		t.Fatalf("probed hosts = %v", probed)
	}
	origins := make([]string, 0, len(discovery.Scopes))
	names := make([]string, 0, len(discovery.Scopes))
	for _, scope := range discovery.Scopes {
		if scope.DisplayName != "shared" || !scope.Reachable || strings.Contains(scope.Origin, directory) {
			t.Fatalf("scope = %#v, want reachable relative-name metadata", scope)
		}
		origins = append(origins, scope.Origin)
		names = append(names, scope.Name)
	}
	slices.Sort(origins)
	if !slices.Equal(origins, []string{"alpha.yaml", "nested/beta.yaml"}) || names[0] == names[1] {
		t.Fatalf("origins/names = %v/%v, want distinct imported contexts", origins, names)
	}
}

func TestDirectoryImportSurfacesSafeDiagnostics(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	writeDirectoryConfig(t, filepath.Join(directory, "valid.yaml"), "alpha", "https://alpha.invalid")
	secret := "not-a-real-secret-but-must-not-leak"
	if err := os.WriteFile(filepath.Join(directory, "broken.yaml"), []byte("clusters: ["+secret), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	writeDirectoryConfig(t, outside, "outside", "https://outside.invalid")
	if err := os.Symlink(outside, filepath.Join(directory, "linked.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "large.yaml"), make([]byte, maxImportBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter, err := New(WithDirectory(directory), withProbe(func(_ context.Context, _ *rest.Config) error { return nil }))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	discovery, err := adapter.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.Scopes) != 1 || len(discovery.Diagnostics) != 3 {
		t.Fatalf("Discovery = %#v, want one valid scope and three diagnostics", discovery)
	}
	payload, err := json.Marshal(discovery)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{directory, outside, secret} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("safe discovery metadata leaked %q: %s", forbidden, payload)
		}
	}
	for _, source := range []string{"broken.yaml", "large.yaml", "linked.yaml"} {
		if !strings.Contains(string(payload), source) {
			t.Errorf("diagnostics = %s, want source %q", payload, source)
		}
	}
}

func TestDirectoryImportRejectsUnsafeRoots(t *testing.T) {
	t.Parallel()
	regularFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(regularFile, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(t.TempDir(), linkedRoot); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	for _, path := range []string{"", regularFile, linkedRoot, missing} {
		if _, err := New(WithDirectory(path)); err == nil {
			t.Errorf("New(WithDirectory(%q)) error = nil, want unsafe root refusal", path)
		} else if path != "" && strings.Contains(err.Error(), path) {
			t.Errorf("New(WithDirectory(%q)) exposed the path in %q", path, err)
		}
	}
}

func TestDirectoryImportBoundsAllTraversalEntries(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(target, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxImportFiles; index++ {
		path := filepath.Join(directory, fmt.Sprintf("entry-%03d", index))
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	imported, err := loadDirectory(directory)
	if err != nil {
		t.Fatalf("loadDirectory() error = %v", err)
	}
	limitReported := false
	for _, diagnostic := range imported.diagnostics {
		limitReported = limitReported || (diagnostic.Source == "" && diagnostic.Message == "kubeconfig entry limit reached")
	}
	if len(imported.diagnostics) != maxImportFiles+1 || !limitReported {
		t.Fatalf("diagnostics = %#v, want bounded traversal diagnostic", imported.diagnostics)
	}
}

func TestDirectoryImportRejectsRootReplacementBeforeOpen(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	directory := filepath.Join(parent, "selected")
	writeDirectoryConfig(t, filepath.Join(directory, "original.yaml"), "original", "https://original.invalid")

	imported, err := loadDirectoryWithHooks(directory, directoryImportHooks{
		afterRootInspection: func() {
			if err := os.Rename(directory, filepath.Join(parent, "selected-original")); err != nil {
				t.Fatal(err)
			}
			writeDirectoryConfig(t, filepath.Join(directory, "replacement.yaml"), "replacement", "https://replacement.invalid")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "changed during import") {
		t.Fatalf("loadDirectoryWithHooks(root replacement) = %#v, %v, want identity rejection", imported, err)
	}
	if strings.Contains(err.Error(), parent) {
		t.Fatalf("root replacement error exposed absolute path: %q", err)
	}
}

func TestDirectoryImportStaysOnOpenedRootAfterPathReplacement(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	directory := filepath.Join(parent, "selected")
	writeDirectoryConfig(t, filepath.Join(directory, "original.yaml"), "original", "https://original.invalid")

	imported, err := loadDirectoryWithHooks(directory, directoryImportHooks{
		afterRootOpen: func() {
			if err := os.Rename(directory, filepath.Join(parent, "selected-original")); err != nil {
				t.Fatal(err)
			}
			writeDirectoryConfig(t, filepath.Join(directory, "replacement.yaml"), "replacement", "https://replacement.invalid")
		},
	})
	if err != nil {
		t.Fatalf("loadDirectoryWithHooks(open-root replacement) error = %v", err)
	}
	if len(imported.raw.Contexts) != 1 || len(imported.metadata) != 1 || len(imported.diagnostics) != 0 {
		t.Fatalf("opened-root import = %#v, want only original descriptor-backed config", imported)
	}
	for _, metadata := range imported.metadata {
		if metadata.displayName != "original" || metadata.origin != "original.yaml" {
			t.Fatalf("opened-root metadata = %#v, want original config", metadata)
		}
	}
}

func TestDirectoryImportRejectsRegularFileReplacement(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	parked := filepath.Join(directory, "config-original.yaml")
	writeDirectoryConfig(t, path, "original", "https://original.invalid")

	imported, err := loadDirectoryWithHooks(directory, directoryImportHooks{
		beforeFileOpen: func(relative string) {
			if relative != "config.yaml" {
				return
			}
			if err := os.Rename(path, parked); err != nil {
				t.Fatal(err)
			}
			writeDirectoryConfig(t, path, "replacement", "https://replacement.invalid")
		},
	})
	if err != nil {
		t.Fatalf("loadDirectoryWithHooks(file replacement) error = %v", err)
	}
	assertRejectedRacyImport(t, imported, "config.yaml", directory)
}

func TestDirectoryImportRejectsExternalSymlinkSwap(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	parked := filepath.Join(directory, "config-original.yaml")
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	writeDirectoryConfig(t, path, "original", "https://original.invalid")
	writeDirectoryConfig(t, outside, "outside", "https://outside.invalid")

	imported, err := loadDirectoryWithHooks(directory, directoryImportHooks{
		beforeFileOpen: func(relative string) {
			if relative != "config.yaml" {
				return
			}
			if err := os.Rename(path, parked); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
		},
	})
	if err != nil {
		t.Fatalf("loadDirectoryWithHooks(symlink swap) error = %v", err)
	}
	assertRejectedRacyImport(t, imported, "config.yaml", directory, outside)
}

func TestDirectoryImportRejectsDeferredLocalFileReferences(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*clientcmdapi.Config){
		"certificate authority": func(config *clientcmdapi.Config) {
			config.Clusters["alpha"].CertificateAuthority = "ca.crt"
		},
		"client certificate": func(config *clientcmdapi.Config) {
			config.AuthInfos["alpha"].ClientCertificate = "client.crt"
		},
		"client key": func(config *clientcmdapi.Config) {
			config.AuthInfos["alpha"].ClientKey = "client.key"
		},
		"token file": func(config *clientcmdapi.Config) {
			config.AuthInfos["alpha"].TokenFile = "token"
		},
		"path-based exec": func(config *clientcmdapi.Config) {
			config.AuthInfos["alpha"].Exec = &clientcmdapi.ExecConfig{Command: "plugins/auth"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			config := directoryTestConfig("alpha", "https://alpha.invalid")
			mutate(config)
			if err := clientcmd.WriteToFile(*config, filepath.Join(directory, "config.yaml")); err != nil {
				t.Fatal(err)
			}
			imported, err := loadDirectory(directory)
			if err != nil {
				t.Fatalf("loadDirectory() error = %v", err)
			}
			assertRejectedRacyImport(t, imported, "config.yaml", directory)
		})
	}
}

func TestDirectoryImportAllowsPathResolvedExecCommand(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	config := directoryTestConfig("alpha", "https://alpha.invalid")
	config.AuthInfos["alpha"].Exec = &clientcmdapi.ExecConfig{Command: "cloud-auth"}
	if err := clientcmd.WriteToFile(*config, filepath.Join(directory, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	imported, err := loadDirectory(directory)
	if err != nil {
		t.Fatalf("loadDirectory() error = %v", err)
	}
	if len(imported.raw.Contexts) != 1 || len(imported.diagnostics) != 0 {
		t.Fatalf("PATH exec import = %#v, want accepted config", imported)
	}
}

func TestDirectoryImportRejectsReplacedAncestorEscape(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	nested := filepath.Join(directory, "nested")
	parked := filepath.Join(directory, "nested-original")
	outsideDirectory := t.TempDir()
	outside := filepath.Join(outsideDirectory, "config.yaml")
	writeDirectoryConfig(t, filepath.Join(nested, "config.yaml"), "original", "https://original.invalid")
	writeDirectoryConfig(t, outside, "outside", "https://outside.invalid")

	imported, err := loadDirectoryWithHooks(directory, directoryImportHooks{
		beforeFileOpen: func(relative string) {
			if relative != "nested/config.yaml" {
				return
			}
			if err := os.Rename(nested, parked); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outsideDirectory, nested); err != nil {
				t.Fatal(err)
			}
		},
	})
	if err != nil {
		t.Fatalf("loadDirectoryWithHooks(ancestor replacement) error = %v", err)
	}
	assertRejectedRacyImport(t, imported, "nested/config.yaml", directory, outsideDirectory, outside)
}

func assertRejectedRacyImport(t *testing.T, imported importedConfig, source string, forbidden ...string) {
	t.Helper()
	if len(imported.raw.Contexts) != 0 || len(imported.diagnostics) != 1 ||
		imported.diagnostics[0] != importDiagnostic(source, "invalid kubeconfig") {
		t.Fatalf("racy import = %#v, want one relative rejection diagnostic", imported)
	}
	payload, err := json.Marshal(imported.diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range forbidden {
		if strings.Contains(string(payload), value) {
			t.Fatalf("racy import diagnostic leaked %q: %s", value, payload)
		}
	}
}

func TestDirectoryImportConflictsWithExplicitSource(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if _, err := New(WithExplicitPath(filepath.Join(directory, "config")), WithDirectory(directory)); err == nil {
		t.Fatal("New() error = nil, want explicit-path/directory conflict")
	}
	if _, err := New(WithLoadingRules(&clientcmd.ClientConfigLoadingRules{}), WithDirectory(directory)); err == nil {
		t.Fatal("New() error = nil, want custom-rules/directory conflict")
	}
}

func TestStandardKubeconfigUsesContextNameAsDisplayLabel(t *testing.T) {
	t.Parallel()
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha"))),
		withProbe(func(_ context.Context, _ *rest.Config) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := adapter.Discover(t.Context())
	if err != nil || len(discovery.Scopes) != 1 {
		t.Fatalf("Discover() = %#v, %v", discovery, err)
	}
	if scope := discovery.Scopes[0]; scope.Name != "alpha" || scope.DisplayName != "alpha" || scope.Origin != "" {
		t.Fatalf("standard scope = %#v", scope)
	}
}

func TestDirectoryImportSkipsBrokenContextReferences(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	config := clientcmdapi.NewConfig()
	config.Clusters["alpha"] = &clientcmdapi.Cluster{Server: "https://alpha.invalid"}
	config.AuthInfos["alpha"] = &clientcmdapi.AuthInfo{}
	config.Contexts["alpha"] = &clientcmdapi.Context{Cluster: "alpha", AuthInfo: "alpha"}
	config.Contexts["missing-cluster"] = &clientcmdapi.Context{Cluster: "gone", AuthInfo: "alpha"}
	config.Contexts["missing-user"] = &clientcmdapi.Context{Cluster: "alpha", AuthInfo: "gone"}
	path := filepath.Join(directory, "mixed.yaml")
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatal(err)
	}
	probes := 0
	adapter, err := New(WithDirectory(directory), withProbe(func(_ context.Context, _ *rest.Config) error {
		probes++
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := adapter.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Scopes) != 1 || probes != 1 {
		t.Fatalf("discovery/probes = %#v/%d, want one valid context only", discovery, probes)
	}
	messages := make([]string, 0, len(discovery.Diagnostics))
	for _, diagnostic := range discovery.Diagnostics {
		if diagnostic.Source != "mixed.yaml" {
			t.Fatalf("diagnostic = %#v, want relative source", diagnostic)
		}
		messages = append(messages, diagnostic.Message)
	}
	slices.Sort(messages)
	if !slices.Equal(messages, []string{"context references an unavailable cluster", "context references an unavailable user"}) {
		t.Fatalf("diagnostics = %v", messages)
	}
}

func writeDirectoryConfig(t *testing.T, path, contextName, host string) {
	t.Helper()
	config := directoryTestConfig(contextName, host)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatalf("write kubeconfig %s: %v", path, err)
	}
}

func directoryTestConfig(contextName, host string) *clientcmdapi.Config {
	config := clientcmdapi.NewConfig()
	config.Clusters[contextName] = &clientcmdapi.Cluster{Server: host}
	config.AuthInfos[contextName] = &clientcmdapi.AuthInfo{}
	config.Contexts[contextName] = &clientcmdapi.Context{Cluster: contextName, AuthInfo: contextName}
	return config
}
