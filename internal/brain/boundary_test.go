// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBrainHasNoWritePathImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read brain package: %v", err)
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
				t.Fatalf("unquote import: %v", err)
			}
			for _, forbidden := range []string{"/connector", "/hubdb", "/localops", "/mcpserver", "/pep"} {
				if strings.Contains(path, forbidden) {
					t.Fatalf("brain imports forbidden write-capable package %q", path)
				}
			}
			for _, forbidden := range []string{"database/sql", "net", "net/http", "os", "os/exec", "google.golang.org/grpc"} {
				if path == forbidden {
					t.Fatalf("brain imports forbidden side-effect package %q", path)
				}
			}
		}
	}
}
