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
	if cfg.LockFile != "/tmp/backup.lock" {
		t.Errorf("lock file = %q, want /tmp/backup.lock", cfg.LockFile)
	}
	if cfg.AlertsFile != "/tmp/alerts.json" {
		t.Errorf("alerts file = %q, want /tmp/alerts.json", cfg.AlertsFile)
	}
	if cfg.BackupTimeout != 6*time.Hour {
		t.Errorf("backup timeout = %s, want 6h", cfg.BackupTimeout)
	}
	if cfg.Retention.KeepDaily != 7 || !cfg.Retention.Prune {
		t.Errorf("retention defaults not applied: %+v", cfg.Retention)
	}
	if cfg.Backup.Profile != "host" {
		t.Errorf("profile = %q, want host", cfg.Backup.Profile)
	}
	if cfg.Schedule.OnCalendar != "*-*-* 03:30:00" {
		t.Errorf("schedule = %q", cfg.Schedule.OnCalendar)
	}
	if cfg.RandomizedDelay != time.Hour {
		t.Errorf("randomized delay = %s", cfg.RandomizedDelay)
	}
	if cfg.FreshnessMaxAge != 25*time.Hour {
		t.Errorf("freshness max age = %s", cfg.FreshnessMaxAge)
	}
	if !cfg.Freshness.RespectBackupTimer {
		t.Error("freshness must respect the backup timer by default")
	}
	if !cfg.Verify.Enabled || cfg.Verify.Level != "files" {
		t.Errorf("verify defaults = %+v", cfg.Verify)
	}
	if cfg.VerifyTimeout != 2*time.Hour {
		t.Errorf("verify timeout = %s", cfg.VerifyTimeout)
	}
	if cfg.VerifyMaxSize != 10_000_000_000 {
		t.Errorf("verify max size = %d", cfg.VerifyMaxSize)
	}
	if cfg.Proxmox.Mode != "snapshot" || cfg.Proxmox.KeepLast != 1 || !cfg.Proxmox.All {
		t.Errorf("proxmox defaults = %+v", cfg.Proxmox)
	}
	if cfg.Notify.Email.Mode != "local" || cfg.Notify.Email.Port != 25 {
		t.Errorf("email defaults = %+v", cfg.Notify.Email)
	}
	if cfg.EmailTimeout != 15*time.Second {
		t.Errorf("email timeout = %s", cfg.EmailTimeout)
	}
	if !cfg.Copy.NewOnly || cfg.Copy.Enabled {
		t.Errorf("copy defaults = %+v", cfg.Copy)
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
		{
			name: "copy enabled without repository",
			mod:  func(c *Config) { c.Copy.Enabled = true },
			want: "copy.repository",
		},
		{
			name: "copy reuses primary password",
			mod: func(c *Config) {
				c.Copy.Enabled = true
				c.Copy.Repository = "local:/copy"
				c.Copy.PasswordFile = c.Repository.PasswordFile
			},
			want: "must differ",
		},
		{
			name: "unknown proxmox mode",
			mod:  func(c *Config) { c.Proxmox.Mode = "magic" },
			want: "proxmox.mode",
		},
		{
			name: "relative dump dir",
			mod:  func(c *Config) { c.Proxmox.DumpDir = "dump" },
			want: "proxmox.dump_dir",
		},
		{
			name: "negative keep last",
			mod:  func(c *Config) { c.Proxmox.KeepLast = -1 },
			want: "proxmox.keep_last",
		},
		{
			name: "unknown verify level",
			mod:  func(c *Config) { c.Verify.Level = "magic" },
			want: "verify.level",
		},
		{
			name: "service verify without hook",
			mod:  func(c *Config) { c.Verify.Level = "service" },
			want: "verify.post_hook",
		},
		{
			name: "unknown profile",
			mod:  func(c *Config) { c.Backup.Profile = "everything" },
			want: "backup.profile",
		},
		{
			name: "relative alerts file",
			mod:  func(c *Config) { c.AlertsFile = "alerts.json" },
			want: "alerts_file",
		},
		{
			name: "relative lock file",
			mod:  func(c *Config) { c.LockFile = "backup.lock" },
			want: "lock_file",
		},
		{
			name: "relative state file",
			mod:  func(c *Config) { c.StateFile = "state.json" },
			want: "state_file",
		},
		{
			name: "relative password file",
			mod:  func(c *Config) { c.Repository.PasswordFile = "restic.pass" },
			want: "password_file",
		},
		{
			name: "sftp fields on a local repository",
			mod:  func(c *Config) { c.Repository.SFTPKeyFile = "/tmp/key" },
			want: "sftp",
		},
		{
			name: "relative env file",
			mod:  func(c *Config) { c.Repository.EnvFile = "restic.env" },
			want: "env_file",
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
			cfg.FreshnessMaxAge = 25 * time.Hour
			cfg.VerifyTimeout = 2 * time.Hour
			cfg.VerifyMaxSize = 10_000_000_000
			cfg.VerifyMinFree = 20_000_000_000
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
RESTIC_QUOTED='value'
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
	if env["RESTIC_QUOTED"] != "value" {
		t.Errorf("RESTIC_QUOTED = %q", env["RESTIC_QUOTED"])
	}
}

func TestEnvFileMergePrefersExplicitValues(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, "restic.env", "RESTIC_FOO=from-file\nRESTIC_BAR=bar\n")
	configPath := writeFile(t, dir, "config.toml", `
[repository]
url = "local:/srv/backup"
password_file = "/tmp/pass"
env_file = `+quote(envPath)+`
env = { RESTIC_FOO = "explicit" }

[backup]
paths = ["/etc"]
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repository.Env["RESTIC_FOO"] != "explicit" {
		t.Errorf("RESTIC_FOO = %q, want explicit", cfg.Repository.Env["RESTIC_FOO"])
	}
	if cfg.Repository.Env["RESTIC_BAR"] != "bar" {
		t.Errorf("RESTIC_BAR = %q, want bar", cfg.Repository.Env["RESTIC_BAR"])
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
	if !cfg.Backup.OneFileSystem {
		t.Error("generated config must keep one_file_system enabled")
	}
	if cfg.Backup.Profile != "host" {
		t.Errorf("profile = %q, want host", cfg.Backup.Profile)
	}
	if len(cfg.Backup.Excludes) == 0 {
		t.Error("generated host config has no default excludes")
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

	cfg.Retention.KeepWithin = "1y5m7d"
	cfg.Retention.KeepTag = []string{"important", "weekly"}
	got = strings.Join(cfg.RetentionArgs(), " ")
	want = "--keep-last 3 --keep-daily 7 --keep-monthly 6 --keep-within 1y5m7d --keep-tag important --keep-tag weekly"
	if got != want {
		t.Fatalf("args with within/tags = %q, want %q", got, want)
	}

	cfg.Retention = Retention{}
	if cfg.HasRetention() {
		t.Fatal("empty retention must not produce forget flags")
	}
}

func TestPresetFor(t *testing.T) {
	pve := PresetFor("host", true)
	if len(pve.Paths) != 2 || pve.Paths[1] != "/etc/pve" {
		t.Fatalf("proxmox paths = %v", pve.Paths)
	}
	if len(pve.Excludes) == 0 {
		t.Fatal("proxmox host profile has no default excludes")
	}

	plain := PresetFor("host", false)
	if len(plain.Paths) != 2 || plain.Paths[0] != "/etc" {
		t.Fatalf("plain linux paths = %v", plain.Paths)
	}

	dumps := PresetFor("vm-dumps", true)
	if len(dumps.Paths) != 1 || dumps.Paths[0] != "/var/lib/vz/dump" {
		t.Fatalf("vm-dumps paths = %v", dumps.Paths)
	}
	if len(dumps.Excludes) != 0 {
		t.Fatalf("vm-dumps excludes = %v", dumps.Excludes)
	}

	custom := PresetFor("custom", true)
	if len(custom.Paths) != 0 || len(custom.Excludes) != 0 {
		t.Fatalf("custom preset = %+v", custom)
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

func TestWriteExampleRejectsRelativePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	err := WriteExample(path, InitOptions{
		Host:         "node-1",
		PasswordFile: filepath.Join(dir, "restic.pass"),
		StateFile:    "state.json",
	}, false)
	if err == nil {
		t.Fatal("expected an error for a relative state file")
	}
	if !strings.Contains(err.Error(), "state_file") {
		t.Fatalf("error does not name the key: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("invalid config was written to disk")
	}
}

func TestRenderExampleVMProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	err := WriteExample(path, InitOptions{
		Host:          "pve",
		RepositoryURL: "local:/srv/repo",
		PasswordFile:  filepath.Join(dir, "restic.pass"),
		Profile:       "vm-dumps",
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backup.Profile != "vm-dumps" {
		t.Errorf("profile = %q", cfg.Backup.Profile)
	}
	if len(cfg.Backup.Paths) != 1 || cfg.Backup.Paths[0] != "/var/lib/vz/dump" {
		t.Errorf("paths = %v", cfg.Backup.Paths)
	}
	if len(cfg.Backup.Excludes) != 0 {
		t.Errorf("excludes = %v", cfg.Backup.Excludes)
	}
}

func TestRenderExampleCustomWithoutPathsFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	err := WriteExample(path, InitOptions{
		Host:          "node-1",
		RepositoryURL: "local:/srv/repo",
		PasswordFile:  filepath.Join(dir, "restic.pass"),
		Profile:       "custom",
	}, false)
	if err == nil {
		t.Fatal("expected an error for a custom profile without paths")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("invalid config was written")
	}
}

func TestSFTPDefaults(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.toml", `
[repository]
url = "sftp:backup@host:/srv/backup"
password_file = "/tmp/pass"
sftp_key_file = "/etc/jealous-vault/ssh_key"

[backup]
paths = ["/etc"]
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repository.SFTPConfig != "/etc/jealous-vault/ssh_config" {
		t.Errorf("sftp config = %q", cfg.Repository.SFTPConfig)
	}
	if cfg.Repository.SFTPKnownHosts != "/etc/jealous-vault/known_hosts" {
		t.Errorf("known hosts = %q", cfg.Repository.SFTPKnownHosts)
	}
}

func TestRenderExampleProxmox(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	err := WriteExample(path, InitOptions{
		Host:          "pve",
		RepositoryURL: "local:/srv/repo",
		PasswordFile:  filepath.Join(dir, "restic.pass"),
		Proxmox:       true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Proxmox.Enabled {
		t.Fatal("proxmox is not enabled in the generated config")
	}
	if cfg.Proxmox.DumpDir != "/var/lib/vz/dump/jealous-vault" {
		t.Errorf("dump dir = %q", cfg.Proxmox.DumpDir)
	}
}

func TestTelegramChats(t *testing.T) {
	cfg := validConfigForValidation()
	cfg.Notify.TelegramToken = "token"
	cfg.Notify.TelegramChatIDs = []string{"1909549916", "-5365208231", "-1001234567890:42"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid chats rejected: %v", err)
	}

	for _, invalid := range []string{"abc", "123:0", "123:-1", ""} {
		cfg := validConfigForValidation()
		cfg.Notify.TelegramToken = "token"
		cfg.Notify.TelegramChatIDs = []string{invalid}
		if err := cfg.Validate(); err == nil {
			t.Errorf("chat %q was accepted", invalid)
		}
	}
}

func validConfigForValidation() *Config {
	cfg := Default()
	cfg.Repository.URL = "local:/srv/repo"
	cfg.Repository.PasswordFile = "/tmp/pass"
	cfg.Backup.Paths = []string{"/etc"}
	cfg.BackupTimeout = time.Hour
	cfg.CheckTimeout = time.Hour
	cfg.NotifyTimeout = time.Second
	cfg.RandomizedDelay = time.Hour
	cfg.FreshnessMaxAge = 25 * time.Hour
	cfg.VerifyTimeout = 2 * time.Hour
	cfg.VerifyMaxSize = 10_000_000_000
	cfg.VerifyMinFree = 20_000_000_000
	cfg.EmailTimeout = 15 * time.Second
	return cfg
}
