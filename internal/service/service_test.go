// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSystemctl(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	path := filepath.Join(dir, "systemctl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path, logPath
}

func TestRun(t *testing.T) {
	_, calls := fakeSystemctl(t, "exit 0\n")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := Run(context.Background(), log, "stop", "postgresql"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "stop postgresql" {
		t.Fatalf("calls = %q", data)
	}
}

func TestRunFailureIncludesOutput(t *testing.T) {
	fakeSystemctl(t, "echo 'unit not found' >&2\nexit 1\n")
	err := Run(context.Background(), nil, "start", "missing")
	if err == nil || !strings.Contains(err.Error(), "unit not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunEmptyNameIsNoop(t *testing.T) {
	if err := Run(context.Background(), nil, "stop", ""); err != nil {
		t.Fatal(err)
	}
}
