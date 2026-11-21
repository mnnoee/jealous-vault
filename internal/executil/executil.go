// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package executil runs child processes with a sanitized environment and a
// killable process group, so a timeout cannot leave orphaned children or let
// loader variables from the parent reach a root child.
package executil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// SafePath is the PATH given to every child. Configured binaries that are not
// in these directories must be absolute paths.
const SafePath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

var blockedEnv = map[string]bool{
	"LD_PRELOAD":              true,
	"LD_LIBRARY_PATH":         true,
	"LD_AUDIT":                true,
	"LD_DEBUG":                true,
	"BASH_ENV":                true,
	"ENV":                     true,
	"SHELLOPTS":               true,
	"BASHOPTS":                true,
	"SSH_ASKPASS":             true,
	"SSH_ASKPASS_REQUIRE":     true,
	"RESTIC_PASSWORD_COMMAND": true,
	"RESTIC_REPOSITORY":       true,
	"RESTIC_PASSWORD_FILE":    true,
	"RESTIC_PASSWORD":         true,
}

// SanitizeEnv drops loader and restic override variables inherited from the
// parent and forces a fixed PATH. Values from the configuration are appended
// by the caller and win over inherited values.
func SanitizeEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || blockedEnv[key] || key == "PATH" {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "PATH="+SafePath)
}

// Command is exec.CommandContext with a process group kill and a bounded wait.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

// Environment returns the process environment after sanitization.
func Environment() []string {
	return SanitizeEnv(os.Environ())
}

// LookPath resolves a bare command name against SafePath, so a poisoned PATH
// cannot select a different binary.
func LookPath(name string) (string, error) {
	if strings.Contains(name, "/") {
		return exec.LookPath(name)
	}
	for _, dir := range strings.Split(SafePath, ":") {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}
