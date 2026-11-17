// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package recovery writes a self-contained recovery kit for one repository.
package recovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
)

// Write creates output with mode 0600. It refuses to overwrite unless force is
// set. The kit contains repository credentials, so it is a secret document.
func Write(cfg *config.Config, output string, force bool, version string) error {
	if output == "" {
		return errors.New("output path is required")
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(output, flags, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}

	password, err := readSecret(cfg.Repository.PasswordFile)
	if err != nil {
		return fmt.Errorf("repository password: %w", err)
	}
	envFile, err := readOptional(cfg.Repository.EnvFile)
	if err != nil {
		return fmt.Errorf("repository env file: %w", err)
	}
	sshKey, err := readOptional(cfg.Repository.SFTPKeyFile)
	if err != nil {
		return fmt.Errorf("sftp key: %w", err)
	}
	configBody, err := readOptional(cfg.Path)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "JealousVault recovery kit\n")
	fmt.Fprintf(&b, "generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "agent:     %s\n", version)
	fmt.Fprintf(&b, "host:      %s\n", cfg.Host)
	fmt.Fprintf(&b, "config:    %s\n", cfg.Path)
	fmt.Fprintf(&b, "repository: %s\n", cfg.Repository.URL)
	fmt.Fprintf(&b, "\n[password]\n%s\n", password)

	fmt.Fprintf(&b, "\n[restore]\n")
	fmt.Fprintf(&b, "export RESTIC_REPOSITORY='%s'\n", strings.ReplaceAll(cfg.Repository.URL, "'", `'\''`))
	fmt.Fprintf(&b, "export RESTIC_PASSWORD='%s'\n", strings.ReplaceAll(password, "'", `'\''`))
	fmt.Fprintf(&b, "restic snapshots\n")
	fmt.Fprintf(&b, "restic restore latest --target /\n")
	fmt.Fprintf(&b, "jealous-vault restore --config %s latest\n", cfg.Path)
	fmt.Fprintf(&b, "\nInstall restic 0.18.x before restoring. Keep this file offline.\n")

	if envFile != "" {
		fmt.Fprintf(&b, "\n[repository env file]\n%s\n", envFile)
	}
	if sshKey != "" {
		fmt.Fprintf(&b, "\n[sftp private key]\n%s\n", sshKey)
	}
	if configBody != "" {
		fmt.Fprintf(&b, "\n[config]\n%s\n", configBody)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	return f.Sync()
}

func readSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readOptional(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
