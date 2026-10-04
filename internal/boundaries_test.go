// Package internal_test contains architectural fitness tests.
//
// ==============================================================================
// ARCHITECTURAL FITNESS FUNCTIONS & JAVA / ARCHUNIT COMPARISON:
//
//  1. What is an Architectural Fitness Function?
//     In software architecture, fitness functions are automated tests that continuously
//     verify that code does not violate architectural constraints, layer boundaries,
//     or dependency rules (e.g. Hexagonal/Onion/Clean Architecture rules).
//
//  2. Java / ArchUnit Comparison:
//     In Java/Spring enterprise projects, ArchUnit is widely used:
//     ArchRule myRule = classes().that().resideInAPackage("..factory..")
//     .should().onlyDependOnClassesThat().resideInAnyPackage("..java..", "..factory..");
//     ArchUnit uses Java reflection and bytecode inspection (`.class` files).
//
//  3. Go's Standard AST Parser (`go/parser`, `go/token`):
//     Go does not need a third-party framework for this! The Go standard library includes
//     a full compiler front-end:
//     - `go/token`: Represents lexical tokens and source positions.
//     - `go/parser`: Parses Go source code into an Abstract Syntax Tree (AST).
//     By using `parser.ImportsOnly`, this test parses all `.go` files in the repository
//     in milliseconds and inspects their `import` statements against the boundary rules
//     specified in `PROJECT_DOCS/02_architecture.md` §6.
//
// 4. Architectural Rules Enforced Here:
//   - Rule 1: `internal/factory` (core domain) must NEVER import `store`, `worker`, `provider`, or `server`.
//   - Rule 2: `internal/store` is the ONLY package allowed to import `database/sql`.
//   - Rule 3: `internal/worker` must NEVER import `store`.
//
// ==============================================================================
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

	// token.NewFileSet manages file position and line number tracking for the parser
	fset := token.NewFileSet()

	// Recursively walk every directory and file in the project
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories (.git, .github), build outputs, and node dependencies
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "ui" || base == "bin" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only inspect Go source files (*.go)
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Parse only the package declaration and import statements (ImportsOnly mode) for high speed
		node, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		// Convert OS file path to normalized forward-slash path (e.g. internal/factory/pipeline.go)
		relPath, _ := filepath.Rel(repoRoot, path)
		slashPath := filepath.ToSlash(relPath)

		// Check every import in the file against our architectural rules
		for _, imp := range node.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			// Rule 2: store is the only package that imports database/sql. No other package imports database/sql.
			if importPath == "database/sql" {
				if !strings.HasPrefix(slashPath, "internal/store/") && slashPath != "internal/store" {
					t.Errorf("Architecture Rule 2 violation in %s: only internal/store may import database/sql", relPath)
				}
			}

			// Rule 1: factory must not import store, worker, provider, server, or intake
			if strings.HasPrefix(slashPath, "internal/factory/") {
				if strings.Contains(importPath, "/internal/store") ||
					strings.Contains(importPath, "/internal/worker") ||
					strings.Contains(importPath, "/internal/provider") ||
					strings.Contains(importPath, "/internal/server") ||
					strings.Contains(importPath, "/internal/intake") {
					t.Errorf("Architecture Rule 1 violation in %s: factory must not import %s", relPath, importPath)
				}
			}

			// Rule 3: worker must not import store
			if strings.HasPrefix(slashPath, "internal/worker/") {
				if strings.Contains(importPath, "/internal/store") {
					t.Errorf("Architecture Rule 3 violation in %s: worker must not import store", relPath)
				}
			}

			// Rule 6: provider/github must not import factory, store, server, worker, or intake
			if strings.HasPrefix(slashPath, "internal/provider/github") {
				if strings.Contains(importPath, "/internal/factory") ||
					strings.Contains(importPath, "/internal/store") ||
					strings.Contains(importPath, "/internal/server") ||
					strings.Contains(importPath, "/internal/worker") ||
					strings.Contains(importPath, "/internal/intake") {
					t.Errorf("Architecture Rule 6 violation in %s: provider/github must not import internal packages: %s", relPath, importPath)
				}
			}
		}

		return nil
	})

	if err != nil {
		t.Fatalf("failed to walk repository files: %v", err)
	}
}
