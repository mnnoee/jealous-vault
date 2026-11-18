// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package dump

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func fakeBinary(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPostgres(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	passwordFile := filepath.Join(dir, "pg.pass")
	if err := os.WriteFile(passwordFile, []byte("pg-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARGS_FILE", argsFile)
	binary := fakeBinary(t, dir, "pg_dump", `file=""
for a in "$@"; do
  case "$a" in --file=*) file="${a#--file=}";; esac
done
printf '%s' "$PGPASSWORD" > "$file"
printf '%s\n' "$*" > "$ARGS_FILE"
`)

	dumpDir := filepath.Join(dir, "dumps")
	m := New(config.DumpOptions{
		Dir:  dumpDir,
		Keep: 1,
		Postgres: &config.PostgresDump{
			Enabled:      true,
			Databases:    []string{"app"},
			Host:         "db",
			Port:         5432,
			User:         "backup",
			PasswordFile: passwordFile,
			Binary:       binary,
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	runDir, err := m.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(runDir, "postgres-app.dump")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pg-secret" {
		t.Fatalf("dump content = %q", data)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("dump mode = %o, want 600", info.Mode().Perm())
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--dbname=app", "--host=db", "--port=5432", "--username=backup"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestRunMySQL(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	passwordFile := filepath.Join(dir, "mysql.pass")
	if err := os.WriteFile(passwordFile, []byte("mysql-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARGS_FILE", argsFile)
	binary := fakeBinary(t, dir, "mysqldump", `file=""
for a in "$@"; do
  case "$a" in --result-file=*) file="${a#--result-file=}";; esac
done
printf 'data' > "$file"
printf '%s\n' "$*" > "$ARGS_FILE"
`)

	m := New(config.DumpOptions{
		Dir:  filepath.Join(dir, "dumps"),
		Keep: 1,
		MySQL: &config.MySQLDump{
			Enabled:      true,
			Databases:    []string{"app"},
			Host:         "db",
			Port:         3306,
			User:         "backup",
			PasswordFile: passwordFile,
			Binary:       binary,
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	runDir, err := m.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "mysql-app.sql")); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(args)
	if !strings.HasPrefix(text, "--defaults-extra-file=") {
		t.Errorf("defaults file is not the first argument: %q", text)
	}
	if strings.Contains(text, "mysql-secret") {
		t.Errorf("password leaked into arguments: %q", text)
	}
	if _, err := os.Stat(filepath.Join(runDir, ".mysql.cnf")); !os.IsNotExist(err) {
		t.Error("defaults file was not removed")
	}
}

func TestRunMySQLIgnoresUserOptionFile(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".my.cnf"), []byte("[client]\npassword=wrong\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	homeFile := filepath.Join(dir, "home-seen")
	t.Setenv("HOME_FILE", homeFile)
	passwordFile := filepath.Join(dir, "mysql.pass")
	if err := os.WriteFile(passwordFile, []byte("mysql-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := fakeBinary(t, dir, "mysqldump", `file=""
for a in "$@"; do
  case "$a" in --result-file=*) file="${a#--result-file=}";; esac
done
printf 'data' > "$file"
printf '%s' "$HOME" > "$HOME_FILE"
`)

	m := New(config.DumpOptions{
		Dir:  filepath.Join(dir, "dumps"),
		Keep: 1,
		MySQL: &config.MySQLDump{
			Enabled:      true,
			Databases:    []string{"app"},
			User:         "backup",
			PasswordFile: passwordFile,
			Binary:       binary,
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	runDir, err := m.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen, err := os.ReadFile(homeFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(seen) != runDir {
		t.Fatalf("dump HOME = %q, want private dump dir %q", seen, runDir)
	}
	if _, err := os.Stat(filepath.Join(runDir, ".my.cnf")); !os.IsNotExist(err) {
		t.Error("user option file appeared in the dump directory")
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"dump-20260101-000000-1", "dump-20260102-000000-2", "dump-20260103-000000-3"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	m := New(config.DumpOptions{
		Dir:      dir,
		Keep:     1,
		Postgres: &config.PostgresDump{Enabled: true, Databases: []string{"app"}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := m.Prune(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "dump-20260103-000000-3" {
		t.Fatalf("entries = %+v", entries)
	}
}
