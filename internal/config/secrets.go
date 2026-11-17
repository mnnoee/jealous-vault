// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// allowedEnvPrefixes lists the backend and restic variables that may be passed
// to the restic child process. Anything else could inject loader variables,
// PATH entries or command execution through restic itself.
var allowedEnvPrefixes = []string{
	"RESTIC_",
	"AWS_",
	"B2_",
	"AZURE_",
	"GOOGLE_",
	"OS_",
	"ST_",
	"RCLONE_",
}

var allowedEnvKeys = map[string]bool{
	"HTTP_PROXY":    true,
	"HTTPS_PROXY":   true,
	"NO_PROXY":      true,
	"ALL_PROXY":     true,
	"http_proxy":    true,
	"https_proxy":   true,
	"no_proxy":      true,
	"all_proxy":     true,
	"SSL_CERT_FILE": true,
	"SSL_CERT_DIR":  true,
}

var blockedEnvKeys = map[string]bool{
	// restic runs this value through a shell, so it is code execution, not
	// configuration.
	"RESTIC_PASSWORD_COMMAND": true,
	// The repository comes from repository.url, so an env override would
	// silently redirect a backup somewhere else.
	"RESTIC_REPOSITORY": true,
	// Use repository.password_file instead of a second password source.
	"RESTIC_PASSWORD_FILE": true,
}

// ValidateEnvKey accepts only variables that configure restic or its storage
// backends. This keeps a writable env file from becoming root code execution
// through LD_PRELOAD, PATH, SSH_ASKPASS or RESTIC_PASSWORD_COMMAND.
func ValidateEnvKey(key string) error {
	if key == "" {
		return errors.New("environment key is empty")
	}
	if blockedEnvKeys[key] {
		return fmt.Errorf("environment key %q is not allowed", key)
	}
	for _, prefix := range allowedEnvPrefixes {
		if strings.HasPrefix(key, prefix) {
			return nil
		}
	}
	if allowedEnvKeys[key] {
		return nil
	}
	return fmt.Errorf("environment key %q is not allowed", key)
}

// CheckSecretFile verifies that a file holding a password, token or key is a
// regular file owned by the agent user and not readable by group or others.
func CheckSecretFile(label, path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s %s: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s %s is not a regular file", label, path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s %s is readable by group or others (%o), chmod 600", label, path, perm)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("%s %s is owned by uid %d, expected %d", label, path, stat.Uid, os.Geteuid())
	}
	return nil
}

// CheckSecretFiles validates every secret path referenced by the configuration.
func (c *Config) CheckSecretFiles() error {
	checks := []struct {
		label string
		path  string
	}{
		{"config", c.Path},
		{"repository.password_file", c.Repository.PasswordFile},
		{"repository.env_file", c.Repository.EnvFile},
		{"repository.sftp_key_file", c.Repository.SFTPKeyFile},
		{"copy.password_file", c.Copy.PasswordFile},
		{"copy.env_file", c.Copy.EnvFile},
		{"notify.email.password_file", c.Notify.Email.PasswordFile},
	}
	if p := c.Backup.Dumps.Postgres; p != nil && p.Enabled {
		checks = append(checks, struct {
			label string
			path  string
		}{"backup.dumps.postgres.password_file", p.PasswordFile})
	}
	if m := c.Backup.Dumps.MySQL; m != nil && m.Enabled {
		checks = append(checks, struct {
			label string
			path  string
		}{"backup.dumps.mysql.password_file", m.PasswordFile})
	}
	for _, check := range checks {
		if err := CheckSecretFile(check.label, check.path); err != nil {
			return err
		}
	}
	return nil
}
