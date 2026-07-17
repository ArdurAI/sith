// SPDX-License-Identifier: Apache-2.0

package elasticsearch

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
	"crypto/sha256":                          true,
	"encoding/hex":                           true,
	"encoding/json":                          true,
	"fmt":                                    true,
	"io":                                     true,
	"sort":                                   true,
	"strings":                                true,
	"time":                                   true,
	"unicode":                                true,
	"unicode/utf8":                           true,
	"github.com/ArdurAI/sith/internal/fleet": true,
}

var allowedProductionDeclarations = map[string]bool{
	"func:ProjectLogCauses":               true,
	"func:buildFact":                      true,
	"func:classifyMessage":                true,
	"func:consumeUniqueJSON":              true,
	"func:containsAny":                    true,
	"func:decodeOptionalField":            true,
	"func:indicatesDependencyFailure":     true,
	"func:indicatesMissingConfig":         true,
	"func:indicatesPanic":                 true,
	"func:matchingDelimiter":              true,
	"func:objectFields":                   true,
	"func:optionalSingleString":           true,
	"func:rejectDuplicateJSON":            true,
	"func:requiredSingleMessage":          true,
	"func:requiredSingleString":           true,
	"func:validateHit":                    true,
	"func:validateProjection":             true,
	"func:validateSearchResponse":         true,
	"func:validateText":                   true,
	"method:hitEnvelope.UnmarshalJSON":    true,
	"method:searchHit.UnmarshalJSON":      true,
	"method:searchResponse.UnmarshalJSON": true,
	"method:shardSummary.UnmarshalJSON":   true,
	"type:Projection":                     true,
	"type:causeAggregate":                 true,
	"type:causeObservation":               true,
	"type:hitEnvelope":                    true,
	"type:searchHit":                      true,
	"type:searchResponse":                 true,
	"type:shardSummary":                   true,
	"value:Kind":                          true,
	"value:ProtocolVersion":               true,
	"value:allowedHitFields":              true,
	"value:clusterField":                  true,
	"value:containerField":                true,
	"value:maxCauseFacts":                 true,
	"value:maxClockSkew":                  true,
	"value:maxFactPayloadBytes":           true,
	"value:maxHits":                       true,
	"value:maxIdentityText":               true,
	"value:maxJSONDepth":                  true,
	"value:maxMessageBytes":               true,
	"value:maxQueryWindow":                true,
	"value:maxResponseBytes":              true,
	"value:messageField":                  true,
	"value:namespaceField":                true,
	"value:podField":                      true,
	"value:timestampField":                true,
}

func functionDeclarationKey(declaration *ast.FuncDecl) string {
	if declaration.Recv == nil {
		return "func:" + declaration.Name.Name
	}
	if len(declaration.Recv.List) != 1 {
		return "method:<invalid>." + declaration.Name.Name
	}
	receiver := declaration.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	identifier, ok := receiver.(*ast.Ident)
	if !ok {
		return "method:<invalid>." + declaration.Name.Name
	}
	return "method:" + identifier.Name + "." + declaration.Name.Name
}

func TestProjectorHasNoIOCredentialPersistenceOrMutationSeam(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read elasticsearch package: %v", err)
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
				key := functionDeclarationKey(declaration)
				if !allowedProductionDeclarations[key] {
					t.Errorf("projector declares unreviewed function or method %s", key)
				}
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					switch typed := specification.(type) {
					case *ast.TypeSpec:
						key := "type:" + typed.Name.Name
						if !allowedProductionDeclarations[key] {
							t.Errorf("projector declares unreviewed type %s", key)
						}
					case *ast.ValueSpec:
						for _, name := range typed.Names {
							key := "value:" + name.Name
							if !allowedProductionDeclarations[key] {
								t.Errorf("projector declares unreviewed value %s", key)
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

func TestFunctionDeclarationKeyQualifiesReceiverType(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "methods.go", `package example
func Project() {}
func (*searchResponse) UnmarshalJSON([]byte) error { return nil }
func (Projection) UnmarshalJSON([]byte) error { return nil }
`, 0)
	if err != nil {
		t.Fatalf("parse method declarations: %v", err)
	}

	var keys []string
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			keys = append(keys, functionDeclarationKey(function))
		}
	}
	want := []string{
		"func:Project",
		"method:searchResponse.UnmarshalJSON",
		"method:Projection.UnmarshalJSON",
	}
	if len(keys) != len(want) {
		t.Fatalf("functionDeclarationKey() produced %v; want %v", keys, want)
	}
	for index := range want {
		if keys[index] != want[index] {
			t.Fatalf("functionDeclarationKey() key %d = %q; want %q", index, keys[index], want[index])
		}
	}
}
