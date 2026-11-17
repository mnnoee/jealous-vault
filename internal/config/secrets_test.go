// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEnvKey(t *testing.T) {
	allowed := []string{
		"RESTIC_PASSWORD",
		"RESTIC_CACHE_DIR",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"B2_ACCOUNT_ID",
		"AZURE_ACCOUNT_NAME",
		"GOOGLE_PROJECT_ID",
		"OS_AUTH_URL",
		"ST_AUTH",
		"RCLONE_CONFIG",
		"HTTPS_PROXY",
		"SSL_CERT_FILE",
	}
	for _, key := range allowed {
		if err := ValidateEnvKey(key); err != nil {
			t.Errorf("ValidateEnvKey(%q) = %v, want nil", key, err)
		}
	}

	blocked := []string{
		"LD_PRELOAD",
		"LD_LIBRARY_PATH",
		"PATH",
		"BASH_ENV",
		"SSH_ASKPASS",
		"RESTIC_PASSWORD_COMMAND",
		"RESTIC_REPOSITORY",
		"GODEBUG",
	}
	for _, key := range blocked {
		if err := ValidateEnvKey(key); err == nil {
			t.Errorf("ValidateEnvKey(%q) = nil, want an error", key)
		}
	}
}

func TestLoadEnvFileRejectsDangerousKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restic.env")
	if err := os.WriteFile(path, []byte("LD_PRELOAD=/tmp/evil.so\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnvFile(path); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("LoadEnvFile = %v, want a key rejection", err)
	}
}

func TestLoadEnvFileRejectsLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restic.env")
	if err := os.WriteFile(path, []byte("AWS_ACCESS_KEY_ID=key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnvFile(path); err == nil || !strings.Contains(err.Error(), "readable by group or others") {
		t.Fatalf("LoadEnvFile = %v, want a permission rejection", err)
	}
}

func TestCheckSecretFile(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")

	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckSecretFile("secret", secret); err != nil {
		t.Errorf("0600 file rejected: %v", err)
	}

	if err := os.Chmod(secret, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckSecretFile("secret", secret); err == nil {
		t.Fatal("0644 file accepted")
	}
}

func TestConfigRejectsDangerousInlineEnv(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.toml", `
[repository]
url = "local:/srv/backup"
password_file = "/tmp/pass"
env = { LD_PRELOAD = "/tmp/evil.so" }

[backup]
paths = ["/etc"]
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("Load = %v, want a key rejection", err)
	}
}

func TestPresetExcludesAgentSecrets(t *testing.T) {
	for _, proxmox := range []bool{false, true} {
		preset := PresetFor("host", proxmox)
		found := false
		for _, exclude := range preset.Excludes {
			if exclude == "/etc/jealous-vault" {
				found = true
			}
		}
		if !found {
			t.Errorf("host preset (proxmox=%v) does not exclude /etc/jealous-vault: %v", proxmox, preset.Excludes)
		}
	}
}
