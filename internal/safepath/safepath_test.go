// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(link); err == nil {
		t.Fatal("expected a symlink error")
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("victim changed: %q", data)
	}
}

func TestCheckParentRejectsWritableDirectory(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "public")
	if err := os.Mkdir(public, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(public, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := CheckParent(filepath.Join(public, "file")); err == nil {
		t.Fatal("expected a writable-parent error")
	}
}

func TestReadFileAcceptsPrivateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ok" {
		t.Fatalf("data = %q", data)
	}
}
