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

// StepTimeouts defines execution timeouts for agents and commands.
type StepTimeouts struct {
	Agent   time.Duration `yaml:"agent"`
	Command time.Duration `yaml:"command"`
}

// EngineConfig holds factory and runner configuration defaults.
type EngineConfig struct {
	MaxConcurrentJobs int           `yaml:"max_concurrent_jobs"`
	MaxRepairAttempts int           `yaml:"max_repair_attempts"`
	PollInterval      time.Duration `yaml:"poll_interval"`
	StepTimeouts      StepTimeouts  `yaml:"step_timeouts"`
	EnvPassthrough    []string      `yaml:"env_passthrough"`
}

// GitHubConfig holds configuration for GitHub integration.
type GitHubConfig struct {
	TokenEnv string `yaml:"token_env"`
}

// ServerConfig holds HTTP server and authentication configuration.
type ServerConfig struct {
	Listen   string `yaml:"listen"`
	APIToken string `yaml:"api_token"`
}

// Config represents the complete global configuration for Garagefab.
type Config struct {
	Server  ServerConfig `yaml:"server"`
	Engine  EngineConfig `yaml:"engine"`
	GitHub  GitHubConfig `yaml:"github"`
	DataDir string       `yaml:"-"`
}

// Default returns a Config initialized with default settings.
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
		GitHub: GitHubConfig{
			TokenEnv: "GARAGEFAB_GITHUB_TOKEN",
		},
	}
}

// Load loads or creates configuration from the given data directory.
// If dataDir is empty, it defaults to ~/.garagefab.
// If the directory does not exist, it creates it with mode 0700.
// If config.yaml does not exist, it generates a fresh one with a random API token and mode 0600.
func Load(dataDir string) (*Config, error) {
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("config: resolve home dir: %w", err)
		}
		dataDir = filepath.Join(home, ".garagefab")
	}

	// Ensure directory exists with 0700
	dirInfo, err := os.Stat(dataDir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0700); err != nil {
			return nil, fmt.Errorf("config: create data dir %s: %w", dataDir, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("config: stat data dir %s: %w", dataDir, err)
	} else {
		// Directory exists, check permissions (SEC-7)
		if perm := dirInfo.Mode().Perm(); perm != 0700 {
			slog.Warn("data directory permissions too permissive", "path", dataDir, "mode", fmt.Sprintf("%#o", perm), "expected", "0700")
		}
	}

	configFile := filepath.Join(dataDir, "config.yaml")
	cfg := Default()
	cfg.DataDir = dataDir

	fileInfo, err := os.Stat(configFile)
	if os.IsNotExist(err) {
		// Generate random 32-byte API token (CLI-6)
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			return nil, fmt.Errorf("config: generate api token: %w", err)
		}
		cfg.Server.APIToken = hex.EncodeToString(tokenBytes)

		// Serialize and write config.yaml with mode 0600
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("config: marshal new config: %w", err)
		}
		if err := os.WriteFile(configFile, data, 0600); err != nil {
			return nil, fmt.Errorf("config: write new config %s: %w", configFile, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("config: stat config file %s: %w", configFile, err)
	} else {
		// Config file exists, check permissions (SEC-7)
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

	// Validate configuration
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Server.APIToken == "" {
		return fmt.Errorf("config: validation: server.api_token must not be empty")
	}

	host, _, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("config: validation: server.listen %q: %w", cfg.Server.Listen, err)
	}

	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("config: validation: server.listen %q: address must be loopback (127.0.0.1 or localhost)", cfg.Server.Listen)
		}
	}

	return nil
}
