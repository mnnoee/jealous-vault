// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func fakeCommand(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("# test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	passwordPath := filepath.Join(dir, "restic.pass")
	if err := os.WriteFile(passwordPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Path = configPath
	cfg.ResticBinary = fakeCommand(t, dir, "restic", `case "$1" in
version) echo "restic 0.18.0 fake";;
cat) exit 0;;
*) exit 0;;
esac
`)
	cfg.Repository.URL = "local:" + filepath.Join(dir, "repo")
	cfg.Repository.PasswordFile = passwordPath
	cfg.StateFile = filepath.Join(dir, "state", "state.json")
	cfg.Backup.Paths = []string{dataPath}
	return cfg
}

func TestRunHealthy(t *testing.T) {
	cfg := testConfig(t)
	systemctl := fakeCommand(t, t.TempDir(), "systemctl", "exit 0\n")

	report := Run(context.Background(), cfg, Options{Systemctl: systemctl})
	if report.Failed {
		t.Fatalf("unexpected failure: %+v", report.Results)
	}
	for _, name := range []string{"config", "password", "restic", "repository", "paths", "state", "disk", "systemd"} {
		if !hasResult(report, name, LevelOK) {
			t.Errorf("check %q is not ok: %+v", name, report.Results)
		}
	}
}

func TestS3InstanceRoleIsNotAWarning(t *testing.T) {
	cfg := testConfig(t)
	cfg.Repository.URL = "s3:https://s3.example.com/bucket"
	systemctl := fakeCommand(t, t.TempDir(), "systemctl", "exit 0\n")

	report := Run(context.Background(), cfg, Options{Systemctl: systemctl})
	if report.Failed {
		t.Fatalf("unexpected failure: %+v", report.Results)
	}
	if !hasResult(report, "s3", LevelOK) {
		t.Errorf("s3 without static keys must be OK: %+v", report.Results)
	}
}

func TestRunMissingPath(t *testing.T) {
	cfg := testConfig(t)
	systemctl := fakeCommand(t, t.TempDir(), "systemctl", "exit 0\n")
	cfg.Backup.Paths = []string{filepath.Join(t.TempDir(), "missing")}

	report := Run(context.Background(), cfg, Options{Systemctl: systemctl})
	if !report.Failed {
		t.Fatal("expected a failure for a missing path")
	}
	if !hasResult(report, "paths", LevelFail) {
		t.Errorf("paths check did not fail: %+v", report.Results)
	}
}

func TestRunMissingPassword(t *testing.T) {
	cfg := testConfig(t)
	systemctl := fakeCommand(t, t.TempDir(), "systemctl", "exit 0\n")
	cfg.Repository.PasswordFile = filepath.Join(t.TempDir(), "missing.pass")

	report := Run(context.Background(), cfg, Options{Systemctl: systemctl})
	if !report.Failed {
		t.Fatal("expected a failure for a missing password")
	}
	if !hasResult(report, "password", LevelFail) {
		t.Errorf("password check did not fail: %+v", report.Results)
	}
}

func TestRunNoSystemd(t *testing.T) {
	cfg := testConfig(t)

	report := Run(context.Background(), cfg, Options{Systemctl: filepath.Join(t.TempDir(), "missing")})
	if report.Failed {
		t.Fatalf("missing systemd must be a warning, not a failure: %+v", report.Results)
	}
	if !hasResult(report, "systemd", LevelWarn) {
		t.Errorf("systemd check did not warn: %+v", report.Results)
	}
}

func hasResult(report Report, name string, level Level) bool {
	for _, r := range report.Results {
		if r.Name == name && r.Level == level {
			return true
		}
	}
	return false
}

func TestSupportedResticVersionIsOK(t *testing.T) {
	cfg := testConfig(t)
	cfg.ResticBinary = fakeCommand(t, t.TempDir(), "restic", `case "$1" in
version) echo "restic 0.19.1 fake";;
cat) exit 0;;
*) exit 0;;
esac
`)
	systemctl := fakeCommand(t, t.TempDir(), "systemctl", "exit 0\n")

	report := Run(context.Background(), cfg, Options{Systemctl: systemctl})
	if !hasResult(report, "restic", LevelOK) {
		t.Errorf("supported version was not accepted: %+v", report.Results)
	}
}

func TestUntestedResticVersionWarns(t *testing.T) {
	cfg := testConfig(t)
	cfg.ResticBinary = fakeCommand(t, t.TempDir(), "restic", `case "$1" in
version) echo "restic 0.20.0 fake";;
cat) exit 0;;
*) exit 0;;
esac
`)
	systemctl := fakeCommand(t, t.TempDir(), "systemctl", "exit 0\n")

	report := Run(context.Background(), cfg, Options{Systemctl: systemctl})
	if report.Failed {
		t.Fatalf("version mismatch must be a warning: %+v", report.Results)
	}
	if !hasResult(report, "restic", LevelWarn) {
		t.Errorf("version check did not warn: %+v", report.Results)
	}
}
