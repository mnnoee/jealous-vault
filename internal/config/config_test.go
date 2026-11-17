// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.toml", `
host = "test-host"
state_file = "/tmp/jealous-vault-state.json"

[repository]
url = "local:/srv/backup"
password_file = "/tmp/restic.pass"

[backup]
paths = ["/etc"]
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Host != "test-host" {
		t.Errorf("host = %q, want test-host", cfg.Host)
	}
	if cfg.ResticBinary != "restic" {
		t.Errorf("restic binary = %q, want restic", cfg.ResticBinary)
	}
	if cfg.BackupTimeout != 6*time.Hour {
		t.Errorf("backup timeout = %s, want 6h", cfg.BackupTimeout)
	}
	if cfg.Retention.KeepDaily != 7 || !cfg.Retention.Prune {
		t.Errorf("retention defaults not applied: %+v", cfg.Retention)
	}
	if cfg.Schedule.OnCalendar != "*-*-* 03:30:00" {
		t.Errorf("schedule = %q", cfg.Schedule.OnCalendar)
	}
	if cfg.RandomizedDelay != 15*time.Minute {
		t.Errorf("randomized delay = %s", cfg.RandomizedDelay)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.toml", `
[repository]
url = "local:/srv/backup"
password_file = "/tmp/restic.pass"

[backup]
path = ["/etc"]
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !strings.Contains(err.Error(), "backup.path") {
		t.Fatalf("error does not name the key: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Config)
		want string
	}{
		{
			name: "missing repository",
			mod:  func(c *Config) { c.Repository.URL = "" },
			want: "repository.url",
		},
		{
			name: "missing password",
			mod:  func(c *Config) { c.Repository.PasswordFile = "" },
			want: "password_file",
		},
		{
			name: "no paths",
			mod:  func(c *Config) { c.Backup.Paths = nil },
			want: "backup.paths",
		},
		{
			name: "relative path",
			mod:  func(c *Config) { c.Backup.Paths = []string{"etc"} },
			want: "absolute",
		},
		{
			name: "telegram token without chat",
			mod:  func(c *Config) { c.Notify.TelegramToken = "token" },
			want: "telegram",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Repository.URL = "local:/srv/backup"
			cfg.Repository.PasswordFile = "/tmp/pass"
			cfg.Backup.Paths = []string{"/etc"}
			cfg.BackupTimeout = time.Hour
			cfg.CheckTimeout = time.Hour
			cfg.NotifyTimeout = time.Second
			cfg.RandomizedDelay = 15 * time.Minute
			tc.mod(cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, "restic.env", `
# comment
AWS_ACCESS_KEY_ID=key
export AWS_SECRET_ACCESS_KEY="secret"
QUOTED='value'
`)

	env, err := LoadEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if env["AWS_ACCESS_KEY_ID"] != "key" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q", env["AWS_ACCESS_KEY_ID"])
	}
	if env["AWS_SECRET_ACCESS_KEY"] != "secret" {
		t.Errorf("AWS_SECRET_ACCESS_KEY = %q", env["AWS_SECRET_ACCESS_KEY"])
	}
	if env["QUOTED"] != "value" {
		t.Errorf("QUOTED = %q", env["QUOTED"])
	}
}

func TestEnvFileMergePrefersExplicitValues(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, "restic.env", "FOO=from-file\nBAR=bar\n")
	configPath := writeFile(t, dir, "config.toml", `
[repository]
url = "local:/srv/backup"
password_file = "/tmp/pass"
env_file = `+quote(envPath)+`
env = { FOO = "explicit" }

[backup]
paths = ["/etc"]
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repository.Env["FOO"] != "explicit" {
		t.Errorf("FOO = %q, want explicit", cfg.Repository.Env["FOO"])
	}
	if cfg.Repository.Env["BAR"] != "bar" {
		t.Errorf("BAR = %q, want bar", cfg.Repository.Env["BAR"])
	}
}

func TestRenderExampleLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	err := WriteExample(path, InitOptions{
		Host:          "node-1",
		RepositoryURL: "s3:https://s3.example.com/jealous-vault/node-1",
		PasswordFile:  filepath.Join(dir, "restic.pass"),
		Paths:         []string{"/etc", "/var/www"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if cfg.Host != "node-1" {
		t.Errorf("host = %q", cfg.Host)
	}
	if len(cfg.Backup.Paths) != 2 {
		t.Errorf("paths = %v", cfg.Backup.Paths)
	}

	if err := WriteExample(path, InitOptions{}, false); err == nil {
		t.Fatal("WriteExample overwrote an existing file")
	}
}

func TestGeneratePassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restic.pass")

	if err := GeneratePassword(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(data))) < 32 {
		t.Errorf("password too short: %q", data)
	}

	if err := GeneratePassword(path); err == nil {
		t.Fatal("GeneratePassword overwrote an existing file")
	}
}

func TestRetentionArgs(t *testing.T) {
	cfg := Default()
	cfg.Retention = Retention{KeepLast: 3, KeepDaily: 7, KeepWeekly: 0, KeepMonthly: 6}

	got := strings.Join(cfg.RetentionArgs(), " ")
	want := "--keep-last 3 --keep-daily 7 --keep-monthly 6"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}

	cfg.Retention = Retention{}
	if cfg.HasRetention() {
		t.Fatal("empty retention must not produce forget flags")
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"s3:https://key:secret@example.com/bucket": "s3:https://***@example.com/bucket",
		"https://user:pass@example.com/repo":       "https://***@example.com/repo",
		"local:/srv/repo":                          "local:/srv/repo",
		"s3:https://example.com/bucket":            "s3:https://example.com/bucket",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
