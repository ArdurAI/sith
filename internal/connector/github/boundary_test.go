// SPDX-License-Identifier: Apache-2.0

package github

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

var allowedProductionImports = map[string]bool{
	"bytes":                                  true,
	"encoding/json":                          true,
	"fmt":                                    true,
	"io":                                     true,
	"strconv":                                true,
	"strings":                                true,
	"time":                                   true,
	"unicode":                                true,
	"unicode/utf8":                           true,
	"github.com/ArdurAI/sith/internal/fleet": true,
}

var allowedProductionDeclarations = map[string]bool{
	"APIVersion":               true,
	"Kind":                     true,
	"ProtocolVersion":          true,
	"Projection":               true,
	"ProjectMergedPullRequest": true,
	"UnmarshalJSON":            true,
	"changeObservation":        true,
	"consumeUniqueJSON":        true,
	"matchingDelimiter":        true,
	"maxClockSkew":             true,
	"maxFactPayloadBytes":      true,
	"maxHostBytes":             true,
	"maxJSONDepth":             true,
	"maxOwnerBytes":            true,
	"maxRepositoryBytes":       true,
	"maxResourceName":          true,
	"maxResponseBytes":         true,
	"maxWorkspaceBytes":        true,
	"mergedObservation":        true,
	"pullCommit":               true,
	"pullResponse":             true,
	"rejectDuplicateJSON":      true,
	"requiredCommitSHA":        true,
	"validCommitSHA":           true,
	"validateHost":             true,
	"validatePathComponent":    true,
	"validateProjection":       true,
	"validateResponseIdentity": true,
	"validateText":             true,
}

func TestProjectorHasNoIOCredentialPersistenceOrMutationSeam(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read github package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import: %v", err)
			}
			if !allowedProductionImports[path] {
				t.Fatalf("projector imports unreviewed package %q", path)
			}
		}
		for _, node := range file.Decls {
			switch declaration := node.(type) {
			case *ast.FuncDecl:
				if !allowedProductionDeclarations[declaration.Name.Name] {
					t.Errorf("projector declares unreviewed function or method %s", declaration.Name.Name)
				}
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					switch typed := specification.(type) {
					case *ast.TypeSpec:
						if !allowedProductionDeclarations[typed.Name.Name] {
							t.Errorf("projector declares unreviewed type %s", typed.Name.Name)
						}
					case *ast.ValueSpec:
						for _, name := range typed.Names {
							if !allowedProductionDeclarations[name.Name] {
								t.Errorf("projector declares unreviewed value %s", name.Name)
							}
						}
					}
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if _, ok := node.(*ast.InterfaceType); ok {
				t.Errorf("projector declares an injected interface seam")
			}
			return true
		})
	}
}
