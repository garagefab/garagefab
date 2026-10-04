// Package main (cmd/garagefab) is the application entry point and composition root.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Composition Root — API credential rotation command (CLI-8).
//
//  1. Architectural Role:
//     Defines the `garagefab token rotate` command. It regenerates the bearer token, persists it
//     with mode 0600, and invalidates every dashboard session so old credentials stop working.
//
//  2. Enterprise / Java Comparison:
//     Analogous to a Spring Boot actuator credential-rotation endpoint, or a Vault
//     `token renew`-style administrative action: rotate the secret, revoke existing leases.
//
//  3. Go Idioms:
//     - Cobra command trees are built from nested `*cobra.Command` values registered in `init()`.
//     - The single-instance `flock` (internal/config) is used to detect a running daemon.
//
// ==============================================================================
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
	"github.com/spf13/cobra"
)

// RunTokenRotateOptions holds the inputs for the token rotation command.
type RunTokenRotateOptions struct {
	DataDir string
	Force   bool
	Out     io.Writer
}

// RunTokenRotate rotates the API token and invalidates all sessions (CLI-8).
//
// The running server holds the token in memory, so a rotation only fully takes effect after a
// restart. To avoid surprising an operator, rotation refuses while a daemon holds the
// single-instance lock unless `--force` is given.
func RunTokenRotate(opts RunTokenRotateOptions) error {
	cfg, err := config.Load(opts.DataDir)
	if err != nil {
		return err
	}

	lock, lockErr := config.AcquireLock(cfg.DataDir)
	if lockErr != nil {
		if !opts.Force {
			return fmt.Errorf("a garagefab instance is already running; stop it first, or use --force (the running daemon keeps the old token until it restarts)")
		}
		_, _ = fmt.Fprintln(opts.Out, "warning: --force used; the running daemon keeps the old bearer token until it restarts")
	} else {
		defer func() { _ = lock.Release() }()
	}

	token, err := config.GenerateAPIToken()
	if err != nil {
		return err
	}
	cfg.Server.APIToken = token

	// Invalidate sessions BEFORE persisting the new token. If this fails, the old token stays
	// in effect and the operator can retry, instead of leaving a half-rotated state.
	db, err := store.Open(filepath.Join(cfg.DataDir, "garagefab.db"))
	if err != nil {
		return fmt.Errorf("token rotate: open store: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Sessions().DeleteAllSessions(context.Background()); err != nil {
		return fmt.Errorf("token rotate: invalidate sessions: %w", err)
	}

	if err := config.Save(cfg.DataDir, cfg); err != nil {
		return err
	}

	_, port, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		port = "7878"
	}
	_, _ = fmt.Fprintf(opts.Out, "New API token written to %s (mode 0600)\n", filepath.Join(cfg.DataDir, "config.yaml"))
	_, _ = fmt.Fprintln(opts.Out, "All sessions invalidated. Restart garagefab for the new token to take effect.")
	_, _ = fmt.Fprintf(opts.Out, "Dashboard login URL: http://127.0.0.1:%s/login#token=%s\n", port, cfg.Server.APIToken)
	return nil
}

var tokenForceFlag bool

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage the Garagefab API token (CLI-8)",
}

var tokenRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Rotate the API token and invalidate all sessions (CLI-8)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunTokenRotate(RunTokenRotateOptions{
			DataDir: dataDirFlag,
			Force:   tokenForceFlag,
			Out:     cmd.OutOrStdout(),
		})
	},
}

func init() {
	tokenRotateCmd.Flags().BoolVar(&tokenForceFlag, "force", false, "rotate even if a daemon is running")
	tokenCmd.AddCommand(tokenRotateCmd)
	rootCmd.AddCommand(tokenCmd)
}
