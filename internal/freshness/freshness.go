// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package freshness decides whether the last successful backup is still within
// its allowed age and whether the backup timer is healthy.
package freshness

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/state"
)

// Kind is the outcome of a freshness check.
type Kind string

// Freshness outcomes.
const (
	KindOK            Kind = "ok"
	KindTimerDisabled Kind = "timer.disabled"
	KindTimerInactive Kind = "timer.inactive"
	KindNoSuccess     Kind = "backup.none"
	KindStale         Kind = "backup.stale"
)

// Result describes the check outcome.
type Result struct {
	Kind        Kind
	Message     string
	Age         time.Duration
	MaxAge      time.Duration
	LastSuccess *state.Run
}

// Options configures the check.
type Options struct {
	MaxAge             time.Duration
	RespectBackupTimer bool
	Systemctl          string
	Unit               string
	Now                func() time.Time
}

// Check evaluates the state and the backup timer.
func Check(ctx context.Context, st *state.State, o Options) Result {
	if o.Systemctl == "" {
		o.Systemctl = "systemctl"
	}
	if o.Unit == "" {
		o.Unit = "jealous-vault-backup.timer"
	}
	if o.Now == nil {
		o.Now = time.Now
	}

	result := Result{MaxAge: o.MaxAge, LastSuccess: st.LastSuccess}

	if o.RespectBackupTimer {
		if _, err := exec.LookPath(o.Systemctl); err == nil {
			if err := run(ctx, o.Systemctl, "is-enabled", o.Unit); err != nil {
				result.Kind = KindTimerDisabled
				result.Message = fmt.Sprintf("%s is disabled or masked", o.Unit)
				return result
			}
			if err := run(ctx, o.Systemctl, "is-active", o.Unit); err != nil {
				result.Kind = KindTimerInactive
				result.Message = fmt.Sprintf("%s is enabled but not active", o.Unit)
				return result
			}
		}
	}

	if st.LastSuccess == nil {
		result.Kind = KindNoSuccess
		result.Message = "no successful backup recorded"
		return result
	}

	age := o.Now().Sub(st.LastSuccess.FinishedAt)
	result.Age = age
	if o.MaxAge > 0 && age > o.MaxAge {
		result.Kind = KindStale
		result.Message = fmt.Sprintf("last successful backup was %s ago, limit %s",
			age.Round(time.Minute), o.MaxAge)
		return result
	}

	result.Kind = KindOK
	result.Message = fmt.Sprintf("last successful backup was %s ago", age.Round(time.Minute))
	return result
}

func run(ctx context.Context, systemctl string, args ...string) error {
	cmd := exec.CommandContext(ctx, systemctl, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text != "" {
			return fmt.Errorf("%s %s: %w: %s", systemctl, strings.Join(args, " "), err, text)
		}
		return fmt.Errorf("%s %s: %w", systemctl, strings.Join(args, " "), err)
	}
	return nil
}
