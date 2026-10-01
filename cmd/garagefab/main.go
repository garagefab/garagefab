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
	"github.com/garagefab/garagefab/internal/server"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/garagefab/garagefab/internal/version"
	"github.com/garagefab/garagefab/internal/worker/agent"
	"github.com/garagefab/garagefab/internal/worker/command"
	"github.com/garagefab/garagefab/internal/worker/worktree"
)

var (
	dataDirFlag string
	portFlag    int
	noOpenFlag  bool
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "garagefab",
	Short: "Garagefab: a lightweight software factory for solo developers",
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("garagefab version %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.BuildDate)
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Garagefab factory service and web dashboard",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load(dataDirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
			os.Exit(1)
		}

		if portFlag > 0 {
			cfg.Server.Listen = fmt.Sprintf("127.0.0.1:%d", portFlag)
		}

		// Startup checks (CLI-7)
		if err := checkGitAvailable(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		if err := checkPortAvailable(cfg.Server.Listen); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		// Single-instance lock (RCV-5)
		lock, err := config.AcquireLock(cfg.DataDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		defer func() {
			if err := lock.Release(); err != nil {
				slog.Error("failed to release lock", "error", err)
			}
		}()

		// Store & migrations (T4)
		dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
		db, err := store.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "database error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		// Worker, Factory, and Scheduler (M1)
		storeAdapter := newFactoryStoreAdapter(db)
		wtMgr := newFactoryWorktreeAdapter(worktree.NewManager(filepath.Join(cfg.DataDir, "worktrees")))
		fakeAgent := newFactoryAgentAdapter(agent.NewFakeRunner())
		cmdRunner := newFactoryCommandAdapter(command.NewRunner())
		engine := factory.NewEngine(storeAdapter, wtMgr, fakeAgent, cmdRunner, filepath.Join(cfg.DataDir, "logs"))
		scheduler := factory.NewScheduler(storeAdapter, engine, cfg.Engine.MaxConcurrentJobs)

		schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
		defer cancelScheduler()
		go scheduler.Start(schedulerCtx)

		// Server (T4)
		srv := server.NewServer(cfg, db, engine, scheduler, garagefab.Dist())
		go func() {
			if err := srv.Start(cfg.Server.Listen); err != nil && err != http.ErrServerClosed {
				slog.Error("server stopped with error", "error", err)
			}
		}()

		_, port, _ := net.SplitHostPort(cfg.Server.Listen)
		dashboardURL := fmt.Sprintf("http://127.0.0.1:%s/login#token=%s", port, cfg.Server.APIToken)
		fmt.Printf("Garagefab server listening on %s\n", cfg.Server.Listen)
		fmt.Printf("Dashboard login URL: %s\n", dashboardURL)

		if !noOpenFlag {
			openBrowser(dashboardURL)
		}

		// Graceful shutdown handling
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nShutting down gracefully...")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("error during server shutdown", "error", err)
		}
	},
}

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
	_ = cmd.Start()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dataDirFlag, "data-dir", "", "path to data directory (defaults to ~/.garagefab)")
	startCmd.Flags().IntVar(&portFlag, "port", 0, "port to listen on (defaults to 7878 or value in config.yaml)")
	startCmd.Flags().BoolVar(&noOpenFlag, "no-open", false, "do not open the browser on start")

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(startCmd)
}
