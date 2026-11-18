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
	cfg.AuditFile = filepath.Join(dir, "audit.jsonl")
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

	auditBody, err := os.ReadFile(cfg.AuditFile)
	if err != nil {
		t.Fatalf("audit journal missing: %v", err)
	}
	for _, want := range []string{`"action":"backup"`, `"snapshot":"snap123"`, `"result":"success"`} {
		if !strings.Contains(string(auditBody), want) {
			t.Errorf("audit entry does not contain %q: %s", want, auditBody)
		}
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
	runs, err := state.Store{Path: cfg.StateFile}.History(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != state.StatusFailed {
		t.Fatalf("failed backup must be in history: %+v", runs)
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
	runs, err := state.Store{Path: cfg.StateFile}.History(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != state.StatusFailed {
		t.Fatalf("failed pre-hook run must be in history: %+v", runs)
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

func TestCheckClearsStaleLocksBeforeChecking(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	script := writeScript(t, `printf '%s\n' "$*" >> `+argsFile+`
case "$1" in
unlock)
  exit 0
  ;;
check)
  exit 0
  ;;
esac
exit 1
`)

	cfg := testConfig(t, dir)
	cfg.ResticBinary = script

	r := New(cfg, discardLogger(), io.Discard)
	if _, err := r.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	unlock := strings.Index(text, "unlock")
	check := strings.Index(text, "check")
	if unlock < 0 || check < 0 || unlock > check {
		t.Fatalf("unlock must run before check: %q", text)
	}
}

func TestBackupRetriesAfterLock(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	marker := filepath.Join(dir, "locked")
	script := writeScript(t, `printf '%s\n' "$*" >> `+argsFile+`
case "$1" in
unlock)
  exit 0
  ;;
backup)
  if [ ! -f `+marker+` ]; then
    touch `+marker+`
    echo "repository is already locked by PID 123" >&2
    exit 1
  fi
  echo '{"message_type":"summary","snapshot_id":"snap-retry"}'
  ;;
esac
`)

	cfg := testConfig(t, dir)
	cfg.ResticBinary = script
	cfg.Retention = config.Retention{}

	r := New(cfg, discardLogger(), io.Discard)
	run, err := r.Backup(context.Background())
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if run.Status != state.StatusSuccess || run.SnapshotID != "snap-retry" {
		t.Fatalf("run = %+v", run)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(args)
	if !strings.Contains(text, "unlock") {
		t.Errorf("unlock was not called: %q", text)
	}
	if got := strings.Count(text, "backup --json"); got != 2 {
		t.Errorf("backup calls = %d, want 2: %q", got, text)
	}
}

func TestBackupRetriesTransientFailure(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	marker := filepath.Join(dir, "failed-once")
	script := writeScript(t, `printf '%s\n' "$*" >> `+argsFile+`
case "$1" in
backup)
  if [ ! -f `+marker+` ]; then
    touch `+marker+`
    echo "connection reset by peer" >&2
    exit 1
  fi
  echo '{"message_type":"summary","snapshot_id":"snap-after-retry"}'
  ;;
esac
`)

	cfg := testConfig(t, dir)
	cfg.ResticBinary = script
	cfg.Backup.Attempts = 2
	cfg.RetryDelay = 0

	r := New(cfg, discardLogger(), io.Discard)
	run, err := r.Backup(context.Background())
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if run.SnapshotID != "snap-after-retry" {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Warnings) == 0 || !strings.Contains(run.Warnings[0], "attempt 1 failed") {
		t.Errorf("retry warning missing: %+v", run.Warnings)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(args), "backup --json"); got != 2 {
		t.Errorf("backup calls = %d, want 2: %q", got, args)
	}
}

func TestBackupAttemptTimeout(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, `exec sleep 30
`)

	cfg := testConfig(t, dir)
	cfg.ResticBinary = script
	cfg.Backup.Attempts = 1
	cfg.BackupAttemptTimeout = 150 * time.Millisecond
	cfg.BackupTimeout = 10 * time.Second

	start := time.Now()
	r := New(cfg, discardLogger(), io.Discard)
	_, err := r.Backup(context.Background())
	if err == nil {
		t.Fatal("hung restic was not interrupted")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a timeout message", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s, the attempt was not interrupted", elapsed)
	}
}

func TestDryRunDoesNotRecordState(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	script := writeScript(t, `printf '%s\n' "$*" >> `+argsFile+`
if [ "$1" = "backup" ]; then
  echo '{"message_type":"summary","snapshot_id":"dry","files_new":1}'
fi
`)
	cfg := testConfig(t, dir)
	cfg.ResticBinary = script

	r := New(cfg, discardLogger(), io.Discard)
	summary, err := r.DryRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.SnapshotID != "dry" {
		t.Fatalf("summary = %+v", summary)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--dry-run") {
		t.Errorf("dry run flag missing: %q", args)
	}
	if _, err := os.Stat(cfg.StateFile); !os.IsNotExist(err) {
		t.Errorf("dry run wrote state: %v", err)
	}
}

func TestBackupIncludesDatabaseDumps(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "restic-args")
	resticScript := writeScript(t, `printf '%s\n' "$*" >> `+argsFile+`
if [ "$1" = "backup" ]; then
  echo '{"message_type":"summary","snapshot_id":"snap-dump"}'
fi
`)
	pgDump := filepath.Join(dir, "pg_dump")
	if err := os.WriteFile(pgDump, []byte(`#!/bin/sh
file=""
for a in "$@"; do case "$a" in --file=*) file="${a#--file=}";; esac; done
printf 'dump' > "$file"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	passwordFile := filepath.Join(dir, "pg.pass")
	if err := os.WriteFile(passwordFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(t, dir)
	cfg.ResticBinary = resticScript
	cfg.Backup.Dumps = config.DumpOptions{
		Dir:  filepath.Join(dir, "dumps"),
		Keep: 1,
		Postgres: &config.PostgresDump{
			Enabled:      true,
			Databases:    []string{"app"},
			Port:         5432,
			PasswordFile: passwordFile,
			Binary:       pgDump,
		},
	}

	r := New(cfg, discardLogger(), io.Discard)
	run, err := r.Backup(context.Background())
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if run.Status != state.StatusSuccess {
		t.Fatalf("run = %+v", run)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), filepath.Join(dir, "dumps", "dump-")) {
		t.Errorf("restic did not receive the dump directory: %q", args)
	}
}
