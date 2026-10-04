// Package main is the entry point for the garagefab binary.
//
// checks.go contains pre-flight startup validations (spec CLI-7).
// Before launching the server, database, or scheduler, the application performs
// sanity checks on the host environment (verifying Git installation, version 2.30+, and port availability).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/worker/agent"
)

// gitVersionRegex extracts the major, minor, and patch numbers from "git version X.Y.Z" output.
// In Go, regexp.MustCompile compiles the regex at package initialization time.
var gitVersionRegex = regexp.MustCompile(`git version (\d+)\.(\d+)(?:\.(\d+))?`)

// validateGitVersionOutput parses the string output of `git --version` and verifies
// that the installed Git version satisfies the minimum requirement (Git 2.30+ per spec OQ-13).
//
// In Go, functions return multiple values (result, error) instead of throwing exceptions.
func validateGitVersionOutput(output string) error {
	// FindStringSubmatch returns captured regex groups: [full_match, major, minor, patch]
	matches := gitVersionRegex.FindStringSubmatch(output)
	if len(matches) < 3 {
		return fmt.Errorf("cannot parse git version from output: %q", strings.TrimSpace(output))
	}

	// strconv.Atoi converts string to integer (equivalent to Integer.parseInt() in Java).
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		// %w wraps the inner error, preserving the error chain (like new Exception("...", cause) in Java).
		return fmt.Errorf("invalid git major version: %w", err)
	}

	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return fmt.Errorf("invalid git minor version: %w", err)
	}

	// Rule: Garagefab requires Git 2.30+ for modern git-worktree lifecycle support.
	if major < 2 || (major == 2 && minor < 30) {
		return fmt.Errorf("git version 2.30+ is required, found %d.%d", major, minor)
	}

	return nil // nil represents the absence of error (successful execution).
}

// checkGitAvailable verifies that Git is installed in the system PATH and is executable.
// In Go, os/exec.Command runs a subprocess (equivalent to Java's ProcessBuilder).
func checkGitAvailable() error {
	// Execute 'git --version' and capture the stdout bytes.
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return fmt.Errorf("startup check: git is not installed or not in PATH: %w", err)
	}

	// Validate the extracted version text.
	if err := validateGitVersionOutput(string(out)); err != nil {
		return fmt.Errorf("startup check: %w", err)
	}

	return nil
}

// checkPortAvailable ensures the target listen address (e.g., "127.0.0.1:7878") is not
// already bound by another process. It prevents unhelpful panics during server boot.
func checkPortAvailable(addr string) error {
	// Attempt to bind a temporary TCP listener to the address.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("startup check: port on %s is not available (already in use): %w", addr, err)
	}

	// Immediately close the temporary listener so Garagefab can bind it during server startup.
	_ = ln.Close()
	return nil
}

// semverRegex extracts semver major.minor.patch from agent version strings.
var semverRegex = regexp.MustCompile(`(\d+\.\d+\.\d+)`)

// AgentCheckResult holds the preflight evaluation of a single AI agent tool (R3).
type AgentCheckResult struct {
	Agent           string
	TestedVersion   string
	Installed       bool
	FoundVersion    string
	VersionMismatch bool
	Warning         string
}

// CheckAgent verifies that a configured agent CLI exists on the system PATH and reports
// any version drift against known tested baselines (R3).
func CheckAgent(agentName string, lookPath func(string) (string, error), runVersion func(string) (string, error)) AgentCheckResult {
	result := AgentCheckResult{
		Agent: agentName,
	}

	switch agentName {
	case "agy":
		result.TestedVersion = agent.TestedAgyVersion
	case "opencode":
		result.TestedVersion = agent.TestedOpenCodeVersion
	default:
		// Custom or untracked agent
	}

	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if runVersion == nil {
		runVersion = func(bin string) (string, error) {
			out, err := exec.Command(bin, "--version").Output()
			if err != nil {
				return "", err
			}
			return string(out), nil
		}
	}

	binPath, err := lookPath(agentName)
	if err != nil {
		result.Installed = false
		result.Warning = fmt.Sprintf("warning: agent %q not found on PATH. Jobs requiring it will fail Blocked.", agentName)
		return result
	}

	result.Installed = true
	verOutput, err := runVersion(binPath)
	if err != nil {
		result.Warning = fmt.Sprintf("warning: agent %q version check failed: %v", agentName, err)
		return result
	}

	match := semverRegex.FindString(verOutput)
	if match != "" {
		result.FoundVersion = match
	} else {
		result.FoundVersion = strings.TrimSpace(verOutput)
	}

	if result.TestedVersion != "" && result.FoundVersion != result.TestedVersion {
		result.VersionMismatch = true
		result.Warning = fmt.Sprintf("warning: agent %q version %s differs from tested version %s (R3)", agentName, result.FoundVersion, result.TestedVersion)
	}

	return result
}

// CheckRegisteredProjectAgents gathers all distinct AI agents referenced by active projects
// and executes preflight validations for each, emitting warnings to the provided writer and logger.
func CheckRegisteredProjectAgents(ctx context.Context, db *store.DB, out io.Writer) []AgentCheckResult {
	if db == nil {
		return nil
	}
	projects, err := db.Projects().ListProjects(ctx)
	if err != nil {
		slog.Warn("preflight: could not list projects for agent check", "error", err)
		return nil
	}

	distinct := make(map[string]bool)
	for _, p := range projects {
		cfg, err := config.LoadProjectConfig(p.RepoPath)
		if err != nil {
			continue
		}
		for _, a := range []string{cfg.Agents.Spec, cfg.Agents.Probe, cfg.Agents.Coding, cfg.Agents.Review} {
			if a != "" && a != "fake" {
				distinct[a] = true
			}
		}
	}

	var results []AgentCheckResult
	for agentName := range distinct {
		res := CheckAgent(agentName, nil, nil)
		results = append(results, res)
		if res.Warning != "" {
			if out != nil {
				fmt.Fprintln(out, res.Warning)
			}
			slog.Warn(res.Warning, "agent", agentName, "installed", res.Installed, "version", res.FoundVersion)
		}
	}
	return results
}

// ghVersionRegex extracts major and minor version numbers from 'gh version X.Y.Z' output.
var ghVersionRegex = regexp.MustCompile(`gh (?:version )?(\d+)\.(\d+)(?:\.(\d+))?`)

// GitHubCheckResult captures the evaluation of GitHub CLI availability and auth status (CLI-7, GHB-1).
type GitHubCheckResult struct {
	Required      bool
	Installed     bool
	Version       string
	VersionValid  bool
	Authenticated bool
	Warning       string
}

// CheckGitHubCLI validates that GitHub CLI ('gh') is installed (v2.0.0+) and authenticated
// if any registered project has 'github.repo' configured (CLI-7, GHB-1, Decision P1).
//
// In accordance with Decision P1, a missing or unauthenticated gh CLI emits warnings
// and registers provider intake errors on the overview page, but DOES NOT terminate
// the factory process fatally.
func CheckGitHubCLI(
	ctx context.Context,
	db *store.DB,
	out io.Writer,
	lookPath func(string) (string, error),
	runVersion func() (string, error),
	checkAuth func() error,
) *GitHubCheckResult {
	res := &GitHubCheckResult{}

	// 1. Determine if any active registered project requires GitHub integration (GHB-3)
	var ghProjects []*store.Project
	if db != nil {
		projects, err := db.Projects().ListProjects(ctx)
		if err != nil {
			slog.Warn("preflight: could not list projects for GitHub check", "error", err)
		} else {
			for _, p := range projects {
				if p.IsArchived {
					continue
				}
				cfg, err := config.LoadProjectConfig(p.RepoPath)
				if err != nil {
					continue
				}
				if cfg.GitHub.Repo != "" {
					ghProjects = append(ghProjects, p)
				}
			}
		}
	}

	if len(ghProjects) == 0 {
		// No projects configure github.repo -> check not required (GHB-3)
		return res
	}
	res.Required = true

	// Inject defaults if test doubles are not provided
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if runVersion == nil {
		runVersion = func() (string, error) {
			outBytes, err := exec.Command("gh", "--version").Output()
			if err != nil {
				return "", err
			}
			return string(outBytes), nil
		}
	}
	if checkAuth == nil {
		checkAuth = func() error {
			return exec.Command("gh", "auth", "status").Run()
		}
	}

	recordWarning := func(warning string) {
		res.Warning = warning
		if out != nil {
			fmt.Fprintln(out, warning)
		}
		slog.Warn("startup check: " + warning)
		if db != nil {
			for _, p := range ghProjects {
				_ = db.Intake().UpsertIntakeError(ctx, &store.IntakeError{
					ProjectID: p.ID,
					Source:    "provider",
					Ref:       p.Name,
					Message:   warning,
				})
			}
		}
	}

	// 2. Check binary installation on system PATH
	if _, err := lookPath("gh"); err != nil {
		res.Installed = false
		recordWarning("warning: GitHub CLI ('gh') is not installed or not in PATH. Required for projects with github.repo.")
		return res
	}
	res.Installed = true

	// 3. Verify minimum version (Git CLI 2.0.0+ required)
	verOutput, err := runVersion()
	if err != nil {
		recordWarning(fmt.Sprintf("warning: failed to inspect GitHub CLI version: %v", err))
		return res
	}

	matches := ghVersionRegex.FindStringSubmatch(verOutput)
	if len(matches) < 3 {
		recordWarning(fmt.Sprintf("warning: could not parse GitHub CLI version output: %q", strings.TrimSpace(verOutput)))
		return res
	}

	major, err := strconv.Atoi(matches[1])
	if err != nil || major < 2 {
		foundVer := strings.TrimSpace(matches[0])
		recordWarning(fmt.Sprintf("warning: GitHub CLI %s is outdated; version 2.0.0+ is required.", foundVer))
		return res
	}
	res.Version = strings.TrimPrefix(matches[0], "gh ")
	res.VersionValid = true

	// 4. Verify authentication status
	if err := checkAuth(); err != nil {
		res.Authenticated = false
		recordWarning("warning: GitHub CLI ('gh') is not authenticated. Run 'gh auth login' to enable issue intake and PR delivery.")
		return res
	}
	res.Authenticated = true

	// On clean pass, clear any existing provider errors
	if db != nil {
		for _, p := range ghProjects {
			_ = db.Intake().ClearIntakeError(ctx, p.ID, "provider", p.Name)
		}
	}

	return res
}

