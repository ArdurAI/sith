// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionPostgresAccessIsConfinedToScopedDAL(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve database boundary test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, declaration := range parsed.Imports {
				importPath, err := strconv.Unquote(declaration.Path.Value)
				if err != nil {
					return err
				}
				if (importPath == "database/sql" || strings.HasPrefix(importPath, "github.com/jackc/pgx/")) &&
					!strings.HasPrefix(relative, "internal/hubdb/") {
					t.Errorf("%s imports PostgreSQL access outside internal/hubdb: %s", relative, importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk production source: %v", err)
		}
	}
}
