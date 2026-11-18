// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package audit writes the append-only journal of critical actions, separate
// from the run history. The journal answers who restored or deleted what, and
// must never contain file contents or credentials.
package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
)

// Entry is one line of the audit journal.
type Entry struct {
	Time     time.Time `json:"ts"`
	Action   string    `json:"action"`
	User     string    `json:"user"`
	UID      int       `json:"uid"`
	Host     string    `json:"host"`
	Snapshot string    `json:"snapshot,omitempty"`
	Target   string    `json:"target,omitempty"`
	Files    int64     `json:"files,omitempty"`
	Bytes    int64     `json:"bytes,omitempty"`
	Result   string    `json:"result"`
	Error    string    `json:"error,omitempty"`
}

// Logger appends entries to Path. An empty Path disables the journal.
type Logger struct {
	Path string
	Log  *slog.Logger
	Now  func() time.Time
}

// Write appends one entry. It fills the timestamp and the effective user when
// the caller did not set them. The journal is created with mode 0600.
func (l Logger) Write(e Entry) error {
	if l.Path == "" {
		return nil
	}
	if e.Time.IsZero() {
		now := time.Now()
		if l.Now != nil {
			now = l.Now()
		}
		e.Time = now
	}
	if e.User == "" {
		e.User = currentUser()
	}
	if e.UID == 0 {
		e.UID = os.Getuid()
	}
	if e.Result == "" {
		e.Result = "success"
	}

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if l.Log != nil {
		l.Log.Debug("audit entry written", "action", e.Action, "result", e.Result)
	}
	return nil
}

// Record is Write with the error reported to the logger. A broken journal must
// not turn a successful restore into a failure, but it must be visible.
func (l Logger) Record(e Entry) {
	if err := l.Write(e); err != nil {
		if l.Log != nil {
			l.Log.Error("cannot write the audit journal", "path", l.Path, "error", err)
			return
		}
		fmt.Fprintf(os.Stderr, "jealous-vault: cannot write the audit journal: %v\n", err)
	}
}

// currentUser avoids os/user in exotic environments where it fails.
func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	if name := os.Getenv("LOGNAME"); name != "" {
		return name
	}
	return "uid:" + strconv.Itoa(os.Getuid())
}
