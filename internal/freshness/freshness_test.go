// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package freshness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mnnoee/jealous-vault/internal/state"
)

func fakeSystemctl(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "systemctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func freshState(now time.Time, age time.Duration) *state.State {
	return &state.State{LastSuccess: &state.Run{
		Kind:            state.KindBackup,
		StartedAt:       now.Add(-age),
		FinishedAt:      now.Add(-age),
		Status:          state.StatusSuccess,
		DurationSeconds: age.Seconds(),
	}}
}

func TestCheckOK(t *testing.T) {
	now := time.Now()
	systemctl := fakeSystemctl(t, "exit 0\n")

	result := Check(context.Background(), freshState(now, 2*time.Hour), Options{
		MaxAge:             25 * time.Hour,
		RespectBackupTimer: true,
		Systemctl:          systemctl,
		Now:                func() time.Time { return now },
	})
	if result.Kind != KindOK {
		t.Fatalf("kind = %s, message = %s", result.Kind, result.Message)
	}
}

func TestCheckStale(t *testing.T) {
	now := time.Now()
	systemctl := fakeSystemctl(t, "exit 0\n")

	result := Check(context.Background(), freshState(now, 30*time.Hour), Options{
		MaxAge:             25 * time.Hour,
		RespectBackupTimer: true,
		Systemctl:          systemctl,
		Now:                func() time.Time { return now },
	})
	if result.Kind != KindStale {
		t.Fatalf("kind = %s, message = %s", result.Kind, result.Message)
	}
}

func TestCheckNoSuccess(t *testing.T) {
	systemctl := fakeSystemctl(t, "exit 0\n")

	result := Check(context.Background(), &state.State{}, Options{
		MaxAge:             25 * time.Hour,
		RespectBackupTimer: true,
		Systemctl:          systemctl,
	})
	if result.Kind != KindNoSuccess {
		t.Fatalf("kind = %s", result.Kind)
	}
}

func TestCheckTimerDisabled(t *testing.T) {
	now := time.Now()
	systemctl := fakeSystemctl(t, `if [ "$1" = "is-enabled" ]; then exit 1; fi
exit 0
`)

	result := Check(context.Background(), freshState(now, time.Hour), Options{
		MaxAge:             25 * time.Hour,
		RespectBackupTimer: true,
		Systemctl:          systemctl,
		Now:                func() time.Time { return now },
	})
	if result.Kind != KindTimerDisabled {
		t.Fatalf("kind = %s", result.Kind)
	}
}

func TestCheckTimerInactive(t *testing.T) {
	now := time.Now()
	systemctl := fakeSystemctl(t, `if [ "$1" = "is-active" ]; then exit 1; fi
exit 0
`)

	result := Check(context.Background(), freshState(now, time.Hour), Options{
		MaxAge:             25 * time.Hour,
		RespectBackupTimer: true,
		Systemctl:          systemctl,
		Now:                func() time.Time { return now },
	})
	if result.Kind != KindTimerInactive {
		t.Fatalf("kind = %s", result.Kind)
	}
}

func TestCheckIgnoresTimerWhenNotRespected(t *testing.T) {
	now := time.Now()
	systemctl := fakeSystemctl(t, "exit 1\n")

	result := Check(context.Background(), freshState(now, time.Hour), Options{
		MaxAge:             25 * time.Hour,
		RespectBackupTimer: false,
		Systemctl:          systemctl,
		Now:                func() time.Time { return now },
	})
	if result.Kind != KindOK {
		t.Fatalf("kind = %s", result.Kind)
	}
}

func TestCheckWithoutSystemd(t *testing.T) {
	now := time.Now()

	result := Check(context.Background(), freshState(now, time.Hour), Options{
		MaxAge:             25 * time.Hour,
		RespectBackupTimer: true,
		Systemctl:          filepath.Join(t.TempDir(), "missing"),
		Now:                func() time.Time { return now },
	})
	if result.Kind != KindOK {
		t.Fatalf("kind = %s", result.Kind)
	}
}
