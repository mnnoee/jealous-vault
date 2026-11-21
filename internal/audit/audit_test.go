// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteCreatesJournal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "audit.jsonl")
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	logger := Logger{Path: path, Now: func() time.Time { return now }}

	if err := logger.Write(Entry{
		Action:   "restore",
		Host:     "node-1",
		Snapshot: "abc123",
		Target:   "/srv/data",
		Result:   "success",
	}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Write(Entry{
		Action: "repo.delete",
		Host:   "node-1",
		Result: "failed",
		Error:  "snapshot not found",
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode = %o, want 600", info.Mode().Perm())
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("journal has %d lines, want 2", len(lines))
	}

	var first Entry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Action != "restore" || first.Target != "/srv/data" || first.Snapshot != "abc123" {
		t.Fatalf("unexpected entry: %+v", first)
	}
	if !first.Time.Equal(now) {
		t.Fatalf("time = %v, want %v", first.Time, now)
	}
	if first.UID != os.Getuid() {
		t.Fatalf("uid = %d, want %d", first.UID, os.Getuid())
	}
	if first.User == "" {
		t.Fatal("user is empty")
	}

	var second Entry
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second.Result != "failed" || second.Error != "snapshot not found" {
		t.Fatalf("unexpected entry: %+v", second)
	}
}

func TestWriteDisabledWithoutPath(t *testing.T) {
	if err := (Logger{}).Write(Entry{Action: "restore"}); err != nil {
		t.Fatalf("disabled journal returned an error: %v", err)
	}
}

func TestRecordDoesNotPanicOnBrokenPath(t *testing.T) {
	// A file where a directory is expected makes MkdirAll fail.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := Logger{Path: filepath.Join(blocker, "audit.jsonl")}
	logger.Record(Entry{Action: "restore"})
}

func TestWriteRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "audit.jsonl")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	logger := Logger{Path: link}
	if err := logger.Write(Entry{Action: "test"}); err == nil {
		t.Fatal("expected a symlink error")
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("symlink target was modified: %q", data)
	}
}
