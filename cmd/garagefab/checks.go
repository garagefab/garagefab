package main

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var gitVersionRegex = regexp.MustCompile(`git version (\d+)\.(\d+)(?:\.(\d+))?`)

func validateGitVersionOutput(output string) error {
	matches := gitVersionRegex.FindStringSubmatch(output)
	if len(matches) < 3 {
		return fmt.Errorf("cannot parse git version from output: %q", strings.TrimSpace(output))
	}

	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return fmt.Errorf("invalid git major version: %w", err)
	}

	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return fmt.Errorf("invalid git minor version: %w", err)
	}

	if major < 2 || (major == 2 && minor < 30) {
		return fmt.Errorf("git version 2.30+ is required, found %d.%d", major, minor)
	}

	return nil
}

func checkGitAvailable() error {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return fmt.Errorf("startup check: git is not installed or not in PATH: %w", err)
	}
	if err := validateGitVersionOutput(string(out)); err != nil {
		return fmt.Errorf("startup check: %w", err)
	}
	return nil
}

func checkPortAvailable(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("startup check: port on %s is not available (already in use): %w", addr, err)
	}
	_ = ln.Close()
	return nil
}
