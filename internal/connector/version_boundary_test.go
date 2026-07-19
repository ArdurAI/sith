// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestWireVersionCoreHasNoIOProcessCredentialOrNetworkSeam(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "version.go", nil, 0)
	if err != nil {
		t.Fatalf("parse version.go: %v", err)
	}
	allowedImports := map[string]bool{"errors": true, "fmt": true, "sort": true}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatalf("unquote import: %v", err)
		}
		if imported.Name != nil || !allowedImports[path] {
			t.Errorf("version core imports unreviewed package %q", path)
		}
	}

	allowedDeclarations := map[string]bool{
		"CurrentWireVersion":      true,
		"ErrInvalidWireVersions":  true,
		"ErrWireMajorMismatch":    true,
		"ErrWireMinorUnsupported": true,
		"NegotiateWireVersion":    true,
		"String":                  true,
		"WireVersion":             true,
		"canonicalWireVersions":   true,
		"maxWireVersionsPerOffer": true,
		"wireVersionLess":         true,
	}
	for _, declaration := range file.Decls {
		for _, name := range declarationNames(declaration) {
			if !allowedDeclarations[name] {
				t.Errorf("version core declares unreviewed symbol %s", name)
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.ChanType, *ast.GoStmt, *ast.InterfaceType:
			t.Errorf("version core gained an asynchronous or injected capability seam")
		}
		return true
	})
}

func declarationNames(declaration ast.Decl) []string {
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		return []string{typed.Name.Name}
	case *ast.GenDecl:
		var names []string
		for _, specification := range typed.Specs {
			switch value := specification.(type) {
			case *ast.TypeSpec:
				names = append(names, value.Name.Name)
			case *ast.ValueSpec:
				for _, name := range value.Names {
					names = append(names, name.Name)
				}
			}
		}
		return names
	default:
		return nil
	}
}
