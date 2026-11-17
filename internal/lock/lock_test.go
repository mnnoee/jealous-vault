// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package lock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireBusyAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.lock")
	info := Info{PID: os.Getpid(), Command: "backup", StartedAt: time.Now()}

	first, err := Acquire(path, info)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Acquire(path, Info{PID: 999, Command: "check"}); err == nil {
		t.Fatal("second lock must fail")
	} else {
		var busy *BusyError
		if !errors.As(err, &busy) {
			t.Fatalf("error is not BusyError: %v", err)
		}
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("error does not wrap ErrBusy: %v", err)
		}
		if busy.Info.PID != info.PID || busy.Info.Command != "backup" {
			t.Fatalf("busy info = %+v", busy.Info)
		}
	}

	heldInfo, held := Inspect(path)
	if !held {
		t.Fatal("Inspect did not report the lock as held")
	}
	if heldInfo.PID != info.PID {
		t.Fatalf("Inspect info = %+v", heldInfo)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if _, held := Inspect(path); held {
		t.Fatal("Inspect reports a released lock as held")
	}

	second, err := Acquire(path, info)
	if err != nil {
		t.Fatalf("lock cannot be reacquired: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "backup.lock")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, Info{PID: os.Getpid()}); err == nil {
		t.Fatal("a symlinked lock file must be refused")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("the symlink target was modified: %q", data)
	}
}

func TestReleaseTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.lock")
	l, err := Acquire(path, Info{PID: os.Getpid(), Command: "backup", StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}
