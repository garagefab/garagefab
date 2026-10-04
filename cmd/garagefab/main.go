// Package main is the entry point and dependency-injection wiring root for the Garagefab binary.
//
// In Clean / Hexagonal Architecture, this package serves as the "Composition Root"
// (equivalent to Spring Boot's Application.java and configuration classes in Java).
// It is the ONLY place in the codebase that knows about concrete implementations across all
// packages (config, store, worker, factory, server) and connects them together.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/garagefab/garagefab"
	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/factory"
	"github.com/garagefab/garagefab/internal/intake"
	"github.com/garagefab/garagefab/internal/provider/github"
	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/version"
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

// Command-line flag variables.
// In Go, flags are bound to package-level variables during package initialization (init()).
var (
	dataDirFlag   string // Custom data directory path (defaults to ~/.garagefab)
	portFlag      int    // Custom port override (defaults to 7878 or config.yaml)
	noOpenFlag    bool   // If true, prevents automatic browser launch on startup
	fakeAgentFlag bool   // If true, routes all agent roles to in-process fake runner
)

// main is the application entry point (equivalent to public static void main in Java).
// Its sole responsibility is executing the root Cobra command tree and exiting with
// code 1 if an error is returned.
func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// rootCmd represents the base CLI command ("garagefab").
// Cobra uses a command-tree structure: subcommands (start, version, status) are attached to rootCmd.
var rootCmd = &cobra.Command{
	Use:   "garagefab",
	Short: "Garagefab: a lightweight software factory for solo developers",
}

// versionCmd implements the "garagefab version" CLI command (spec CLI-5).
// It prints the version, git commit SHA, and UTC build timestamp injected at compile time via -ldflags.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("garagefab version %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.BuildDate)
	},
}

// startCmd implements the "garagefab start" CLI command (spec CLI-1).
// It executes the full factory bootstrap:
//  1. Load or auto-generate configuration and API token (CLI-6)
//  2. Run environment pre-flight checks: Git version & port availability (CLI-7)
//  3. Acquire single-instance process lock (RCV-5)
//  4. Open SQLite database and apply Goose schema migrations
//  5. Wire worker, factory, and scheduler components (Hexagonal adapters)
//  6. Start HTTP server and serve embedded React UI (server)
//  7. Open browser dashboard unless --no-open is specified
//  8. Block until SIGINT/SIGTERM, then perform graceful shutdown within 5 seconds
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Garagefab factory service and web dashboard",
	Run: func(cmd *cobra.Command, args []string) {
		// 1. Load configuration. On first run, config.Load creates ~/.garagefab,
		// generates a cryptographically secure 64-char API token, and writes config.yaml (0600 mode).
		cfg, err := config.Load(dataDirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
			os.Exit(1)
		}

		// Allow CLI --port flag to override the port specified in config.yaml.
		if portFlag > 0 {
			cfg.Server.Listen = fmt.Sprintf("127.0.0.1:%d", portFlag)
		}

		// 2. Pre-flight startup checks (spec CLI-7). Fail-fast before acquiring locks or touching DB.
		if err := checkGitAvailable(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		if err := checkPortAvailable(cfg.Server.Listen); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		// 3. Single-instance process lock (spec RCV-5).
		// Uses an OS file lock (flock) to prevent two garagefab instances from accessing the same data dir.
		lock, err := config.AcquireLock(cfg.DataDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		// 'defer' in Go functions like Java's try-finally: this cleanup runs whenever startCmd exits.
		defer func() {
			if err := lock.Release(); err != nil {
				slog.Error("failed to release lock", "error", err)
			}
		}()

		// 4. Persistence Layer (SQLite + Goose migrations).
		// Opens SQLite with WAL mode, single writer, and pooled readers.
		dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
		db, err := store.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "database error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		// 4b. Crash Recovery & Orphan Process Reclamation (RCV-2, RCV-3).
		// Inspect active process records from previous runs, terminate orphan process groups,
		// and mark interrupted jobs before starting scheduler or HTTP listener.
		if err := RecoverOrphanProcesses(context.Background(), db); err != nil {
			slog.Warn("startup crash recovery warning", "error", err)
		}

		// 4c. Pre-flight Agent Checks (R3).
		// Validate that all distinct agent binaries referenced by registered projects
		// are on PATH and match tested version baselines.
		CheckRegisteredProjectAgents(context.Background(), db, os.Stdout)

		// 4d. Pre-flight GitHub CLI Checks (CLI-7, GHB-1).
		// Validate that gh is installed (>= 2.0.0) and authenticated if any project uses GitHub.
		CheckGitHubCLI(context.Background(), db, os.Stdout, nil, nil, nil)

		// 5. Dependency Injection / Wiring (Hexagonal Architecture).
		// 'factory' defines interfaces (ports) and cannot import 'store' or 'worker' directly.
		// These adapters bridge the concrete implementations to the factory's interfaces.
		storeAdapter := newFactoryStoreAdapter(db)
		wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))

		// Build Agent Router and register supported CLI adapters (HND-2, COD-10).
		agentRouter := agent.NewRouter()
		if fakeAgentFlag || os.Getenv("GARAGEFAB_FAKE_AGENT") == "1" {
			fakeRunner := agent.NewFakeRunner()
			agentRouter.Register("fake", fakeRunner)
			agentRouter.Register("agy", fakeRunner)
			agentRouter.Register("opencode", fakeRunner)
		} else {
			agentRouter.Register("agy", agent.NewAgyRunner("", cfg.Engine.EnvPassthrough))
			agentRouter.Register("opencode", agent.NewOpenCodeRunner("", cfg.Engine.EnvPassthrough))
		}
		agentAdapter := newFactoryAgentAdapter(agentRouter)

		cmdRunner := newFactoryCommandAdapter(command.NewRunner())
		guardrailAdapter := newFactoryGuardrailAdapter()
		projCfgAdapter := newFactoryProjectConfigAdapter()

		engine := factory.NewEngine(storeAdapter, wtMgr, agentAdapter, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
		engine.SetGuardrailRunner(guardrailAdapter)
		engine.SetProjectConfigProvider(projCfgAdapter)
		engine.SetMaxRepairAttempts(cfg.Engine.MaxRepairAttempts)
		engine.SetDefaultAgentTimeout(cfg.Engine.StepTimeouts.Agent)

		scheduler := factory.NewScheduler(storeAdapter, engine, cfg.Engine.MaxConcurrentJobs)
		scheduler.SetProjectConfigProvider(projCfgAdapter)

		// 5b. GitHub Provider & Adapters (M6: GHB-1, DLV-1, INT-3).
		ghRunner := github.NewDefaultGHRunner("")
		ghClient := github.NewClient(ghRunner)
		prAdapter := newFactoryPullRequestAdapter(ghClient)
		engine.SetPullRequestProvider(prAdapter)

		// 5c. Intake Poller & Issue Feedback Reconciler (M6: INT-2..7, GHB-2, GHB-5).
		issueSourceAdapter := newIntakeIssueSourceAdapter(ghClient)
		issueFeedbackAdapter := newIntakeIssueFeedbackAdapter(ghClient)
		feedbackReconciler := intake.NewFeedbackReconciler(issueFeedbackAdapter)

		poller := intake.NewPoller(db, issueSourceAdapter, scheduler, 30*time.Second)
		poller.SetFeedbackReconciler(feedbackReconciler)

		pollerCtx, cancelPoller := context.WithCancel(context.Background())
		defer cancelPoller()
		go poller.Start(pollerCtx)

		// Start the job scheduler in a background goroutine (lightweight async thread in Go).
		schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
		defer cancelScheduler()
		go scheduler.Start(schedulerCtx)

		// 6. HTTP Server & Embedded UI.
		// garagefab.Dist() provides the in-memory React SPA files embedded at compile time via //go:embed.
		srv := server.NewServer(cfg, db, engine, scheduler, garagefab.Dist())
		go func() {
			if err := srv.Start(cfg.Server.Listen); err != nil && err != http.ErrServerClosed {
				slog.Error("server stopped with error", "error", err)
			}
		}()

		// Display connection details to the user.
		_, port, _ := net.SplitHostPort(cfg.Server.Listen)
		dashboardURL := fmt.Sprintf("http://127.0.0.1:%s/login#token=%s", port, cfg.Server.APIToken)
		fmt.Printf("Garagefab server listening on %s\n", cfg.Server.Listen)
		fmt.Printf("Dashboard login URL: %s\n", dashboardURL)

		// 7. Automatically open the default web browser unless the user passed --no-open.
		if !noOpenFlag {
			openBrowser(dashboardURL)
		}

		// 8. Graceful shutdown handler (RCV-4).
		// In Go, channels (chan) are used to receive OS signals.
		// signal.Notify redirects SIGINT (Ctrl+C) and SIGTERM (kill) into sigCh.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh // Blocks execution here until a termination signal is received!

		fmt.Println("\nShutting down gracefully...")

		// Stop admitting new jobs and stop intake poller immediately
		cancelPoller()
		cancelScheduler()

		// Allow active HTTP requests up to 5 seconds to complete cleanly before terminating.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("error during server shutdown", "error", err)
		}

		// Graceful Worker Drain: await completion of all active
		// job goroutines BEFORE db.Close() runs via defer.
		scheduler.Close()
	},
}

// openBrowser launches the system default web browser with the target URL.
// It executes OS-native launcher commands ('open' on macOS, 'xdg-open' on Linux).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start() // Fire-and-forget: do not block the server waiting for the browser process.
}

// init is a special Go runtime function that executes automatically before main().
// It is equivalent to a static { ... } initializer block in Java.
// Here, it binds command-line flags and registers child commands (version, start) onto rootCmd.
func init() {
	rootCmd.PersistentFlags().StringVar(&dataDirFlag, "data-dir", "", "path to data directory (defaults to ~/.garagefab)")
	startCmd.Flags().IntVar(&portFlag, "port", 0, "port to listen on (defaults to 7878 or value in config.yaml)")
	startCmd.Flags().BoolVar(&noOpenFlag, "no-open", false, "do not open the browser on start")
	startCmd.Flags().BoolVar(&fakeAgentFlag, "fake-agent", false, "force all agent roles to use fake agent")
	_ = startCmd.Flags().MarkHidden("fake-agent")

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(startCmd)
}
