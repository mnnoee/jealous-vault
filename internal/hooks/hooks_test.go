// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunSetsEnvironment(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")

	commands := []string{"printf '%s:%s' \"$JM_PHASE\" \"$CUSTOM\" > " + marker}
	if err := Run(context.Background(), testLogger(), "pre", commands, map[string]string{
		"JM_PHASE": "pre",
		"CUSTOM":   "value",
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pre:value" {
		t.Fatalf("marker = %q", data)
	}
}

func TestRunStopsAtFailure(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")

	commands := []string{
		"echo one > " + first,
		"exit 3",
		"echo two > " + second,
	}
	err := Run(context.Background(), testLogger(), "backup-pre", commands, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "backup-pre") {
		t.Errorf("error does not name the phase: %v", err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Errorf("first command did not run: %v", err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Errorf("commands continued after a failure")
	}
}

func TestRunWithoutCommands(t *testing.T) {
	if err := Run(context.Background(), nil, "post", nil, nil); err != nil {
		t.Fatalf("empty hook list returned an error: %v", err)
	}
}

func TestRunErrorDoesNotLeakCommand(t *testing.T) {
	err := Run(context.Background(), nil, "backup-pre", []string{"exit 7"}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "exit 7") {
		t.Fatalf("hook command leaked into the error: %v", err)
	}
}
