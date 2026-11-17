// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package recovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	password := filepath.Join(dir, "restic.pass")
	if err := os.WriteFile(password, []byte("secret-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "restic.env")
	if err := os.WriteFile(envFile, []byte("AWS_ACCESS_KEY_ID=key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Host = "node-1"
	cfg.Path = filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg.Path, []byte("host = \"node-1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Repository.URL = "s3:https://example.com/bucket"
	cfg.Repository.PasswordFile = password
	cfg.Repository.EnvFile = envFile
	return cfg
}

func TestWrite(t *testing.T) {
	cfg := testConfig(t)
	output := filepath.Join(t.TempDir(), "kit.txt")
	if err := Write(cfg, output, false, "v0.0.0-test"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"JealousVault recovery kit", "secret-password", "s3:https://example.com/bucket", "AWS_ACCESS_KEY_ID=key", "host = \"node-1\""} {
		if !strings.Contains(string(data), want) {
			t.Errorf("kit does not contain %q:\n%s", want, data)
		}
	}

	if err := Write(cfg, output, false, "v0.0.0-test"); err == nil {
		t.Fatal("existing kit was overwritten without force")
	}
	if err := Write(cfg, output, true, "v0.0.0-test"); err != nil {
		t.Fatalf("force overwrite: %v", err)
	}
}
