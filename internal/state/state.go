// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package state stores the result of the last run in a small JSON file.
package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CurrentVersion is the state and history format written by this build.
// Version 0 means the file predates the version field (0.1.0 and older);
// it stays readable and is rewritten as CurrentVersion on the next save.
const CurrentVersion = 1

// Run kinds.
const (
	KindBackup = "backup"
	KindCheck  = "check"
	KindVerify = "verify"
)

// Run statuses.
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// Run is one finished backup or check.
type Run struct {
	Version         int       `json:"version"`
	Kind            string    `json:"kind"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	Status          string    `json:"status"`
	SnapshotID      string    `json:"snapshot_id,omitempty"`
	FilesNew        int       `json:"files_new,omitempty"`
	FilesChanged    int       `json:"files_changed,omitempty"`
	BytesAdded      int64     `json:"bytes_added,omitempty"`
	DurationSeconds float64   `json:"duration_seconds"`
	Warnings        []string  `json:"warnings,omitempty"`
	Error           string    `json:"error,omitempty"`
}

// State is the whole persisted document.
type State struct {
	Version     int  `json:"version"`
	LastRun     *Run `json:"last_run,omitempty"`
	LastSuccess *Run `json:"last_success,omitempty"`
	LastCheck   *Run `json:"last_check,omitempty"`
	LastVerify  *Run `json:"last_verify,omitempty"`
}

// Store reads and writes the current state and the append-only run history.
type Store struct {
	Path        string
	HistoryPath string
}

const (
	maxHistoryBytes = 4 << 20
	keepHistoryRuns = 2000
)

// Load returns an empty state when the file does not exist yet. A state file
// torn by a crash is moved aside and treated as empty: a backup agent must
// keep running even when its tiny bookkeeping file is damaged.
func (s Store) Load() (*State, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &State{}, nil
		}
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		if _, qerr := quarantine(s.Path); qerr != nil {
			return nil, fmt.Errorf("parse %s: %w", s.Path, err)
		}
		return &State{}, nil
	}
	if st.Version > CurrentVersion {
		return nil, fmt.Errorf("state %s: format version %d is newer than supported %d", s.Path, st.Version, CurrentVersion)
	}
	return &st, nil
}

// quarantine moves an unreadable state file out of the way so the next save
// starts clean while the damaged bytes stay around for inspection.
func quarantine(path string) (string, error) {
	dst := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405")
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			break
		}
		dst = fmt.Sprintf("%s.corrupt-%s-%d", path, time.Now().UTC().Format("20060102T150405"), i)
	}
	if err := os.Rename(path, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Save writes the state atomically with owner-only permissions.
func (s Store) Save(st *State) error {
	if st.Version == 0 {
		st.Version = CurrentVersion
	}
	if st.Version > CurrentVersion {
		return fmt.Errorf("state format version %d is newer than supported %d", st.Version, CurrentVersion)
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, s.Path)
}

// Record stores a run and updates the success pointer when appropriate.
func (s Store) Record(run Run) (*State, error) {
	run.Version = CurrentVersion
	st, err := s.Load()
	if err != nil {
		return nil, err
	}
	switch run.Kind {
	case KindCheck:
		st.LastCheck = &run
	case KindVerify:
		st.LastVerify = &run
	default:
		st.LastRun = &run
		if run.Status == StatusSuccess {
			st.LastSuccess = &run
		}
	}
	if err := s.Save(st); err != nil {
		return nil, err
	}
	if err := s.Append(run); err != nil {
		return st, err
	}
	return st, nil
}

func (s Store) historyPath() string {
	if s.HistoryPath != "" {
		return s.HistoryPath
	}
	return filepath.Join(filepath.Dir(s.Path), "history.jsonl")
}

// Append adds one run to the JSONL history and rotates the file when it grows
// past maxHistoryBytes. The caller normally holds the run lock already.
func (s Store) Append(run Run) error {
	if run.Version == 0 {
		run.Version = CurrentVersion
	}
	path := s.historyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := repairTail(path); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= maxHistoryBytes {
		if err := s.rotateHistory(path); err != nil {
			return err
		}
	}
	line, err := json.Marshal(run)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
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
	return f.Sync()
}

// History returns the newest limit runs in chronological order. A limit of
// zero returns everything. A record torn by a crash at the very end of the
// file is ignored; a malformed record in the middle is still an error.
func (s Store) History(limit int) ([]Run, error) {
	path := s.historyPath()
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var runs []Run
	var torn []byte
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var run Run
		if err := json.Unmarshal(line, &run); err != nil {
			if torn != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			torn = append([]byte(nil), line...)
			continue
		}
		if run.Version > CurrentVersion {
			return nil, fmt.Errorf("history %s: record format version %d is newer than supported %d", path, run.Version, CurrentVersion)
		}
		if torn != nil {
			return nil, fmt.Errorf("parse %s: malformed history record in the middle of the file", path)
		}
		runs = append(runs, run)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(runs) > limit {
		runs = runs[len(runs)-limit:]
	}
	return runs, nil
}

// repairTail makes the history safe for the next append. A record without a
// trailing newline is either a valid record written just before a crash or a
// torn one; the former gets a newline, the latter is cut off.
func repairTail(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return nil
	}
	line := bytes.TrimSpace(data[bytes.LastIndexByte(data, '\n')+1:])
	var run Run
	if json.Unmarshal(line, &run) == nil {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write([]byte{'\n'}); err != nil {
			return err
		}
		return f.Sync()
	}
	return os.Truncate(path, int64(bytes.LastIndexByte(data, '\n')+1))
}

func (s Store) rotateHistory(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := bytes.Split(data, []byte{'\n'})
	start := 0
	if len(lines) > keepHistoryRuns {
		start = len(lines) - keepHistoryRuns
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	for _, line := range lines[start:] {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
