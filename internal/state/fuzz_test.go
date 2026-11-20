// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package state

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzLoadState(f *testing.F) {
	f.Add([]byte(`{"version":1,"last_run":{"kind":"backup","status":"success","duration_seconds":1}}`))
	f.Add([]byte(`{"last_run":{"kind":"backup","status":"success"}}`))
	f.Add([]byte(`{"version":99}`))
	f.Add([]byte(`{`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		store := Store{Path: path}
		_, _ = store.Load()
	})
}

func FuzzHistory(f *testing.F) {
	f.Add([]byte(`{"version":1,"kind":"backup","status":"success","duration_seconds":1}` + "\n"))
	f.Add([]byte(`{"kind":"backup","status":"success"}` + "\n"))
	f.Add([]byte(`not json` + "\n" + `{"kind":"backup"}` + "\n"))
	f.Add([]byte(`{"version":99,"kind":"backup"}` + "\n"))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "history.jsonl")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		store := Store{Path: filepath.Join(dir, "state.json"), HistoryPath: path}
		_, _ = store.History(0)
	})
}
