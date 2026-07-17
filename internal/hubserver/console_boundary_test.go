// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestConsoleProductionBoundaryIsReadOnlyAndStructurallyPinned(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var productionFiles []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, "console") && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			productionFiles = append(productionFiles, name)
		}
	}
	if len(productionFiles) != 1 || productionFiles[0] != "console.go" {
		t.Fatalf("console production files = %v, want [console.go]", productionFiles)
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), "console.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowedImports := map[string]bool{
		"bytes": true, "crypto/hmac": true, "crypto/rand": true, "crypto/sha256": true, "crypto/subtle": true,
		"embed": true, "encoding/base64": true, "encoding/json": true, "fmt": true,
		"html/template": true, "io": true, "io/fs": true, "net/http": true, "net/url": true, "strconv": true, "strings": true,
		"sync": true, "time": true, "unicode": true,
		"github.com/ArdurAI/sith/internal/fleet": true, "github.com/ArdurAI/sith/internal/hubfleet": true,
		"github.com/ArdurAI/sith/internal/pep": true, "github.com/ArdurAI/sith/internal/tenancy": true,
	}
	seenImports := make(map[string]bool, len(parsed.Imports))
	for _, declaration := range parsed.Imports {
		path, err := strconv.Unquote(declaration.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if !allowedImports[path] {
			t.Fatalf("console.go imports unreviewed capability %q", path)
		}
		seenImports[path] = true
	}
	if len(seenImports) != len(allowedImports) {
		t.Fatalf("console.go import set drifted: got %v", seenImports)
	}

	forbiddenCalls := map[string]bool{
		"Collect": true, "SyncOnce": true, "SyncKinds": true, "Exec": true,
		"Apply": true, "Edit": true, "PortForward": true, "WriteFile": true, "Create": true,
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && forbiddenCalls[selector.Sel.Name] {
			t.Errorf("console.go calls forbidden capability %s", selector.Sel.Name)
		}
		return true
	})

	assetEntries, err := os.ReadDir("console_assets")
	if err != nil {
		t.Fatal(err)
	}
	var assets []string
	for _, entry := range assetEntries {
		if entry.IsDir() {
			t.Fatalf("console asset directory contains nested directory %q", entry.Name())
		}
		assets = append(assets, filepath.ToSlash(entry.Name()))
	}
	wantAssets := []string{"console.css", "console.html", "console.js"}
	if strings.Join(assets, "|") != strings.Join(wantAssets, "|") {
		t.Fatalf("console assets = %v, want %v", assets, wantAssets)
	}

	runtimeSource, err := os.ReadFile(filepath.Join("..", "hubruntime", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []string{
		`hubfleet.NewCorrelator(hubfleet.CorrelatorConfig{Querier: database, PEP: enforcer})`,
		`Correlator: consoleCorrelator`,
	} {
		if strings.Count(string(runtimeSource), dependency) != 1 {
			t.Fatalf("Hub runtime does not compose exact console dependency %q once", dependency)
		}
	}
	for _, route := range []string{
		`mux.Handle("GET /v1/workspaces/{workspace}/console", http.HandlerFunc(consoleHandler.ServePage))`,
		`mux.Handle("GET /v1/workspaces/{workspace}/console/fleet", http.HandlerFunc(consoleHandler.ServeFleet))`,
		`mux.Handle("GET /v1/workspaces/{workspace}/console/correlate", http.HandlerFunc(consoleHandler.ServeCorrelation))`,
		`mux.Handle("GET /v1/console/assets/console.css", http.HandlerFunc(consoleHandler.ServeCSS))`,
		`mux.Handle("GET /v1/console/assets/console.js", http.HandlerFunc(consoleHandler.ServeJavaScript))`,
	} {
		if strings.Count(string(runtimeSource), route) != 1 {
			t.Fatalf("Hub runtime does not mount exact console route %q once", route)
		}
	}
	for _, forbidden := range []string{`mux.Handle("POST /v1/workspaces/{workspace}/console`, `mux.Handle("PUT /v1/workspaces/{workspace}/console`, `mux.Handle("DELETE /v1/workspaces/{workspace}/console`} {
		if strings.Contains(string(runtimeSource), forbidden) {
			t.Fatalf("Hub runtime mounts forbidden console mutation route %q", forbidden)
		}
	}
}
