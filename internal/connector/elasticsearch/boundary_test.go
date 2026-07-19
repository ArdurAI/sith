// SPDX-License-Identifier: Apache-2.0

package elasticsearch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/format"
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

var allowedProductionFiles = map[string]string{
	"project.go": "965301c6aff18bb9dd8fd5415f8c7781b3cb3557c21d1564eb268860ad2340c1",
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

func projectLogCausesHasExpectedSignature(declaration *ast.FuncDecl) bool {
	if declaration.Recv != nil || declaration.Type.TypeParams != nil || declaration.Type.Params == nil ||
		len(declaration.Type.Params.List) != 1 || declaration.Type.Results == nil ||
		len(declaration.Type.Results.List) != 2 {
		return false
	}
	parameter := declaration.Type.Params.List[0]
	if len(parameter.Names) != 1 || parameter.Names[0].Name != "input" || !isIdentifier(parameter.Type, "Projection") {
		return false
	}
	firstResult, ok := declaration.Type.Results.List[0].Type.(*ast.ArrayType)
	return ok && firstResult.Len == nil && isSelector(firstResult.Elt, "fleet", "GraphFact") &&
		isIdentifier(declaration.Type.Results.List[1].Type, "error")
}

func isIdentifier(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func isSelector(expression ast.Expr, packageName, selectedName string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && isIdentifier(selector.X, packageName) && selector.Sel.Name == selectedName
}

func projectionHasExpectedFields(specification *ast.TypeSpec) bool {
	structure, ok := specification.Type.(*ast.StructType)
	if !ok || structure.Fields == nil {
		return false
	}
	expected := []struct {
		name        string
		identifier  string
		packageName string
		selected    string
		slice       bool
	}{
		{name: "Workspace", identifier: "string"},
		{name: "Scope", identifier: "string"},
		{name: "Namespace", identifier: "string"},
		{name: "Pod", identifier: "string"},
		{name: "Container", identifier: "string"},
		{name: "WindowStart", packageName: "time", selected: "Time"},
		{name: "WindowEnd", packageName: "time", selected: "Time"},
		{name: "ObservedAt", packageName: "time", selected: "Time"},
		{name: "Response", identifier: "byte", slice: true},
	}
	if len(structure.Fields.List) != len(expected) {
		return false
	}
	for index, field := range structure.Fields.List {
		want := expected[index]
		if len(field.Names) != 1 || field.Names[0].Name != want.name || field.Tag != nil {
			return false
		}
		if want.slice {
			slice, ok := field.Type.(*ast.ArrayType)
			if !ok || slice.Len != nil || !isIdentifier(slice.Elt, want.identifier) {
				return false
			}
			continue
		}
		if want.packageName != "" {
			if !isSelector(field.Type, want.packageName, want.selected) {
				return false
			}
			continue
		}
		if !isIdentifier(field.Type, want.identifier) {
			return false
		}
	}
	return true
}

func productionStructureFingerprint(fileSet *token.FileSet, file *ast.File) (string, error) {
	var document bytes.Buffer
	if err := format.Node(&document, fileSet, file); err != nil {
		return "", err
	}
	digest := sha256.Sum256(document.Bytes())
	return hex.EncodeToString(digest[:]), nil
}

func TestProjectorHasNoIOCredentialPersistenceOrMutationSeam(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read elasticsearch package: %v", err)
	}
	seenFiles := make(map[string]bool, len(allowedProductionFiles))
	seenDeclarations := make(map[string]bool, len(allowedProductionDeclarations))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		expectedFingerprint, allowed := allowedProductionFiles[entry.Name()]
		if !allowed {
			t.Errorf("projector contains unreviewed production file %s", entry.Name())
		}
		seenFiles[entry.Name()] = true
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		fingerprint, err := productionStructureFingerprint(fileSet, file)
		if err != nil {
			t.Fatalf("fingerprint %s: %v", entry.Name(), err)
		}
		if fingerprint != expectedFingerprint {
			t.Errorf("projector production structure changed for %s: got %s", entry.Name(), fingerprint)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import: %v", err)
			}
			if imported.Name != nil {
				t.Errorf("projector aliases production import %q as %q", path, imported.Name.Name)
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
				if seenDeclarations[key] {
					t.Errorf("projector repeats reviewed declaration %s", key)
				}
				seenDeclarations[key] = true
				if key == "func:ProjectLogCauses" && !projectLogCausesHasExpectedSignature(declaration) {
					t.Errorf("ProjectLogCauses must retain the reviewed pure-projector signature")
				}
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					switch typed := specification.(type) {
					case *ast.TypeSpec:
						key := "type:" + typed.Name.Name
						if !allowedProductionDeclarations[key] {
							t.Errorf("projector declares unreviewed type %s", key)
						}
						if seenDeclarations[key] {
							t.Errorf("projector repeats reviewed declaration %s", key)
						}
						seenDeclarations[key] = true
						if key == "type:Projection" && !projectionHasExpectedFields(typed) {
							t.Errorf("Projection must retain the reviewed value-only fields")
						}
					case *ast.ValueSpec:
						for _, name := range typed.Names {
							key := "value:" + name.Name
							if !allowedProductionDeclarations[key] {
								t.Errorf("projector declares unreviewed value %s", key)
							}
							if seenDeclarations[key] {
								t.Errorf("projector repeats reviewed declaration %s", key)
							}
							seenDeclarations[key] = true
						}
					}
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.InterfaceType:
				t.Errorf("projector declares an injected interface seam")
			case *ast.SelectorExpr:
				if isIdentifier(typed.X, "io") && typed.Sel.Name != "EOF" {
					t.Errorf("projector uses disallowed io capability io.%s", typed.Sel.Name)
				}
			}
			return true
		})
	}
	for name := range allowedProductionFiles {
		if !seenFiles[name] {
			t.Errorf("projector is missing reviewed production file %s", name)
		}
	}
	for key := range allowedProductionDeclarations {
		if !seenDeclarations[key] {
			t.Errorf("projector is missing reviewed declaration %s", key)
		}
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

func TestProjectLogCausesSignatureRejectsCapabilitySeams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		signature string
		want      bool
	}{
		{name: "reviewed", signature: `func ProjectLogCauses(input Projection) ([]fleet.GraphFact, error)`, want: true},
		{name: "reader parameter", signature: `func ProjectLogCauses(input Projection, reader io.Reader) ([]fleet.GraphFact, error)`},
		{name: "reader result", signature: `func ProjectLogCauses(input Projection) ([]fleet.GraphFact, io.Reader)`},
		{name: "interface input", signature: `func ProjectLogCauses(input any) ([]fleet.GraphFact, error)`},
		{name: "method", signature: `func (Projection) ProjectLogCauses(input Projection) ([]fleet.GraphFact, error)`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file, err := parser.ParseFile(token.NewFileSet(), "signature.go", "package example\n"+test.signature+" {}", 0)
			if err != nil {
				t.Fatalf("parse signature: %v", err)
			}
			declaration := file.Decls[0].(*ast.FuncDecl)
			if got := projectLogCausesHasExpectedSignature(declaration); got != test.want {
				t.Fatalf("projectLogCausesHasExpectedSignature() = %t; want %t", got, test.want)
			}
		})
	}
}

func TestProjectionFieldsRejectCapabilitySeams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		fields string
		want   bool
	}{
		{
			name: "reviewed",
			fields: `Workspace string
Scope string
Namespace string
Pod string
Container string
WindowStart time.Time
WindowEnd time.Time
ObservedAt time.Time
Response []byte`,
			want: true,
		},
		{name: "callback hook", fields: `Workspace string; Hook func()`},
		{name: "reader", fields: `Workspace string; Reader io.Reader`},
		{name: "extra response", fields: `Workspace string; Response []byte; Extra []byte`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := "package example\ntype Projection struct {\n" + test.fields + "\n}"
			file, err := parser.ParseFile(token.NewFileSet(), "projection.go", source, 0)
			if err != nil {
				t.Fatalf("parse Projection: %v", err)
			}
			specification := file.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec)
			if got := projectionHasExpectedFields(specification); got != test.want {
				t.Fatalf("projectionHasExpectedFields() = %t; want %t", got, test.want)
			}
		})
	}
}

func TestIOCapabilityGuardAllowsOnlyEOFSentinel(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "io.go", `package example
import "io"
var end = io.EOF
type seam struct { reader io.Reader }
`, 0)
	if err != nil {
		t.Fatalf("parse io declarations: %v", err)
	}
	var rejected []string
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && isIdentifier(selector.X, "io") && selector.Sel.Name != "EOF" {
			rejected = append(rejected, selector.Sel.Name)
		}
		return true
	})
	if len(rejected) != 1 || rejected[0] != "Reader" {
		t.Fatalf("io capability guard rejected %v; want [Reader]", rejected)
	}
}
