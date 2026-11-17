// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package state stores the result of the last run in a small JSON file.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Run kinds.
const (
	KindBackup = "backup"
	KindCheck  = "check"
)

// Run statuses.
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

// Run is one finished backup or check.
type Run struct {
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
	LastRun     *Run `json:"last_run,omitempty"`
	LastSuccess *Run `json:"last_success,omitempty"`
	LastCheck   *Run `json:"last_check,omitempty"`
}

// Store reads and writes a state file.
type Store struct {
	Path string
}

// Load returns an empty state when the file does not exist yet.
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
		return nil, fmt.Errorf("parse %s: %w", s.Path, err)
	}
	return &st, nil
}

// Save writes the state atomically with owner-only permissions.
func (s Store) Save(st *State) error {
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
	st, err := s.Load()
	if err != nil {
		return nil, err
	}
	switch run.Kind {
	case KindCheck:
		st.LastCheck = &run
	default:
		st.LastRun = &run
		if run.Status == StatusSuccess {
			st.LastSuccess = &run
		}
	}
	if err := s.Save(st); err != nil {
		return nil, err
	}
	return st, nil
}
