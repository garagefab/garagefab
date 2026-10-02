// Package main is the entry point and CLI command definitions for the garagefab binary.
//
// ==============================================================================
// ARCHITECTURAL ROLE & ENTERPRISE / JAVA SPRING COMPARISON:
// Command-Line Interface (Inbound / Driving Adapter, CLI-2).
//
// In Clean / Hexagonal Architecture:
// `open.go` is an Inbound / Driving Adapter that allows a developer to quickly access
// or recover access to the running Garagefab web dashboard by printing and launching
// an authenticated login URL containing the API token hash fragment.
//
// Enterprise / Spring Boot Comparison:
// In Spring Boot applications, administrative credentials or actuator links are
// typically printed to STDOUT during startup or configured via application.properties.
// Here, `open` acts as an on-demand credential recovery CLI command that reads the
// local configuration and constructs the loopback URL.
//
// ==============================================================================
package main

import (
	"fmt"
	"net"

	"github.com/spf13/cobra"

	"github.com/garagefab/garagefab/internal/config"
)

var openNoOpenFlag bool

// openCmd implements the "garagefab open" command (spec CLI-2).
// It constructs and displays the dashboard login URL with the API token fragment:
//
//	http://127.0.0.1:<port>/login#token=<api_token>
//
// and launches the system browser unless --no-open is specified.
var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Print and open dashboard login URL (CLI-2)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. Load configuration to retrieve server port and API token
		cfg, err := config.Load(dataDirFlag)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		listen := cfg.Server.Listen
		if portFlag > 0 {
			listen = fmt.Sprintf("127.0.0.1:%d", portFlag)
		}

		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			port = "7878"
		}

		dashboardURL := fmt.Sprintf("http://127.0.0.1:%s/login#token=%s", port, cfg.Server.APIToken)
		fmt.Fprintf(cmd.OutOrStdout(), "Dashboard login URL: %s\n", dashboardURL)

		// 2. Launch system default browser unless --no-open is passed
		if !openNoOpenFlag {
			openBrowser(dashboardURL)
		}
		return nil
	},
}

func init() {
	openCmd.Flags().BoolVar(&openNoOpenFlag, "no-open", false, "do not open the browser")
	openCmd.Flags().IntVar(&portFlag, "port", 0, "port override for the running service")
	rootCmd.AddCommand(openCmd)
}
