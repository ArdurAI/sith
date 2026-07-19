// SPDX-License-Identifier: Apache-2.0

package dcgm

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
	"strconv":                                true,
	"strings":                                true,
	"time":                                   true,
	"unicode":                                true,
	"unicode/utf8":                           true,
	"github.com/ArdurAI/sith/internal/fleet": true,
	"k8s.io/apimachinery/pkg/util/validation": true,
}

var allowedProductionFiles = map[string]string{
	"project.go": "282d08a75f4d8fa5109e055806e12804f307977cb67e0097600c345d5b01729e",
}

var allowedProductionDeclarations = map[string]bool{
	"func:ProjectGPUUtilization":     true,
	"func:canonicalPercent":          true,
	"func:consumeUniqueJSON":         true,
	"func:matchingDelimiter":         true,
	"func:parseUnixTimestamp":        true,
	"func:projectSeries":             true,
	"func:rejectDuplicateJSON":       true,
	"func:requiredLabel":             true,
	"func:validGPUUUID":              true,
	"func:validIndex":                true,
	"func:validLabelName":            true,
	"func:validSafeToken":            true,
	"func:validateCanonicalTime":     true,
	"func:validateLabelValue":        true,
	"func:validateProjection":        true,
	"func:validateResponse":          true,
	"func:validateText":              true,
	"func:workloadIdentity":          true,
	"type:InstantQuery":              true,
	"type:Projection":                true,
	"type:gpuUtilizationObservation": true,
	"type:projectedSeries":           true,
	"type:queryData":                 true,
	"type:queryEnvelope":             true,
	"type:seriesIdentity":            true,
	"type:sourceSeries":              true,
	"value:GPUUtilizationMetric":     true,
	"value:Kind":                     true,
	"value:ProtocolVersion":          true,
	"value:attributionMIGInstance":   true,
	"value:attributionPhysicalGPU":   true,
	"value:attributionWorkload":      true,
	"value:maxClockSkew":             true,
	"value:maxFactPayloadBytes":      true,
	"value:maxIdentityText":          true,
	"value:maxJSONDepth":             true,
	"value:maxLabelNameBytes":        true,
	"value:maxLabelValueBytes":       true,
	"value:maxLabelsPerSeries":       true,
	"value:maxMIGProfileBytes":       true,
	"value:maxModelNameBytes":        true,
	"value:maxPercentLiteralBytes":   true,
	"value:maxResponseBytes":         true,
	"value:maxSeries":                true,
	"value:maxTimestampLiteralSize":  true,
}

func TestProjectorHasNoIOCredentialPersistenceOrMutationSeam(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dcgm package: %v", err)
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
				t.Errorf("projector imports unreviewed package %q", path)
			}
		}
		for _, declaration := range file.Decls {
			for _, key := range productionDeclarationKeys(declaration) {
				if !allowedProductionDeclarations[key] {
					t.Errorf("projector declares unreviewed symbol %s", key)
				}
				if seenDeclarations[key] {
					t.Errorf("projector declaration %s is duplicated", key)
				}
				seenDeclarations[key] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && isIdentifier(selector.X, "io") && selector.Sel.Name != "EOF" {
				t.Errorf("projector uses unreviewed io capability io.%s", selector.Sel.Name)
			}
			function, ok := node.(*ast.FuncDecl)
			if !ok {
				return true
			}
			switch function.Name.Name {
			case "Plan", "Execute", "Verify", "Write", "Delete", "Update", "Create", "Reload", "Dial", "Fetch":
				t.Errorf("projector declares forbidden capability seam %s", function.Name.Name)
			}
			return true
		})
	}
	for name := range allowedProductionFiles {
		if !seenFiles[name] {
			t.Errorf("reviewed production file %s is missing", name)
		}
	}
	for key := range allowedProductionDeclarations {
		if !seenDeclarations[key] {
			t.Errorf("reviewed production declaration %s is missing", key)
		}
	}
}

func TestPublicProjectorBoundaryIsValueOnlyAndExact(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "project.go", nil, 0)
	if err != nil {
		t.Fatalf("parse project.go: %v", err)
	}
	var projector *ast.FuncDecl
	var projection, query *ast.TypeSpec
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			if value.Name.Name == "ProjectGPUUtilization" {
				projector = value
			}
		case *ast.GenDecl:
			for _, specification := range value.Specs {
				typeSpecification, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				switch typeSpecification.Name.Name {
				case "Projection":
					projection = typeSpecification
				case "InstantQuery":
					query = typeSpecification
				}
			}
		}
	}
	if projector == nil || !projectorHasExpectedSignature(projector) {
		t.Fatal("ProjectGPUUtilization must keep the exact value-only Projection to []fleet.GraphFact,error signature")
	}
	if projection == nil || !structHasExpectedFields(projection, []fieldShape{
		{name: "Workspace", identifier: "string"},
		{name: "Scope", identifier: "string"},
		{name: "Query", identifier: "InstantQuery"},
		{name: "CollectedAt", packageName: "time", selected: "Time"},
		{name: "Response", identifier: "byte", slice: true},
	}) {
		t.Fatal("Projection boundary changed or gained a capability-bearing field")
	}
	if query == nil || !structHasExpectedFields(query, []fieldShape{
		{name: "Expression", identifier: "string"},
		{name: "EvaluatedAt", packageName: "time", selected: "Time"},
		{name: "Limit", identifier: "int"},
		{name: "LookbackDelta", packageName: "time", selected: "Duration"},
	}) {
		t.Fatal("InstantQuery boundary changed or gained an unreviewed field")
	}
}

type fieldShape struct {
	name        string
	identifier  string
	packageName string
	selected    string
	slice       bool
}

func structHasExpectedFields(specification *ast.TypeSpec, expected []fieldShape) bool {
	structure, ok := specification.Type.(*ast.StructType)
	if !ok || structure.Fields == nil || len(structure.Fields.List) != len(expected) {
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

func projectorHasExpectedSignature(declaration *ast.FuncDecl) bool {
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

func productionDeclarationKeys(declaration ast.Decl) []string {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		return []string{functionDeclarationKey(value)}
	case *ast.GenDecl:
		if value.Tok == token.IMPORT {
			return nil
		}
		keys := make([]string, 0, len(value.Specs))
		for _, specification := range value.Specs {
			switch typed := specification.(type) {
			case *ast.TypeSpec:
				keys = append(keys, "type:"+typed.Name.Name)
			case *ast.ValueSpec:
				for _, name := range typed.Names {
					keys = append(keys, "value:"+name.Name)
				}
			}
		}
		return keys
	default:
		return []string{"declaration:<invalid>"}
	}
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

func productionStructureFingerprint(fileSet *token.FileSet, file *ast.File) (string, error) {
	var document bytes.Buffer
	if err := format.Node(&document, fileSet, file); err != nil {
		return "", err
	}
	digest := sha256.Sum256(document.Bytes())
	return hex.EncodeToString(digest[:]), nil
}

func isIdentifier(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func isSelector(expression ast.Expr, packageName, selectedName string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && isIdentifier(selector.X, packageName) && selector.Sel.Name == selectedName
}
