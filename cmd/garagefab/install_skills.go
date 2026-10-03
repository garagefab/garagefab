// Package main provides the entry point and CLI commands for Garagefab.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Command-Line Interface Adapter — Agent Skill Installer (Driving Adapter, CLI-3, HND-3).
//
// In Clean / Hexagonal Architecture:
// `install_skills.go` is an Inbound / Driving Adapter that delivers the `garagefab-work`
// interactive agent skill to external AI agent environments (Google Antigravity `agy`
// and OpenCode `opencode`).
//
// Enterprise / Spring Boot Comparison:
// Similar to Spring Boot CLI or Maven/Gradle plugins that install developer tooling or
// code generation templates into the local user environment (e.g. `~/.m2`, `~/.sdkman`),
// this command inspects the host machine for supported agents, resolves their global
// configuration directories, and installs versioned, executable skill bundles atomically.
//
// Go Idiom & Language Concept Bridges:
//   - `fs.WalkDir`: Traverses embedded filesystems (`io/fs.FS`) recursively with zero memory allocation
//     for directory structures.
//   - Atomic Filesystem Swaps (`os.Rename`): Go utilizes OS-level atomic renames to prevent partial
//     skill writes during system crashes or power interruptions.
//   - Dependency Injection in CLI commands: `InstallSkillsOptions` accepts function pointers
//     (like `LookPath`) and `io.Writer`, enabling pure, deterministic unit testing without
//     requiring real agent binaries on PATH.
//
// ==============================================================================
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/garagefab/garagefab/cmd/garagefab/skills"
)

var installSkillsDryRunFlag bool

// agentTarget defines the installation metadata for a supported coding agent.
type agentTarget struct {
	Name       string
	TargetPath string
	ConfigPath string // Secondary/discovery path if distinct from TargetPath
	IsPresent  func(home string, lookPath func(string) (string, error)) bool
}

// dirExists reports whether a directory exists at path.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// supportedAgentTargets returns the list of target configurations for supported agents (agy, opencode).
func supportedAgentTargets(homeDir string) []agentTarget {
	return []agentTarget{
		{
			Name:       "agy",
			TargetPath: filepath.Join(homeDir, ".gemini", "antigravity", "skills", "garagefab-work"),
			ConfigPath: filepath.Join(homeDir, ".gemini", "config", "skills", "garagefab-work"),
			IsPresent: func(home string, lookPath func(string) (string, error)) bool {
				if _, err := lookPath("agy"); err == nil {
					return true
				}
				return dirExists(filepath.Join(home, ".gemini"))
			},
		},
		{
			Name:       "opencode",
			TargetPath: filepath.Join(homeDir, ".config", "opencode", "skills", "garagefab-work"),
			ConfigPath: "",
			IsPresent: func(home string, lookPath func(string) (string, error)) bool {
				if _, err := lookPath("opencode"); err == nil {
					return true
				}
				return dirExists(filepath.Join(home, ".config", "opencode")) ||
					dirExists(filepath.Join(home, ".opencode"))
			},
		},
	}
}

// writeEmbeddedSkillTree extracts the embedded skill assets to targetDir atomically.
// Directories are created with 0755, shell scripts with 0755, and files with 0644.
func writeEmbeddedSkillTree(targetDir string) error {
	parent := filepath.Dir(targetDir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fmt.Errorf("create parent directory %s: %w", parent, err)
	}

	// Create a staging directory in the same parent filesystem for atomic rename.
	tmpDir, err := os.MkdirTemp(parent, ".garagefab-work-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp staging dir: %w", err)
	}
	defer func() {
		// Clean up staging directory if it was not successfully renamed.
		_ = os.RemoveAll(tmpDir)
	}()

	// Traverse the embedded filesystem tree under "garagefab-work".
	err = fs.WalkDir(skills.FS, "garagefab-work", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel := strings.TrimPrefix(path, "garagefab-work")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}

		destPath := filepath.Join(tmpDir, rel)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		content, err := fs.ReadFile(skills.FS, path)
		if err != nil {
			return fmt.Errorf("read embedded asset %s: %w", path, err)
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		// Shell scripts require executable permissions (0755); standard docs use 0644.
		perm := os.FileMode(0644)
		if strings.HasSuffix(d.Name(), ".sh") {
			perm = 0755
		}

		return os.WriteFile(destPath, content, perm)
	})
	if err != nil {
		return fmt.Errorf("extract embedded assets: %w", err)
	}

	// Atomically replace targetDir with the staging directory.
	backupDir := targetDir + fmt.Sprintf(".old.%d", time.Now().UnixNano())
	hasBackup := false
	if _, err := os.Stat(targetDir); err == nil {
		if err := os.Rename(targetDir, backupDir); err == nil {
			hasBackup = true
		} else {
			_ = os.RemoveAll(targetDir)
		}
	}

	if err := os.Rename(tmpDir, targetDir); err != nil {
		if hasBackup {
			_ = os.Rename(backupDir, targetDir)
		}
		return fmt.Errorf("atomic rename to %s: %w", targetDir, err)
	}

	if hasBackup {
		_ = os.RemoveAll(backupDir)
	}

	return nil
}

// InstallSkillsOptions specifies configuration for skill installation.
type InstallSkillsOptions struct {
	HomeDir  string
	DryRun   bool
	Out      io.Writer
	LookPath func(string) (string, error)
}

// RunInstallSkills executes the skill installation logic across supported agents (CLI-3, HND-3).
// If neither agent is installed, it prints skip notices and returns nil (not an error).
func RunInstallSkills(opts InstallSkillsOptions) error {
	if opts.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve user home directory: %w", err)
		}
		opts.HomeDir = home
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}

	targets := supportedAgentTargets(opts.HomeDir)

	for _, target := range targets {
		if !target.IsPresent(opts.HomeDir, opts.LookPath) {
			fmt.Fprintf(opts.Out, "skipped: %s not found\n", target.Name)
			continue
		}

		if opts.DryRun {
			fmt.Fprintf(opts.Out, "target: %s\n", target.TargetPath)
			continue
		}

		// Write primary skill directory
		if err := writeEmbeddedSkillTree(target.TargetPath); err != nil {
			return fmt.Errorf("install skill to %s (%s): %w", target.Name, target.TargetPath, err)
		}

		// If a secondary configuration path exists (e.g. agy's ~/.gemini/config/skills),
		// ensure it is also populated so the CLI discovers the skill reliably.
		if target.ConfigPath != "" && target.ConfigPath != target.TargetPath {
			_ = writeEmbeddedSkillTree(target.ConfigPath)
		}

		fmt.Fprintf(opts.Out, "installed: %s\n", target.TargetPath)
	}

	return nil
}

// installSkillsCmd implements the "garagefab install-skills" CLI subcommand (CLI-3, HND-3).
var installSkillsCmd = &cobra.Command{
	Use:   "install-skills",
	Short: "Install the garagefab-work skill into configured agent directories (CLI-3, HND-3)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunInstallSkills(InstallSkillsOptions{
			DryRun: installSkillsDryRunFlag,
			Out:    cmd.OutOrStdout(),
		})
	},
}

func init() {
	installSkillsCmd.Flags().BoolVar(&installSkillsDryRunFlag, "dry-run", false, "print install targets without writing files")
	rootCmd.AddCommand(installSkillsCmd)
}
