// Package skills embeds the garagefab-work skill assets into the binary.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Embedded Asset Provider (Driving Adapter / Infrastructure Component, CLI-3, HND-3).
//
// In Clean / Hexagonal Architecture:
// `embed.go` serves as an immutable, compiled-in artifact container that packages
// interactive agent skill files (`SKILL.md` instructions and `gf-api.sh` helper)
// directly inside the single Go binary.
//
// Enterprise / Spring Boot Comparison:
// In Spring Boot, static resources or shell templates are packaged in `src/main/resources`
// and read at runtime via `ClassPathResource` or `ResourceLoader`. In Go, the `embed`
// package allows compile-time embedding of filesystem trees into an `embed.FS` struct
// with zero disk I/O at runtime and no external asset path dependencies.
//
// Go Idiom Bridges:
//   - `//go:embed all:garagefab-work`: The compiler directive embeds the entire directory
//     hierarchy matching the pattern. The `all:` prefix is crucial because it includes
//     hidden files, nested directories, and helper scripts without omission.
//   - `embed.FS`: Implements the standard `io/fs.FS` interface, allowing read-only
//     virtual filesystem traversal, directory walking, and byte extraction.
//
// ==============================================================================
package skills

import (
	"embed"
)

// FS contains the embedded garagefab-work skill assets tree.
//
//go:embed all:garagefab-work
var FS embed.FS
