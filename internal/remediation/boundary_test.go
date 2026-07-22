// SPDX-License-Identifier: Apache-2.0

package remediation

import (
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
	"crypto/sha256":                          true,
	"encoding/hex":                           true,
	"encoding/json":                          true,
	"fmt":                                    true,
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
