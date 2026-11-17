// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/state"
)

func testConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Host = "node-1"
	cfg.Repository.URL = "local:" + filepath.Join(dir, "repo")
	cfg.Repository.PasswordFile = filepath.Join(dir, "restic.pass")
	cfg.Backup.Paths = []string{"/etc"}
	cfg.BackupTimeout = time.Minute
	cfg.CheckTimeout = time.Minute
	cfg.NotifyTimeout = time.Second
	cfg.StateFile = filepath.Join(dir, "state.json")
	cfg.Retention = config.Retention{}
	return cfg
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restic")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBackupSuccessRunsHooksAndRecordsState(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	script := writeScript(t, `printf '%s\n' "$*" >> `+argsFile+`
if [ "$1" = "backup" ]; then
  echo '{"message_type":"summary","snapshot_id":"snap123","files_new":3,"files_changed":1,"data_added":2048}'
fi
`)

	cfg := testConfig(t, dir)
	cfg.ResticBinary = script
	preMarker := filepath.Join(dir, "pre")
	postMarker := filepath.Join(dir, "post")
	cfg.Backup.PreHooks = []string{"echo $JM_PHASE > " + preMarker}
	cfg.Backup.PostHooks = []string{"echo $JM_STATUS > " + postMarker}

	r := New(cfg, discardLogger(), io.Discard)
	run, err := r.Backup(context.Background())
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if run.Status != state.StatusSuccess {
		t.Errorf("status = %q", run.Status)
	}
	if run.SnapshotID != "snap123" {
		t.Errorf("snapshot = %q", run.SnapshotID)
	}

	if data, err := os.ReadFile(preMarker); err != nil || strings.TrimSpace(string(data)) != "pre" {
		t.Errorf("pre hook marker = %q, err %v", data, err)
	}
	if data, err := os.ReadFile(postMarker); err != nil || strings.TrimSpace(string(data)) != "success" {
		t.Errorf("post hook marker = %q, err %v", data, err)
	}

	st, err := state.Store{Path: cfg.StateFile}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastSuccess == nil || st.LastSuccess.SnapshotID != "snap123" {
		t.Fatalf("state = %+v", st)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "backup --json --host node-1 /etc") {
		t.Errorf("restic args = %q", args)
	}
}

func TestBackupFailureStillRunsPostHooks(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, `echo "repository is locked" >&2
exit 1
`)

	cfg := testConfig(t, dir)
	cfg.ResticBinary = script
	postMarker := filepath.Join(dir, "post")
	cfg.Backup.PostHooks = []string{"echo $JM_STATUS > " + postMarker}

	r := New(cfg, discardLogger(), io.Discard)
	run, err := r.Backup(context.Background())
	if err == nil {
		t.Fatal("expected a backup error")
	}
	if run.Status != state.StatusFailed {
		t.Errorf("status = %q", run.Status)
	}
	if !strings.Contains(run.Error, "repository is locked") {
		t.Errorf("error = %q", run.Error)
	}
	if data, _ := os.ReadFile(postMarker); strings.TrimSpace(string(data)) != "failed" {
		t.Errorf("post hook marker = %q", data)
	}

	st, err := state.Store{Path: cfg.StateFile}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRun == nil || st.LastRun.Status != state.StatusFailed {
		t.Fatalf("state = %+v", st)
	}
}

func TestFailedPreHookSkipsBackup(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	script := writeScript(t, `printf '%s\n' "$*" >> `+argsFile)
	cfg := testConfig(t, dir)
	cfg.ResticBinary = script
	cfg.Backup.PreHooks = []string{"exit 1"}

	r := New(cfg, discardLogger(), io.Discard)
	run, err := r.Backup(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if run.Status != state.StatusFailed {
		t.Errorf("status = %q", run.Status)
	}
	if _, statErr := os.Stat(argsFile); !os.IsNotExist(statErr) {
		t.Errorf("restic was called after a failed pre hook")
	}
}

func TestCheckRecordsSuccess(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, ":\n")
	cfg := testConfig(t, dir)
	cfg.ResticBinary = script
	cfg.Check.ReadDataSubset = "5%"

	r := New(cfg, discardLogger(), io.Discard)
	run, err := r.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if run.Kind != state.KindCheck || run.Status != state.StatusSuccess {
		t.Fatalf("run = %+v", run)
	}

	st, err := state.Store{Path: cfg.StateFile}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastCheck == nil {
		t.Fatal("last check not recorded")
	}
	if st.LastRun != nil {
		t.Errorf("check must not touch the last backup run: %+v", st.LastRun)
	}
}
