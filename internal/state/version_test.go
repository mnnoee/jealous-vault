// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOldStateWithoutVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	body := `{"last_run":{"kind":"backup","status":"success","duration_seconds":1}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	store := Store{Path: path}
	st, err := store.Load()
	if err != nil {
		t.Fatalf("old state must stay readable: %v", err)
	}
	if st.Version != 0 {
		t.Errorf("old state version = %d, want 0", st.Version)
	}
	if st.LastRun == nil || st.LastRun.Kind != KindBackup {
		t.Fatalf("old run not loaded: %+v", st.LastRun)
	}

	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	st, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != CurrentVersion {
		t.Errorf("state was not upgraded: version = %d, want %d", st.Version, CurrentVersion)
	}
}

func TestNewerStateVersionRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	body := `{"version":99,"last_run":{"kind":"backup","status":"success"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	store := Store{Path: path}
	if _, err := store.Load(); err == nil {
		t.Fatal("a newer state format must be rejected")
	}
}

func TestHistoryNewerVersionRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	body := `{"version":99,"kind":"backup","status":"success"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	store := Store{Path: filepath.Join(dir, "state.json"), HistoryPath: path}
	if _, err := store.History(0); err == nil {
		t.Fatal("a newer history format must be rejected")
	}
}
