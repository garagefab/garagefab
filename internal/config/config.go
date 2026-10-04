// Package config manages configuration loading, validation, default provisioning,
// and single-instance process file locking for Garagefab.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Infrastructure Configuration & Security Guardrails.
//
// This package is responsible for:
// 1. Determining the application data directory (`~/.garagefab` by default).
// 2. Ensuring strict OS-level permissions (0700 for directories, 0600 for secrets).
// 3. Auto-generating a secure 256-bit API token on initial run (CLI-6).
// 4. Validating network listen addresses to guarantee loopback-only binding (SEC-1).
//
// GO CONCEPTS & JAVA / SPRING COMPARISONS:
//
//  1. Struct Tags (`yaml:"..."`):
//     In Java/Spring Boot, annotations like `@JsonProperty("server_port")` or `@Value`
//     are used for JSON/YAML binding.
//     In Go, backtick annotations like `yaml:"listen"` are "Struct Tags". The `yaml.v3`
//     library inspects them via runtime reflection (`reflect` package).
//     `yaml:"-"` is equivalent to Java's `transient` or `@JsonIgnore` — it excludes
//     the field from serialization.
//
//  2. Strongly-Typed Durations (`time.Duration`):
//     In Java, durations in config are often strings or long milliseconds.
//     Go's `time.Duration` is an `int64` representing nanoseconds. The YAML unmarshaler
//     automatically parses human-readable strings like "30s", "10m", or "2h".
//
//  3. Structured Logging (`log/slog`):
//     Standard library structured logger introduced in Go 1.21 (replaces older packages
//     like Logrus or Zap, equivalent to SLF4J with structured MDC/key-values in Java).
//
// ==============================================================================
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// StepTimeouts defines execution timeouts for agents and background commands.
type StepTimeouts struct {
	Agent   time.Duration `yaml:"agent"`   // Maximum time an AI agent CLI process may run before timeout
	Command time.Duration `yaml:"command"` // Maximum time a verification command (e.g. tests) may run
}

// EngineConfig holds factory and runner configuration defaults.
type EngineConfig struct {
	MaxConcurrentJobs int           `yaml:"max_concurrent_jobs"` // Maximum parallel jobs the scheduler admits
	MaxRepairAttempts int           `yaml:"max_repair_attempts"` // Maximum fix-loop iterations on build failure
	PollInterval      time.Duration `yaml:"poll_interval"`       // Interval between intake polling sweeps
	StepTimeouts      StepTimeouts  `yaml:"step_timeouts"`       // Timeout boundaries per pipeline step
	EnvPassthrough    []string      `yaml:"env_passthrough"`     // Environment variables allowed into agent subprocesses
}

// ServerConfig holds HTTP server and authentication configuration.
type ServerConfig struct {
	Listen   string `yaml:"listen"`    // Host and port to bind (e.g. 127.0.0.1:7878)
	APIToken string `yaml:"api_token"` // Bearer token secret required for API access
}

// Config represents the complete global configuration for Garagefab.
type Config struct {
	Server  ServerConfig `yaml:"server"`
	Engine  EngineConfig `yaml:"engine"`
	DataDir string       `yaml:"-"` // Resolved absolute path on disk (excluded from YAML serialization)
}

// Default returns a Config struct initialized with secure production defaults.
//
// Go Concept: Factory function returning default values.
// In Java, this might be a static `Config.getDefault()` or defaults in `@ConfigurationProperties`.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Listen: "127.0.0.1:7878",
		},
		Engine: EngineConfig{
			MaxConcurrentJobs: 5,
			MaxRepairAttempts: 3,
			PollInterval:      30 * time.Second,
			StepTimeouts: StepTimeouts{
				Agent:   30 * time.Minute,
				Command: 10 * time.Minute,
			},
			EnvPassthrough: []string{},
		},
	}
}

// Load loads or creates configuration from the given data directory.
// If dataDir is empty, it defaults to ~/.garagefab.
// If the directory does not exist, it creates it with mode 0700 (owner read/write/exec only).
// If config.yaml does not exist, it generates a fresh one with a random 256-bit API token
// and saves it with mode 0600 (owner read/write only).
func Load(dataDir string) (*Config, error) {
	// 1. Resolve default directory if none specified (~/.garagefab)
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("config: resolve home dir: %w", err)
		}
		dataDir = filepath.Join(home, ".garagefab")
	}

	// 2. Ensure data directory exists with strict permissions (0700: rwx------)
	dirInfo, err := os.Stat(dataDir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0700); err != nil {
			return nil, fmt.Errorf("config: create data dir %s: %w", dataDir, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("config: stat data dir %s: %w", dataDir, err)
	} else {
		// Directory already exists: inspect Unix permissions (SEC-7)
		if perm := dirInfo.Mode().Perm(); perm != 0700 {
			slog.Warn("data directory permissions too permissive", "path", dataDir, "mode", fmt.Sprintf("%#o", perm), "expected", "0700")
		}
	}

	configFile := filepath.Join(dataDir, "config.yaml")
	cfg := Default()
	cfg.DataDir = dataDir

	// 3. Inspect or create config.yaml
	fileInfo, err := os.Stat(configFile)
	if os.IsNotExist(err) {
		// Generate cryptographically secure random 32-byte (256-bit) API token (CLI-6)
		token, gErr := GenerateAPIToken()
		if gErr != nil {
			return nil, gErr
		}
		cfg.Server.APIToken = token

		// Serialize to YAML and persist to disk with mode 0600 (rw-------)
		if err := Save(dataDir, cfg); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("config: stat config file %s: %w", configFile, err)
	} else {
		// Existing config file: warn if readable by other OS users (SEC-7)
		if perm := fileInfo.Mode().Perm(); perm != 0600 {
			slog.Warn("config file permissions too permissive", "path", configFile, "mode", fmt.Sprintf("%#o", perm), "expected", "0600")
		}

		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("config: read config file %s: %w", configFile, err)
		}

		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse yaml %s: %w", configFile, err)
		}
	}

	// 4. Validate semantic constraints
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// GenerateAPIToken returns a fresh cryptographically secure 256-bit API token as hex (CLI-6, CLI-8).
func GenerateAPIToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("config: generate api token: %w", err)
	}
	return hex.EncodeToString(tokenBytes), nil
}

// Save writes cfg to <dataDir>/config.yaml with mode 0600 (CLI-8). The write is atomic: a
// temporary file in the same directory is renamed into place, so a crash cannot leave a
// partially written config.
func Save(dataDir string, cfg *Config) error {
	if dataDir == "" {
		return fmt.Errorf("config: save: data directory must not be empty")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("config: create data dir %s: %w", dataDir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal config: %w", err)
	}

	configFile := filepath.Join(dataDir, "config.yaml")
	tmp, err := os.CreateTemp(dataDir, "config.yaml.tmp-*")
	if err != nil {
		return fmt.Errorf("config: create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp config: %w", err)
	}
	if err := os.Rename(tmpName, configFile); err != nil {
		return fmt.Errorf("config: install config %s: %w", configFile, err)
	}
	return nil
}

// validate ensures mandatory settings are valid and safe before booting the server.
func validate(cfg *Config) error {
	// API token must never be blank
	if cfg.Server.APIToken == "" {
		return fmt.Errorf("config: validation: server.api_token must not be empty")
	}

	// Parse host and port from "host:port" string
	host, _, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("config: validation: server.listen %q: %w", cfg.Server.Listen, err)
	}

	// Enforce loopback binding: Garagefab must never bind to public interfaces (0.0.0.0 or LAN IP)
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("config: validation: server.listen %q: address must be loopback (127.0.0.1 or localhost)", cfg.Server.Listen)
		}
	}

	return nil
}
