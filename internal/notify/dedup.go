// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	shortCooldown = 3 * time.Hour
	longCooldown  = 6 * time.Hour
	recordTTL     = 90 * 24 * time.Hour
)

type alertRecord struct {
	Event           string    `json:"event"`
	ErrorHash       string    `json:"error_hash"`
	LastSent        time.Time `json:"last_sent"`
	CooldownSeconds int64     `json:"cooldown_seconds"`
	Suppressed      int       `json:"suppressed"`
}

type alertDisk struct {
	Version int                    `json:"version"`
	Records map[string]alertRecord `json:"records"`
}

type limiter struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

func newLimiter(path string, now func() time.Time) *limiter {
	if now == nil {
		now = time.Now
	}
	return &limiter{path: path, now: now}
}

// Allow reports whether the alert should be sent and how many earlier alerts
// of the same kind were suppressed.
func (l *limiter) Allow(ev Event) (bool, int, error) {
	if l.path == "" {
		return true, 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	state, err := l.load()
	if err != nil {
		return true, 0, err
	}
	now := l.now()
	key := alertKey(ev)
	record, ok := state.Records[key]
	if !ok {
		return true, 0, nil
	}

	cooldown := time.Duration(record.CooldownSeconds) * time.Second
	if cooldown <= 0 {
		cooldown = cooldownFor(ev.Event)
	}
	if now.Sub(record.LastSent) < cooldown {
		record.Suppressed++
		state.Records[key] = record
		if err := l.save(state); err != nil {
			return false, record.Suppressed, err
		}
		return false, record.Suppressed, nil
	}
	return true, record.Suppressed, nil
}

// MarkSent records a delivery and resets the suppressed counter.
func (l *limiter) MarkSent(ev Event) error {
	if l.path == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	state, err := l.load()
	if err != nil {
		return err
	}
	key := alertKey(ev)
	state.Records[key] = alertRecord{
		Event:           ev.Event,
		ErrorHash:       hashError(ev.Error),
		LastSent:        l.now(),
		CooldownSeconds: int64(cooldownFor(ev.Event) / time.Second),
		Suppressed:      0,
	}
	return l.save(state)
}

func alertKey(ev Event) string {
	return ev.Host + "|" + ev.Event + "|" + hashError(ev.Error)
}

func hashError(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func cooldownFor(event string) time.Duration {
	switch event {
	case "freshness", "verify", "copy":
		return longCooldown
	default:
		return shortCooldown
	}
}

func (l *limiter) load() (alertDisk, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return alertDisk{Version: 1, Records: map[string]alertRecord{}}, nil
		}
		return alertDisk{}, err
	}
	state := alertDisk{Version: 1, Records: map[string]alertRecord{}}
	if err := json.Unmarshal(data, &state); err != nil {
		return alertDisk{}, err
	}
	if state.Records == nil {
		state.Records = map[string]alertRecord{}
	}
	return state, nil
}

func (l *limiter) save(state alertDisk) error {
	now := l.now()
	for key, record := range state.Records {
		if now.Sub(record.LastSent) > recordTTL {
			delete(state.Records, key)
		}
	}

	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".alerts-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
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
	return os.Rename(tmpName, l.path)
}
