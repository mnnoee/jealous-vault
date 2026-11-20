// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":             "''",
		"restic":       "restic",
		"/etc/passwd":  "/etc/passwd",
		"a b":          "'a b'",
		"it's":         `'it'\''s'`,
		"*.log":        "'*.log'",
		"s3:https://x": "s3:https://x",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildResticExport(t *testing.T) {
	t.Setenv("RESTIC_CACHE_DIR", "")
	cache := t.TempDir()
	cfg := config.Default()
	cfg.Job = "usr"
	cfg.Host = "host1"
	cfg.ResticBinary = "restic"
	cfg.Backup.Profile = "custom"
	cfg.Backup.Paths = []string{"/etc", "/home"}
	cfg.Backup.Excludes = []string{"/tmp", "*.log"}
	cfg.Backup.Tags = []string{"nightly"}
	cfg.Backup.OneFileSystem = true
	cfg.Backup.Compression = "max"
	cfg.Backup.LimitUpload = 1000
	cfg.Backup.PackSize = 16
	cfg.Repository.URL = "s3:https://key:secret@example/bucket"
	cfg.Repository.PasswordFile = "/etc/jealous-vault/restic.pass"
	cfg.Repository.CacheDir = cache
	cfg.Repository.EnvFile = "/etc/jealous-vault/restic.env"
	cfg.Repository.Env = map[string]string{
		"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE",
		"RESTIC_PASSWORD":   "topsecret",
	}

	export := buildResticExport(cfg)
	shell := export.Shell

	for _, want := range []string{
		"# JealousVault export: job \"usr\", profile \"custom\"",
		"export RESTIC_REPOSITORY=",
		"export RESTIC_PASSWORD_FILE=/etc/jealous-vault/restic.pass",
		"export RESTIC_CACHE_DIR=" + cache,
		". /etc/jealous-vault/restic.env",
		"restic backup --host host1 --one-file-system --limit-upload 1000 --compression max --pack-size 16 --tag nightly --exclude /tmp --exclude '*.log' /etc /home",
		"restic forget --keep-last 3 --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune",
		"restic snapshots",
		"restic restore latest --target /",
	} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell does not contain %q:\n%s", want, shell)
		}
	}
	for _, secret := range []string{"topsecret", "AKIAEXAMPLE"} {
		if strings.Contains(shell, secret) {
			t.Errorf("shell leaked %q:\n%s", secret, shell)
		}
	}
	if strings.Contains(strings.Join(export.EnvKeys, ","), "RESTIC_PASSWORD") {
		t.Errorf("RESTIC_* keys must not be listed as backend env: %v", export.EnvKeys)
	}
	if len(export.BackupCommand) == 0 || export.BackupCommand[0] != "restic" {
		t.Errorf("backup command = %v", export.BackupCommand)
	}
}

func TestBuildResticExportIncludesDumpDir(t *testing.T) {
	t.Setenv("RESTIC_CACHE_DIR", "")
	cfg := config.Default()
	cfg.Host = "host1"
	cfg.Repository.URL = "local:/srv/repo"
	cfg.Repository.PasswordFile = "/etc/jealous-vault/restic.pass"
	cfg.Repository.CacheDir = t.TempDir()
	cfg.Backup.Paths = []string{"/etc"}
	cfg.Backup.Dumps.Dir = "/var/lib/jealous-vault/dumps"
	cfg.Backup.Dumps.Postgres = &config.PostgresDump{Enabled: true}

	export := buildResticExport(cfg)
	joined := strings.Join(export.BackupCommand, " ")
	if !strings.Contains(joined, "/var/lib/jealous-vault/dumps") {
		t.Fatalf("dump dir is missing: %s", joined)
	}
	if !strings.Contains(export.Shell, "run the database dumps first") {
		t.Fatalf("dump warning is missing:\n%s", export.Shell)
	}
}

func TestBuildResticExportWithoutPasswordFile(t *testing.T) {
	t.Setenv("RESTIC_CACHE_DIR", "")
	cfg := config.Default()
	cfg.Repository.URL = "local:/srv/repo"
	cfg.Repository.PasswordFile = ""
	cfg.Repository.CacheDir = t.TempDir()
	cfg.Repository.Env = map[string]string{"RESTIC_PASSWORD": "topsecret"}
	cfg.Backup.Paths = []string{"/etc"}

	export := buildResticExport(cfg)
	if !strings.Contains(export.Shell, "set RESTIC_PASSWORD first") {
		t.Fatalf("missing password guard:\n%s", export.Shell)
	}
	if strings.Contains(export.Shell, "topsecret") {
		t.Fatalf("password leaked:\n%s", export.Shell)
	}
}
