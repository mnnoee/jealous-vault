// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package restic

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRestic(t *testing.T, body string) (binary, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	binary = filepath.Join(dir, "restic")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$ARGS_FILE\"\n" + body
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary, filepath.Join(dir, "args.log")
}

func newTestClient(binary, argsFile string) *Client {
	return New(Options{
		Binary:     binary,
		Repository: "local:/srv/repo",
		Env:        map[string]string{"ARGS_FILE": argsFile},
	})
}

func readArgs(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestBackup(t *testing.T) {
	binary, argsFile := fakeRestic(t, `case "$1" in
backup)
  echo '{"message_type":"status","percent_done":0.5}'
  echo '{"message_type":"summary","snapshot_id":"snap123","files_new":2,"files_changed":1,"data_added":4096,"total_duration":1.5}'
  ;;
esac
`)
	client := newTestClient(binary, argsFile)

	summary, err := client.Backup(context.Background(), BackupOptions{
		Host:          "node-1",
		Paths:         []string{"/etc", "/var/www"},
		Excludes:      []string{"/var/cache"},
		Tags:          []string{"nightly"},
		OneFileSystem: true,
		DryRun:        true,
		LimitUpload:   1000,
		Compression:   "max",
		PackSize:      16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.SnapshotID != "snap123" {
		t.Errorf("snapshot = %q", summary.SnapshotID)
	}
	if summary.FilesNew != 2 || summary.FilesChanged != 1 {
		t.Errorf("files = %+v", summary)
	}
	if summary.DataAdded != 4096 {
		t.Errorf("data added = %d", summary.DataAdded)
	}

	args := readArgs(t, argsFile)
	for _, want := range []string{"backup --json", "--host node-1", "--one-file-system", "--tag nightly", "--exclude /var/cache", "--dry-run", "--limit-upload 1000", "--compression max", "--pack-size 16", "/etc /var/www"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestBackupFailureIncludesStderr(t *testing.T) {
	binary, argsFile := fakeRestic(t, `echo "repository is locked" >&2
exit 1
`)
	client := newTestClient(binary, argsFile)

	_, err := client.Backup(context.Background(), BackupOptions{Paths: []string{"/etc"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "repository is locked") {
		t.Fatalf("error does not include stderr: %v", err)
	}
}

func TestBackupWithoutSummary(t *testing.T) {
	binary, argsFile := fakeRestic(t, `echo '{"message_type":"status"}'
`)
	client := newTestClient(binary, argsFile)

	_, err := client.Backup(context.Background(), BackupOptions{Paths: []string{"/etc"}})
	if err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("expected a summary error, got %v", err)
	}
}

func TestSnapshots(t *testing.T) {
	binary, argsFile := fakeRestic(t, `case "$1" in
snapshots)
  echo '[{"id":"snap123","short_id":"snap123","time":"2026-01-02T03:04:05Z","hostname":"node-1","paths":["/etc"],"tags":["nightly"]}]'
  ;;
esac
`)
	client := newTestClient(binary, argsFile)

	snapshots, err := client.Snapshots(context.Background(), "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("got %d snapshots", len(snapshots))
	}
	if snapshots[0].ShortID != "snap123" || snapshots[0].Hostname != "node-1" {
		t.Errorf("snapshot = %+v", snapshots[0])
	}
	if got := readArgs(t, argsFile); !strings.Contains(got, "snapshots --json --host node-1") {
		t.Errorf("args = %q", got)
	}
}

func TestSnapshotsEmpty(t *testing.T) {
	binary, argsFile := fakeRestic(t, `echo 'null'
`)
	client := newTestClient(binary, argsFile)

	snapshots, err := client.Snapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("got %d snapshots", len(snapshots))
	}
}

func TestRestoreArgs(t *testing.T) {
	binary, argsFile := fakeRestic(t, ":\n")
	client := newTestClient(binary, argsFile)

	err := client.Restore(context.Background(), RestoreOptions{
		Snapshot:  "latest",
		Target:    "/",
		Host:      "node-1",
		Includes:  []string{"/etc"},
		Overwrite: "never",
		Verify:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	args := readArgs(t, argsFile)
	for _, want := range []string{"restore latest --target /", "--host node-1", "--include /etc", "--overwrite never", "--verify"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestMountArgs(t *testing.T) {
	binary, argsFile := fakeRestic(t, "echo mounted\n")
	client := newTestClient(binary, argsFile)

	var out bytes.Buffer
	if err := client.Mount(context.Background(), "/mnt/jv", &out, &out); err != nil {
		t.Fatal(err)
	}
	if args := readArgs(t, argsFile); !strings.Contains(args, "mount /mnt/jv") {
		t.Errorf("args = %q", args)
	}
}

func TestMissingBinary(t *testing.T) {
	client := New(Options{Binary: filepath.Join(t.TempDir(), "missing-restic")})
	_, err := client.Snapshots(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected a clear missing binary error, got %v", err)
	}
}

func TestCheckAndForget(t *testing.T) {
	binary, argsFile := fakeRestic(t, ":\n")
	client := newTestClient(binary, argsFile)

	if err := client.Check(context.Background(), "5%"); err != nil {
		t.Fatal(err)
	}
	if err := client.Forget(context.Background(), []string{"--keep-last", "3"}, true); err != nil {
		t.Fatal(err)
	}

	args := readArgs(t, argsFile)
	if !strings.Contains(args, "check --read-data-subset 5%") {
		t.Errorf("check args = %q", args)
	}
	if !strings.Contains(args, "check --read-data-subset 5% --retry-lock 30m") {
		t.Errorf("check must wait for a shared repository: %q", args)
	}
	if !strings.Contains(args, "forget --keep-last 3 --prune") {
		t.Errorf("forget args = %q", args)
	}
	if !strings.Contains(args, "forget --keep-last 3 --prune --retry-lock 30m") {
		t.Errorf("forget must wait for a shared repository: %q", args)
	}
}

func TestExtraOptions(t *testing.T) {
	binary, argsFile := fakeRestic(t, "echo 'null'\n")
	client := New(Options{
		Binary:     binary,
		Repository: "sftp:backup@host:/srv/repo",
		Env:        map[string]string{"ARGS_FILE": argsFile},
		Extra:      []string{"-o", "sftp.args=-F /etc/jealous-vault/ssh_config"},
	})

	if _, err := client.Snapshots(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	args := readArgs(t, argsFile)
	want := "-o sftp.args=-F /etc/jealous-vault/ssh_config snapshots --json"
	if args != want {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestStats(t *testing.T) {
	binary, argsFile := fakeRestic(t, `if [ "$1" = "stats" ]; then
  echo '{"total_size":12345,"total_file_count":10,"total_blob_count":5,"snapshots_count":2}'
fi
`)
	client := newTestClient(binary, argsFile)

	stats, err := client.Stats(context.Background(), "raw-data")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalSize != 12345 || stats.TotalFileCount != 10 || stats.SnapshotsCount != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if args := readArgs(t, argsFile); !strings.Contains(args, "stats --json --mode raw-data") {
		t.Errorf("args = %q", args)
	}
}

func TestStatsForSnapshot(t *testing.T) {
	binary, argsFile := fakeRestic(t, `if [ "$1" = "stats" ]; then
  echo '{"total_size":42,"total_file_count":1,"total_blob_count":1,"snapshots_count":1}'
fi
`)
	client := newTestClient(binary, argsFile)

	stats, err := client.StatsFor(context.Background(), "abc123", "restore-size")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalSize != 42 {
		t.Fatalf("stats = %+v", stats)
	}
	if args := readArgs(t, argsFile); !strings.Contains(args, "stats --json --mode restore-size abc123") {
		t.Errorf("args = %q", args)
	}
}

func TestDiffAndForget(t *testing.T) {
	binary, argsFile := fakeRestic(t, ":\n")
	client := newTestClient(binary, argsFile)

	if err := client.Diff(context.Background(), "a", "b", true); err != nil {
		t.Fatal(err)
	}
	if err := client.ForgetSnapshot(context.Background(), "abc", true); err != nil {
		t.Fatal(err)
	}
	args := readArgs(t, argsFile)
	if !strings.Contains(args, "diff a b --json") {
		t.Errorf("diff args = %q", args)
	}
	if !strings.Contains(args, "forget abc --prune") {
		t.Errorf("forget args = %q", args)
	}
}

func TestCopyTo(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "restic")
	argsFile := filepath.Join(dir, "args.log")
	envFile := filepath.Join(dir, "env.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argsFile + "\n" +
		"printf '%s|%s|%s\\n' \"$RESTIC_REPOSITORY\" \"$RESTIC_PASSWORD_FILE\" \"$RESTIC_TEST_SOURCE\" >> " + envFile + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	client := New(Options{
		Binary:       binary,
		Repository:   "local:/source",
		PasswordFile: "/source.pass",
		Env:          map[string]string{"RESTIC_TEST_SOURCE": "primary-secret"},
	})
	err := client.CopyTo(context.Background(), CopyDest{
		Repository:   "local:/dest",
		PasswordFile: "/dest.pass",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}

	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"copy --from-repo local:/source", "--from-password-file /source.pass"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
	env, _ := os.ReadFile(envFile)
	if strings.TrimSpace(string(env)) != "local:/dest|/dest.pass|" {
		t.Fatalf("destination env = %q", env)
	}
}

func TestKeyCommands(t *testing.T) {
	binary, argsFile := fakeRestic(t, ":\n")
	client := newTestClient(binary, argsFile)

	if err := client.AddKey(context.Background(), "/new.pass"); err != nil {
		t.Fatal(err)
	}
	if err := client.Keys(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveKey(context.Background(), "abcd1234"); err != nil {
		t.Fatal(err)
	}
	args := readArgs(t, argsFile)
	for _, want := range []string{"key add --new-password-file /new.pass", "key list", "key remove abcd1234"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestEnvironmentCacheDir(t *testing.T) {
	client := New(Options{
		Repository: "local:/repo",
		CacheDir:   "/var/cache/jealous-vault",
	})
	env := strings.Join(client.environment(), "\n")
	if !strings.Contains(env, "RESTIC_CACHE_DIR=/var/cache/jealous-vault") {
		t.Fatalf("cache directory is missing from the restic environment:\n%s", env)
	}

	plain := New(Options{Repository: "local:/repo"})
	for _, entry := range plain.environment() {
		if strings.HasPrefix(entry, "RESTIC_CACHE_DIR=") {
			t.Fatalf("cache directory was set without being requested: %s", entry)
		}
	}
}

func TestCopyToPassesCacheDir(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "restic")
	envFile := filepath.Join(dir, "env.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$RESTIC_CACHE_DIR\" > " + envFile + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	client := New(Options{Binary: binary, Repository: "local:/source"})
	if err := client.CopyTo(context.Background(), CopyDest{
		Repository: "local:/dest",
		CacheDir:   "/var/cache/jealous-vault",
	}, nil, true); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(envFile)
	if strings.TrimSpace(string(env)) != "/var/cache/jealous-vault" {
		t.Fatalf("copy cache dir = %q", env)
	}
}
