// Package version provides application build and version metadata.
//
// ==============================================================================
// BUILD-TIME METADATA INJECTION & JAVA / MAVEN COMPARISON:
//
// 1. Linker Flag Injection (`-ldflags -X`):
//
//   - In Java: Version and Git commit info are typically baked into `git.properties`
//     or `META-INF/MANIFEST.MF` by Maven plugins (`git-commit-id-plugin`) or Gradle.
//     At runtime, the application parses the properties file via the ClassLoader.
//
//   - In Go: The Go linker (`link`) supports the `-X` flag to overwrite the string
//     content of package-level variables at compile time without generating any
//     external files:
//     go build -ldflags "-X github.com/garagefab/garagefab/internal/version.Version=1.0.0 \
//     -X github.com/garagefab/garagefab/internal/version.Commit=$(git rev-parse HEAD)"
//
//     2. Fallback Defaults:
//     When compiling locally with a simple `go build` or `go test` without `-ldflags`,
//     the variables retain their default fallback values declared below.
//
// ==============================================================================
package version

// Build-time variables injected via -ldflags.
var (
	// Version is the semantic release version of Garagefab (e.g., "0.1.0" or "0.1.0-dev").
	Version = "0.1.0-dev"

	// Commit is the Git SHA hash of the repository commit the binary was built from.
	Commit = "unknown"

	// BuildDate is the RFC3339 timestamp indicating when the binary was compiled.
	BuildDate = "unknown"
)
