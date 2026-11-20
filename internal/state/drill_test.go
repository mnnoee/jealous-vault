// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecordDrill(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "state.json")}
	run := Run{
		Kind:       KindDrill,
		Level:      "full",
		StartedAt:  time.Now().Add(-time.Second),
		FinishedAt: time.Now(),
		Status:     StatusSuccess,
		SnapshotID: "snap",
		Details:    []string{"restore: success", "db: success"},
	}
	if _, err := store.Record(run); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastDrill == nil {
		t.Fatal("last drill is missing")
	}
	if st.LastDrill.Level != "full" || st.LastDrill.SnapshotID != "snap" {
		t.Fatalf("last drill = %+v", st.LastDrill)
	}
	if len(st.LastDrill.Details) != 2 {
		t.Fatalf("details = %v", st.LastDrill.Details)
	}
	if st.LastRun != nil {
		t.Fatalf("drill must not overwrite last run: %+v", st.LastRun)
	}
}
