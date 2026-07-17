// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestPEPHasNoNetworkOrDispatchImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read pep package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", entry.Name(), err)
			}
			for _, forbidden := range []string{"net", "os/exec", "/connector", "/hubfleet", "/localops", "/mcpserver", "/hubserver"} {
				if path == forbidden || strings.Contains(path, forbidden) {
					t.Fatalf("policy boundary imports forbidden package %q", path)
				}
			}
		}
	}
}

func TestPolicyBoundaryOmitsRawProposalAndDigestMaterial(t *testing.T) {
	assertExactFields(t, reflect.TypeFor[AuditEvent](), []string{
		"At", "TraceID", "WorkspaceID", "Actor", "Role", "Action", "Verb", "Verdict", "ReasonCode",
	})
	assertExactFields(t, reflect.TypeFor[ProposalInput](), []string{
		"intentID", "workspaceID", "actor", "verb", "target", "argumentsDigest", "resolvedDigest",
	})
}

func assertExactFields(t *testing.T, value reflect.Type, expected []string) {
	t.Helper()
	if value.NumField() != len(expected) {
		t.Fatalf("%s fields = %d, want exactly %d", value.Name(), value.NumField(), len(expected))
	}
	for _, name := range expected {
		if _, exists := value.FieldByName(name); !exists {
			t.Fatalf("%s is missing approved field %s", value.Name(), name)
		}
	}
}
