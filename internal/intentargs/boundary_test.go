// SPDX-License-Identifier: Apache-2.0

package intentargs

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPackageHasNoExecutionImports(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"bytes": true, "encoding/json": true, "errors": true, "fmt": true, "io": true,
		"math/big": true, "strconv": true, "strings": true, "unicode": true, "unicode/utf8": true,
		"github.com/google/jsonschema-go/jsonschema": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read intentargs package: %v", err)
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
			if !allowed[path] {
				t.Fatalf("intentargs imports unreviewed package %q", path)
			}
		}
	}
}
