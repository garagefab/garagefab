// Package factory implements the core SDLC pipeline orchestrator and state engine.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Core Domain Service — Prompt Template Engine (SPC-1/2, COD-1, COD-9, REV-1..4, PRB).
//
// In Garagefab, prompt generation is a pure domain concern. Each SDLC stage produces
// a standardized, deterministic prompt for the agent that includes:
// 1. Fixed section structure (# Garagefab <Role> Task -> Role & Rules -> Context -> Required Output -> Constraints).
// 2. Clear isolation boundaries (review never sees coding agent chatter; coding never sees full conversational history).
// 3. Automated repair feedback and human gate rejection notes passed verbatim when present.
//
// JAVA / ENTERPRISE BACKEND COMPARISONS:
//
//  1. Server-Side Template Engines (FreeMarker / Thymeleaf):
//     In enterprise Java, templated emails or LLM prompts are generated using FreeMarker
//     (`Configuration.getTemplate(...)`) or Velocity with strict variable resolution.
//     In Go, the standard `text/template` package provides safe, compiled templating.
//
//  2. Embedded Resources (`//go:embed` vs Spring Resource Loader):
//     In Java/Spring, template files in `src/main/resources/prompts/` are loaded via
//     `ResourceLoader.getResource("classpath:prompts/...")`.
//     In Go, the `//go:embed` compiler directive compiles template assets directly into the
//     read-only data segment of the application binary, eliminating external file path dependencies.
//
//  3. Strict Template Validation (`missingkey=error`):
//     Setting `.Option("missingkey=error")` causes template execution to fail fast if any
//     referenced property in `PromptData` is missing, preventing silent partial renders.
//
// ==============================================================================
package factory

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/*.md.tmpl
var promptTemplatesFS embed.FS

// PromptData contains all domain inputs required to render an agent stage prompt.
type PromptData struct {
	JobID          int64    // Numeric identifier of the job
	WorkType       string   // Job work type: "feature", "bug_fix", "refactor", "docs"
	Intent         string   // Raw user intent description
	ArtifactDir    string   // Relative job artifact directory (e.g. ".garagefab/jobs/<id>")
	Spec           string   // Approved spec.md content (for coding and review stages)
	Clarification  string   // Historical clarification.md Q&A content (for spec stage)
	RepairFeedback string   // Automated repair failure output from previous attempt
	RejectionNotes []string // Rejection notes from human gates (APR-6)
	BuildCmds      []string // Configured project build commands
	TestCmds       []string // Configured project test commands
	LintCmds       []string // Configured project lint commands
	ProtectedPaths []string // Protected file globs from project guardrails (GRD-1)
	ProbeFiles     []string // Protected probe test files (COD-8)
	Diff           string   // Git diff from merge base (for review stage)
	CommandSummary []string // Summary of prior command execution results (for review stage)
	ProbeResult    string   // Output from failing probe reproduction (for bug fixes)
}

var parsedTemplates = template.Must(
	template.New("prompts").
		Option("missingkey=error").
		ParseFS(promptTemplatesFS, "prompts/*.md.tmpl"),
)

// RenderPrompt compiles and renders the markdown prompt for the specified SDLC role.
func RenderPrompt(role string, data PromptData) (string, error) {
	templateName := fmt.Sprintf("%s.md.tmpl", role)
	tmpl := parsedTemplates.Lookup(templateName)
	if tmpl == nil {
		return "", fmt.Errorf("factory: render prompt: unknown role %q", role)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("factory: render prompt for %s: %w", role, err)
	}

	return strings.TrimSpace(buf.String()), nil
}
