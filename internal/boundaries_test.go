package internal_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArchitectureImportBoundaries verifies the dependency boundaries defined in architecture.md §6.
func TestArchitectureImportBoundaries(t *testing.T) {
	// Root of the repository is one level above internal/
	repoRoot := ".."

	fset := token.NewFileSet()

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories, vendor, node_modules, and UI
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "ui" || base == "bin" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		node, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(repoRoot, path)
		slashPath := filepath.ToSlash(relPath)

		for _, imp := range node.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			// Rule 2: store is the only package that imports database/sql. No other package imports database/sql.
			if importPath == "database/sql" {
				if !strings.HasPrefix(slashPath, "internal/store/") && slashPath != "internal/store" {
					t.Errorf("Architecture Rule 2 violation in %s: only internal/store may import database/sql", relPath)
				}
			}

			// Rule 1: factory must not import store, worker, provider, or server
			if strings.HasPrefix(slashPath, "internal/factory/") {
				if strings.Contains(importPath, "/internal/store") ||
					strings.Contains(importPath, "/internal/worker") ||
					strings.Contains(importPath, "/internal/provider") ||
					strings.Contains(importPath, "/internal/server") {
					t.Errorf("Architecture Rule 1 violation in %s: factory must not import %s", relPath, importPath)
				}
			}

			// Rule 3: worker must not import store
			if strings.HasPrefix(slashPath, "internal/worker/") {
				if strings.Contains(importPath, "/internal/store") {
					t.Errorf("Architecture Rule 3 violation in %s: worker must not import store", relPath)
				}
			}
		}

		return nil
	})

	if err != nil {
		t.Fatalf("failed to walk repository files: %v", err)
	}
}
