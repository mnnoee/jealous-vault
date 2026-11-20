// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmailValidation(t *testing.T) {
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
	cfg.DrillTimeout = 2 * time.Hour
	cfg.DrillDBTimeout = 30 * time.Minute
	cfg.DrillMaxSize = 10_000_000_000
	cfg.DrillMinFree = 20_000_000_000
	cfg.EmailTimeout = 15 * time.Second

	cfg.Notify.Email = Email{
		Enabled:  true,
		Mode:     "smtp",
		From:     "backups@example.com",
		To:       []string{"admin@example.com"},
		Host:     "smtp.example.com",
		Port:     587,
		Username: "user",
		Security: "starttls",
		Timeout:  "15s",
	}
	cfg.EmailPassword = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid email config rejected: %v", err)
	}

	cases := []struct {
		name string
		mod  func(*Config)
	}{
		{"bad from", func(c *Config) { c.Notify.Email.From = "nope" }},
		{"no recipients", func(c *Config) { c.Notify.Email.To = nil }},
		{"smtp without host", func(c *Config) { c.Notify.Email.Host = "" }},
		{"auth without password", func(c *Config) { c.EmailPassword = "" }},
		{"none with auth", func(c *Config) { c.Notify.Email.Security = "none" }},
		{"bad mode", func(c *Config) { c.Notify.Email.Mode = "carrier-pigeon" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clone := *cfg
			clone.Notify.Email = cfg.Notify.Email
			clone.Notify.Email.To = append([]string(nil), cfg.Notify.Email.To...)
			tc.mod(&clone)
			if err := clone.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestEmailPasswordFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	passwordFile := filepath.Join(dir, "smtp.pass")
	if err := os.WriteFile(passwordFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[repository]
url = "local:/srv/repo"
password_file = "/tmp/pass"

[backup]
paths = ["/etc"]

[notify.email]
enabled = true
mode = "smtp"
from = "backups@example.com"
to = ["admin@example.com"]
host = "smtp.example.com"
port = 587
username = "user"
password_file = "`+passwordFile+`"
security = "starttls"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmailPassword != "from-file" {
		t.Errorf("password = %q", cfg.EmailPassword)
	}

	t.Setenv("JV_SMTP_PASSWORD", "from-env")
	path2 := filepath.Join(dir, "config-env.toml")
	body := `
[repository]
url = "local:/srv/repo"
password_file = "/tmp/pass"

[backup]
paths = ["/etc"]

[notify.email]
enabled = true
mode = "smtp"
from = "backups@example.com"
to = ["admin@example.com"]
host = "smtp.example.com"
port = 587
username = "user"
password_env = "JV_SMTP_PASSWORD"
security = "starttls"
`
	if err := os.WriteFile(path2, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmailPassword != "from-env" {
		t.Errorf("password = %q", cfg.EmailPassword)
	}
}
