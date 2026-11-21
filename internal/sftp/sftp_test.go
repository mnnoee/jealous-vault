// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package sftp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func TestParseRepositoryURL(t *testing.T) {
	cases := []struct {
		raw  string
		user string
		host string
	}{
		{"sftp:backup@10.0.16.152:/srv/backup", "backup", "10.0.16.152"},
		{"sftp:10.0.16.152:/srv/backup", "", "10.0.16.152"},
		{"sftp://backup@backup.example.com/srv/backup", "backup", "backup.example.com"},
		{"sftp:backup+sync@host:/srv/backup", "backup+sync", "host"},
		{"sftp://backup@[2001:db8::1]/srv/backup", "backup", "2001:db8::1"},
	}
	for _, tc := range cases {
		target, err := ParseRepositoryURL(tc.raw)
		if err != nil {
			t.Errorf("ParseRepositoryURL(%q): %v", tc.raw, err)
			continue
		}
		if target.User != tc.user || target.Host != tc.host {
			t.Errorf("ParseRepositoryURL(%q) = %+v", tc.raw, target)
		}
	}

	for _, raw := range []string{
		"local:/srv/repo",
		"sftp:host",
		"sftp:host:",
		"sftp://alice%0AProxyCommand%20id@host/path",
		"sftp://alice%0D%0AProxyCommand%20id@host/path",
		"sftp://-oProxyCommand=id@host/path",
		"sftp:backup@-host:/srv/backup",
		"sftp:backup user@host:/srv/backup",
		"sftp:backup@ho%20st:/srv/backup",
		"sftp:backup@host\nname:/srv/backup",
	} {
		if _, err := ParseRepositoryURL(raw); err == nil {
			t.Errorf("ParseRepositoryURL(%q) did not fail", raw)
		}
	}
}

func TestEnsureConfig(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "ssh_key")
	if err := os.WriteFile(key, []byte("key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sshConfig := filepath.Join(dir, "ssh_config")
	knownHosts := filepath.Join(dir, "known_hosts")

	cfg := config.Default()
	cfg.Repository.URL = "sftp:backup@10.0.16.152:/srv/backup"
	cfg.Repository.SFTPKeyFile = key
	cfg.Repository.SFTPConfig = sshConfig
	cfg.Repository.SFTPKnownHosts = knownHosts
	cfg.Repository.SFTPAcceptNewHostKey = true

	if err := EnsureConfig(cfg); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(sshConfig)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}

	body, err := os.ReadFile(sshConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Host 10.0.16.152",
		"IdentityFile \"" + key + "\"",
		"UserKnownHostsFile \"" + knownHosts + "\"",
		"StrictHostKeyChecking accept-new",
		"BatchMode yes",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("config does not contain %q:\n%s", want, body)
		}
	}
}

func TestEnsureConfigRejectsOpenKey(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "ssh_key")
	if err := os.WriteFile(key, []byte("key\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Repository.URL = "sftp:backup@host:/srv/backup"
	cfg.Repository.SFTPKeyFile = key
	cfg.Repository.SFTPConfig = filepath.Join(dir, "ssh_config")
	cfg.Repository.SFTPKnownHosts = filepath.Join(dir, "known_hosts")

	if err := EnsureConfig(cfg); err == nil {
		t.Fatal("expected an error for a group readable key")
	}
}

func TestEnsureConfigMissingKey(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Repository.URL = "sftp:backup@host:/srv/backup"
	cfg.Repository.SFTPKeyFile = filepath.Join(dir, "missing")
	cfg.Repository.SFTPConfig = filepath.Join(dir, "ssh_config")
	cfg.Repository.SFTPKnownHosts = filepath.Join(dir, "known_hosts")

	if err := EnsureConfig(cfg); err == nil {
		t.Fatal("expected an error for a missing key")
	}
}

func TestResticExtraOptions(t *testing.T) {
	cfg := config.Default()
	cfg.Repository.URL = "sftp:backup@host:/srv/backup"
	cfg.Repository.SFTPConfig = "/etc/jealous-vault/ssh_config"

	got := strings.Join(cfg.ResticExtraOptions(), " ")
	want := "-o sftp.args=-F /etc/jealous-vault/ssh_config"
	if got != want {
		t.Fatalf("options = %q, want %q", got, want)
	}

	cfg.Repository.URL = "local:/srv/repo"
	if cfg.ResticExtraOptions() != nil {
		t.Fatal("non-sftp repository must not get sftp options")
	}
}

func TestEnsureConfigRequiresKnownHosts(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "ssh_key")
	if err := os.WriteFile(key, []byte("key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Repository.URL = "sftp:backup@host:/srv/backup"
	cfg.Repository.SFTPKeyFile = key
	cfg.Repository.SFTPConfig = filepath.Join(dir, "ssh_config")
	cfg.Repository.SFTPKnownHosts = filepath.Join(dir, "known_hosts")

	if err := EnsureConfig(cfg); err == nil {
		t.Fatal("expected missing known_hosts to fail closed")
	}
	if err := os.WriteFile(cfg.Repository.SFTPKnownHosts, []byte("host ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureConfig(cfg); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(cfg.Repository.SFTPConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "StrictHostKeyChecking yes") {
		t.Fatalf("expected StrictHostKeyChecking yes, got:\n%s", body)
	}
}
