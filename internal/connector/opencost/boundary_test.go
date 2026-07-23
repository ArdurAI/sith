// SPDX-License-Identifier: Apache-2.0

package opencost

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
	"math/big":                               true,
	"sort":                                   true,
	"strings":                                true,
	"time":                                   true,
	"unicode":                                true,
	"unicode/utf8":                           true,
	"github.com/ArdurAI/sith/internal/fleet": true,
	"k8s.io/apimachinery/pkg/util/validation": true,
}

var allowedProductionFiles = map[string]string{
	"project.go": "bc5a1b2919a50cd0213deb2881c25a3fd43beed790c9d3cbfeb9e147cb72dc96",
	"rollup.go":  "1dccb66e42dde4be827d2b2ef3a8cfea624dbce374934fd9e8340849cc3ba093",
}

var allowedProductionDeclarations = map[string]bool{
	"func:ProjectNamespaceCosts":                true,
	"func:ProjectNamespaceCostSnapshot":         true,
	"func:RollupWorkspaceCosts":                 true,
	"func:buildFact":                            true,
	"func:consumeUniqueJSON":                    true,
	"func:decodeOptionalField":                  true,
	"func:matchingDelimiter":                    true,
	"func:namespaceCostNativeID":                true,
	"func:newCostAccumulator":                   true,
	"func:objectFields":                         true,
	"func:observationCostValue":                 true,
	"func:parseCanonicalCost":                   true,
	"func:parseCanonicalTime":                   true,
	"func:parseCostAmount":                      true,
	"func:rejectCaseAliases":                    true,
	"func:rejectDuplicateJSON":                  true,
	"func:sortedScopeKeys":                      true,
	"func:validCostLiteral":                     true,
	"func:validateAllocation":                   true,
	"func:validateAllocationWindow":             true,
	"func:validateCanonicalTime":                true,
	"func:validateNamespaceCostFact":            true,
	"func:validateNamespaceCostSnapshot":        true,
	"func:validateProjection":                   true,
	"func:validateResponse":                     true,
	"func:validateText":                         true,
	"func:validateWorkspaceRollupRequest":       true,
	"method:allocationProperties.UnmarshalJSON": true,
	"method:allocationRecord.UnmarshalJSON":     true,
	"method:allocationResponse.UnmarshalJSON":   true,
	"method:allocationWindow.UnmarshalJSON":     true,
	"method:costAccumulator.add":                true,
	"method:costAccumulator.amounts":            true,
	"type:AllocationQuery":                      true,
	"type:CostAmounts":                          true,
	"type:NamespaceCostSnapshot":                true,
	"type:Projection":                           true,
	"type:WorkspaceCostCoverage":                true,
	"type:WorkspaceCostRollup":                  true,
	"type:WorkspaceRollupRequest":               true,
	"type:allocationProperties":                 true,
	"type:allocationRecord":                     true,
	"type:allocationResponse":                   true,
	"type:allocationWindow":                     true,
	"type:costField":                            true,
	"type:costAccumulator":                      true,
	"type:namespaceCostObservation":             true,
	"value:Kind":                                true,
	"value:ProtocolVersion":                     true,
	"value:aggregateNamespace":                  true,
	"value:costFields":                          true,
	"value:costScale":                           true,
	"value:currencyUSD":                         true,
	"value:maxAllocations":                      true,
	"value:maxClockSkew":                        true,
	"value:maxCostLiteralBytes":                 true,
	"value:maxCostUnits":                        true,
	"value:maxFactPayloadBytes":                 true,
	"value:maxIdentityText":                     true,
	"value:maxJSONDepth":                        true,
	"value:maxQueryWindow":                      true,
	"value:maxResponseBytes":                    true,
	"value:maxRollupCostUnits":                  true,
	"value:maxRollupFacts":                      true,
	"value:maxRollupInputBytes":                 true,
	"value:maxRollupPayloadBytes":               true,
	"value:maxRollupScopes":                     true,
}

func TestProjectorHasNoIOCredentialPersistenceOrMutationSeam(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read opencost package: %v", err)
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
			if value.Name.Name == "ProjectNamespaceCosts" {
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
				case "AllocationQuery":
					query = typeSpecification
				}
			}
		}
	}
	if projector == nil || !projectorHasExpectedSignature(projector) {
		t.Fatal("ProjectNamespaceCosts must keep the exact value-only Projection to []fleet.GraphFact,error signature")
	}
	if projection == nil || !structHasExpectedFields(projection, []fieldShape{
		{name: "Workspace", identifier: "string"},
		{name: "Scope", identifier: "string"},
		{name: "CurrencyCode", identifier: "string"},
		{name: "Query", identifier: "AllocationQuery"},
		{name: "CollectedAt", packageName: "time", selected: "Time"},
		{name: "Response", identifier: "byte", slice: true},
	}) {
		t.Fatal("Projection boundary changed or gained a capability-bearing field")
	}
	if query == nil || !structHasExpectedFields(query, []fieldShape{
		{name: "WindowStart", packageName: "time", selected: "Time"},
		{name: "WindowEnd", packageName: "time", selected: "Time"},
		{name: "Step", packageName: "time", selected: "Duration"},
		{name: "Aggregate", identifier: "string"},
		{name: "Filter", identifier: "string"},
		{name: "Accumulate", identifier: "bool"},
		{name: "IncludeIdle", identifier: "bool"},
		{name: "ShareIdle", identifier: "bool"},
		{name: "IdleByNode", identifier: "bool"},
		{name: "ShareLoadBalancer", identifier: "bool"},
		{name: "IncludeAggregatedMetadata", identifier: "bool"},
		{name: "IncludeProportionalAssetCosts", identifier: "bool"},
	}) {
		t.Fatal("AllocationQuery boundary changed or gained an unreviewed field")
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
