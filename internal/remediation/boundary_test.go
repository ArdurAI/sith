// SPDX-License-Identifier: Apache-2.0

package remediation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

var allowedProductionImports = map[string]bool{
	"context":                                true,
	"crypto/sha1":                            true,
	"crypto/sha256":                          true,
	"encoding/hex":                           true,
	"encoding/json":                          true,
	"fmt":                                    true,
	"path":                                   true,
	"reflect":                                true,
	"slices":                                 true,
	"sort":                                   true,
	"strings":                                true,
	"time":                                   true,
	"unicode":                                true,
	"unicode/utf8":                           true,
	"github.com/ArdurAI/sith/internal/brain": true,
	"github.com/ArdurAI/sith/internal/connector":  true,
	"github.com/ArdurAI/sith/internal/fleet":      true,
	"github.com/ArdurAI/sith/internal/intent":     true,
	"github.com/ArdurAI/sith/internal/intentargs": true,
	"github.com/ArdurAI/sith/internal/tenancy":    true,
}

func TestRemediationPackageTreeHasNoIOAuthorityOrPolicyImports(t *testing.T) {
	t.Parallel()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if !allowedProductionImports[importPath] {
				t.Errorf("remediation production file %s imports unreviewed package %q", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk remediation package tree: %v", err)
	}
}

func TestGitOpsBoundaryOmitsAuthorityAndKeepsBundleOpaque(t *testing.T) {
	t.Parallel()
	assertExactFields(t, reflect.TypeFor[GitOpsProvenanceInput](), []string{
		"Workspace", "Subject", "Sources", "ObservedAt", "ValidUntil", "Handler", "Repository",
		"BaseRef", "BaseCommit", "FilePath", "ObservedBlobSHA", "DesiredContent", "Title", "Body",
		"CommitMessage", "EvidenceRefs",
	})
	assertExactFields(t, reflect.TypeFor[Resolution](), []string{
		"Status", "Target", "Arguments", "ArgumentsDigest", "EvidenceRefs", "Reasons",
	})
	assertExactFields(t, reflect.TypeFor[GitOpsResolver](), []string{"handler", "now"})

	bundle := reflect.TypeFor[GitOpsProvenanceBundle]()
	if bundle.NumField() != 17 {
		t.Fatalf("GitOpsProvenanceBundle fields = %d, want exact reviewed shape", bundle.NumField())
	}
	for index := range bundle.NumField() {
		field := bundle.Field(index)
		if field.IsExported() {
			t.Fatalf("GitOpsProvenanceBundle exposes mutable field %s", field.Name)
		}
	}

	for _, value := range []reflect.Type{
		reflect.TypeFor[GitOpsProvenanceInput](), reflect.TypeFor[GitOpsProvenanceBundle](), reflect.TypeFor[Resolution](),
	} {
		for index := range value.NumField() {
			name := strings.ToLower(value.Field(index).Name)
			for _, forbidden := range []string{"actor", "role", "intent", "approval", "policy", "credential", "token", "secret", "signature", "endpoint"} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s exposes forbidden authority field %s", value.Name(), value.Field(index).Name)
				}
			}
		}
	}
}

func TestGitSourceSnapshotBoundaryIsObservedOnlyAndOpaque(t *testing.T) {
	t.Parallel()
	assertExactFields(t, reflect.TypeFor[GitSourceSnapshotInput](), []string{
		"Workspace", "Subject", "Sources", "ObservedAt", "ValidUntil", "Repository", "BaseRef",
		"BaseCommit", "FilePath", "ObservedBlobSHA", "CurrentContent", "EvidenceRefs",
	})

	snapshot := reflect.TypeFor[GitSourceSnapshot]()
	if snapshot.NumField() != 13 {
		t.Fatalf("GitSourceSnapshot fields = %d, want exact reviewed shape", snapshot.NumField())
	}
	for index := range snapshot.NumField() {
		if field := snapshot.Field(index); field.IsExported() {
			t.Fatalf("GitSourceSnapshot exposes mutable field %s", field.Name)
		}
	}
	if snapshot.NumMethod() != 2 || snapshot.Method(0).Name != "Freshness" || snapshot.Method(1).Name != "Version" {
		t.Fatalf("GitSourceSnapshot methods = %#v, want only Freshness and Version", snapshot)
	}

	for _, value := range []reflect.Type{reflect.TypeFor[GitSourceSnapshotInput](), snapshot} {
		for index := range value.NumField() {
			name := strings.ToLower(value.Field(index).Name)
			for _, forbidden := range []string{
				"desired", "title", "body", "commitmessage", "handler", "actor", "role", "intent",
				"approval", "policy", "credential", "token", "secret", "signature", "endpoint", "dispatch",
			} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s exposes forbidden change or authority field %s", value.Name(), value.Field(index).Name)
				}
			}
		}
	}
}

func TestDesiredChangeBoundaryIsTransformerOwnedAndOpaque(t *testing.T) {
	t.Parallel()
	change := reflect.TypeFor[DesiredChange]()
	if change.NumField() != 5 {
		t.Fatalf("DesiredChange fields = %d, want exact reviewed shape", change.NumField())
	}
	for index := range change.NumField() {
		field := change.Field(index)
		if field.IsExported() {
			t.Fatalf("DesiredChange exposes mutable field %s", field.Name)
		}
		name := strings.ToLower(field.Name)
		for _, forbidden := range []string{
			"title", "body", "commitmessage", "handler", "actor", "role", "intent", "approval",
			"policy", "credential", "token", "secret", "signature", "endpoint", "dispatch", "execute",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("DesiredChange exposes forbidden authority field %s", field.Name)
			}
		}
	}
	if change.NumMethod() != 1 || change.Method(0).Name != "Version" {
		t.Fatalf("DesiredChange methods = %#v, want only Version", change)
	}

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, rawDeclaration := range file.Decls {
			declaration, ok := rawDeclaration.(*ast.FuncDecl)
			if ok && declaration.Recv == nil && token.IsExported(declaration.Name.Name) &&
				returnsDesiredChange(declaration) {
				t.Errorf("DesiredChange construction escaped the reviewed package boundary as %s in %s", declaration.Name.Name, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect DesiredChange package boundary: %v", err)
	}
}

func returnsDesiredChange(declaration *ast.FuncDecl) bool {
	if declaration.Type.Results == nil {
		return false
	}
	found := false
	for _, result := range declaration.Type.Results.List {
		ast.Inspect(result.Type, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && identifier.Name == "DesiredChange" {
				found = true
				return false
			}
			return !found
		})
	}
	return found
}

func assertExactFields(t *testing.T, value reflect.Type, expected []string) {
	t.Helper()
	if value.NumField() != len(expected) {
		t.Fatalf("%s fields = %d, want exactly %d", value.Name(), value.NumField(), len(expected))
	}
	for index, name := range expected {
		if value.Field(index).Name != name {
			t.Fatalf("%s field %d = %s, want %s", value.Name(), index, value.Field(index).Name, name)
		}
	}
}
