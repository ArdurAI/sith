// SPDX-License-Identifier: Apache-2.0

package privacy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var approvedNetworkImports = map[string]map[string]bool{
	"internal/cli/mcp.go":                            {"net": true, "net/http": true},
	"internal/cli/ui.go":                             {"net": true, "net/http": true},
	"internal/connector/kubeconfig/local_streams.go": {"net/http": true, "net/url": true},
	"internal/hubserver/auth.go":                     {"net/http": true},
	"internal/hubserver/exchange.go":                 {"net": true, "net/http": true},
	"internal/hubauth/oidc.go":                       {"net": true, "net/http": true, "net/netip": true, "net/url": true},
	"internal/hubdb/app.go":                          {"net/netip": true},
	"internal/mcpserver/server.go":                   {"net": true, "net/http": true, "net/url": true},
	"internal/webui/api.go":                          {"net/http": true},
	"internal/webui/server.go":                       {"net": true, "net/http": true, "net/url": true},
}

var approvedFilesystemWrites = map[string]map[string]bool{
	"internal/cli/local.go":         {"CreateTemp": true},
	"internal/tui/local_actions.go": {"CreateTemp": true},
}

var approvedProcessImports = map[string]map[string]bool{
	"internal/cli/local.go":         {"os/exec": true},
	"internal/cli/ui.go":            {"os/exec": true},
	"internal/tui/local_actions.go": {"os/exec": true},
}

var forbiddenTelemetryPrefixes = []string{
	"go.opentelemetry.io/",
	"github.com/DataDog/",
	"github.com/amplitude/",
	"github.com/getsentry/",
	"github.com/mixpanel/",
	"github.com/newrelic/",
	"github.com/posthog/",
	"github.com/segmentio/analytics",
}

func TestProductionNetworkAndPersistenceBoundaries(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	seenNetwork := make(map[string]map[string]bool)
	seenWrites := make(map[string]map[string]bool)
	seenProcesses := make(map[string]map[string]bool)
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			reviewProductionFile(t, root, path, seenNetwork, seenWrites, seenProcesses)
			return nil
		})
		if err != nil {
			t.Fatalf("walk production source: %v", err)
		}
	}
	assertExactBoundary(t, "network import", approvedNetworkImports, seenNetwork)
	assertExactBoundary(t, "filesystem write", approvedFilesystemWrites, seenWrites)
	assertExactBoundary(t, "process import", approvedProcessImports, seenProcesses)
}

func reviewProductionFile(
	t *testing.T,
	root, path string,
	seenNetwork, seenWrites, seenProcesses map[string]map[string]bool,
) {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	relative = filepath.ToSlash(relative)
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relative, err)
	}
	aliases := make(map[string]string)
	for _, declaration := range parsed.Imports {
		importPath, err := strconv.Unquote(declaration.Path.Value)
		if err != nil {
			t.Fatalf("decode import in %s: %v", relative, err)
		}
		alias := filepath.Base(importPath)
		if declaration.Name != nil {
			alias = declaration.Name.Name
		}
		aliases[alias] = importPath
		if importPath == "net" || strings.HasPrefix(importPath, "net/") {
			if !approvedNetworkImports[relative][importPath] {
				t.Errorf("%s imports unapproved network package %q", relative, importPath)
			}
			markBoundary(seenNetwork, relative, importPath)
		}
		if importPath == "os/exec" {
			if !approvedProcessImports[relative][importPath] {
				t.Errorf("%s imports unapproved process execution package %q", relative, importPath)
			}
			markBoundary(seenProcesses, relative, importPath)
		}
		if lowLevelNetworkPackage(importPath) {
			t.Errorf("%s imports unapproved low-level network package %q", relative, importPath)
		}
		if strings.HasPrefix(importPath, "k8s.io/client-go/") &&
			!strings.HasPrefix(relative, "internal/connector/kubeconfig/") {
			t.Errorf("%s imports Kubernetes transport outside the local source adapter: %q", relative, importPath)
		}
		if strings.HasPrefix(relative, "internal/keychain/") && filesystemPackage(importPath) {
			t.Errorf("%s imports filesystem package %q; keychain custody must not have a file fallback", relative, importPath)
		}
		for _, prefix := range forbiddenTelemetryPrefixes {
			if strings.HasPrefix(importPath, prefix) {
				t.Errorf("%s imports forbidden local-mode telemetry package %q", relative, importPath)
			}
		}
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok || aliases[identifier.Name] != "os" || !filesystemWriteCall(selector.Sel.Name) {
			return true
		}
		if !approvedFilesystemWrites[relative][selector.Sel.Name] {
			t.Errorf("%s calls unapproved filesystem write os.%s", relative, selector.Sel.Name)
		}
		markBoundary(seenWrites, relative, selector.Sel.Name)
		return true
	})
}

func filesystemPackage(importPath string) bool {
	switch importPath {
	case "io/fs", "os", "path", "path/filepath":
		return true
	default:
		return false
	}
}

func lowLevelNetworkPackage(importPath string) bool {
	return importPath == "syscall" || strings.HasPrefix(importPath, "golang.org/x/net/") ||
		strings.HasPrefix(importPath, "google.golang.org/grpc") || strings.HasPrefix(importPath, "github.com/quic-go/")
}

func filesystemWriteCall(name string) bool {
	switch name {
	case "Create", "CreateTemp", "Mkdir", "MkdirAll", "OpenFile", "Rename", "WriteFile":
		return true
	default:
		return false
	}
}

func markBoundary(seen map[string]map[string]bool, path, operation string) {
	if seen[path] == nil {
		seen[path] = make(map[string]bool)
	}
	seen[path][operation] = true
}

func assertExactBoundary(t *testing.T, name string, expected, seen map[string]map[string]bool) {
	t.Helper()
	for path, operations := range expected {
		for operation := range operations {
			if !seen[path][operation] {
				t.Errorf("approved %s %s:%s was not observed", name, path, operation)
			}
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve privacy test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
