// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package recovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSecretRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "kit")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openSecret(link, true); err == nil {
		t.Fatal("expected a symlink error in force mode")
	}
	if _, err := openSecret(link, false); err == nil {
		t.Fatal("expected an O_EXCL error in non-force mode")
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("symlink target was modified: %q", data)
	}
}
