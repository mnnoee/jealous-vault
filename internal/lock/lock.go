// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package lock prevents concurrent backup, check and restore runs.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrBusy is returned when another run holds the lock.
var ErrBusy = errors.New("another run is active")

// Info describes the current holder. It is written for diagnostics only, the
// actual exclusion is done by flock.
type Info struct {
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
}

// BusyError carries the holder info.
type BusyError struct {
	Info Info
}

func (e *BusyError) Error() string {
	if e.Info.PID == 0 && e.Info.Command == "" {
		return ErrBusy.Error()
	}
	return fmt.Sprintf("%s: pid %d, %s since %s", ErrBusy, e.Info.PID, e.Info.Command, e.Info.StartedAt.Format(time.RFC3339))
}

func (e *BusyError) Unwrap() error { return ErrBusy }

// Lock is an acquired file lock.
type Lock struct {
	file *os.File
}

// Acquire takes an exclusive non-blocking lock on path.
func Acquire(path string, info Info) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &BusyError{Info: readInfo(path)}
		}
		return nil, err
	}

	if err := f.Truncate(0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	if err := json.NewEncoder(f).Encode(info); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Release unlocks and closes the file. It is safe to call more than once.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	// Clear the diagnostic info on a clean release. A crashed process leaves
	// it behind, which is useful when inspecting a stale lock.
	_ = l.file.Truncate(0)
	_ = l.file.Sync()
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}

// Inspect reports whether the lock is currently held and by whom.
func Inspect(path string) (Info, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return Info{}, false
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return readInfo(path), true
		}
		return Info{}, false
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return Info{}, false
}

func readInfo(path string) Info {
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}
	}
	return info
}
