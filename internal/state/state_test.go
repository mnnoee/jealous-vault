// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "missing.json")}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRun != nil || st.LastSuccess != nil || st.LastCheck != nil {
		t.Fatalf("expected empty state, got %+v", st)
	}
}

func TestRecordBackupAndCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := Store{Path: path}

	start := time.Now().Add(-time.Minute)
	backup := Run{
		Kind:            KindBackup,
		StartedAt:       start,
		FinishedAt:      time.Now(),
		Status:          StatusSuccess,
		SnapshotID:      "abc123",
		FilesNew:        4,
		BytesAdded:      2048,
		DurationSeconds: 60,
	}
	if _, err := store.Record(backup); err != nil {
		t.Fatal(err)
	}

	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRun == nil || st.LastRun.SnapshotID != "abc123" {
		t.Fatalf("last run not stored: %+v", st.LastRun)
	}
	if st.LastSuccess == nil {
		t.Fatal("last success not stored")
	}

	check := Run{
		Kind:       KindCheck,
		StartedAt:  time.Now(),
		FinishedAt: time.Now(),
		Status:     StatusSuccess,
	}
	if _, err := store.Record(check); err != nil {
		t.Fatal(err)
	}
	st, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastCheck == nil {
		t.Fatal("last check not stored")
	}
	if st.LastRun == nil || st.LastRun.SnapshotID != "abc123" {
		t.Fatalf("check overwrote the backup run: %+v", st.LastRun)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state mode = %o, want 600", info.Mode().Perm())
	}
}

func TestFailedBackupKeepsLastSuccess(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "state.json")}

	success := Run{Kind: KindBackup, StartedAt: time.Now(), FinishedAt: time.Now(), Status: StatusSuccess, SnapshotID: "ok"}
	if _, err := store.Record(success); err != nil {
		t.Fatal(err)
	}
	failed := Run{Kind: KindBackup, StartedAt: time.Now(), FinishedAt: time.Now(), Status: StatusFailed, Error: "boom"}
	if _, err := store.Record(failed); err != nil {
		t.Fatal(err)
	}

	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRun == nil || st.LastRun.Status != StatusFailed {
		t.Fatalf("last run = %+v", st.LastRun)
	}
	if st.LastSuccess == nil || st.LastSuccess.SnapshotID != "ok" {
		t.Fatalf("last success = %+v", st.LastSuccess)
	}
}

func TestRecordVerify(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "state.json")}
	run := Run{Kind: KindVerify, StartedAt: time.Now(), FinishedAt: time.Now(), Status: StatusSkipped, Error: "too big"}
	if _, err := store.Record(run); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastVerify == nil || st.LastVerify.Status != StatusSkipped {
		t.Fatalf("last verify = %+v", st.LastVerify)
	}
	if st.LastRun != nil {
		t.Fatalf("verify must not touch last run: %+v", st.LastRun)
	}
}

func TestHistoryAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	store := Store{Path: filepath.Join(dir, "state.json"), HistoryPath: filepath.Join(dir, "history.jsonl")}

	first := Run{Kind: KindBackup, StartedAt: time.Now(), FinishedAt: time.Now(), Status: StatusFailed, Error: "first"}
	second := Run{Kind: KindBackup, StartedAt: time.Now(), FinishedAt: time.Now(), Status: StatusSuccess, SnapshotID: "snap2"}
	if _, err := store.Record(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(second); err != nil {
		t.Fatal(err)
	}

	runs, err := store.History(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Error != "first" || runs[1].SnapshotID != "snap2" {
		t.Fatalf("history = %+v", runs)
	}

	limited, err := store.History(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].SnapshotID != "snap2" {
		t.Fatalf("limited history = %+v", limited)
	}

	info, err := os.Stat(store.HistoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("history mode = %o, want 600", info.Mode().Perm())
	}
}

func TestHistoryMissingIsEmpty(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "state.json")}
	runs, err := store.History(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("history = %+v", runs)
	}
}
