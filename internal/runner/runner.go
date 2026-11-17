// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package runner executes a backup or a check from start to finish: hooks,
// restic, retention, state and notifications.
package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/hooks"
	"github.com/mnnoee/jealous-vault/internal/notify"
	"github.com/mnnoee/jealous-vault/internal/restic"
	"github.com/mnnoee/jealous-vault/internal/state"
)

// Runner owns the whole backup workflow for one configuration.
type Runner struct {
	cfg    *config.Config
	restic *restic.Client
	notify *notify.Notifier
	store  state.Store
	log    *slog.Logger
	now    func() time.Time
}

// New builds a Runner and its restic and notification clients.
func New(cfg *config.Config, log *slog.Logger, out io.Writer) *Runner {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runner{
		cfg: cfg,
		restic: restic.New(restic.Options{
			Binary:       cfg.ResticBinary,
			Repository:   cfg.Repository.URL,
			PasswordFile: cfg.Repository.PasswordFile,
			Env:          cfg.Repository.Env,
			Extra:        cfg.ResticExtraOptions(),
			Logger:       log,
			Output:       out,
		}),
		notify: notify.New(notify.Options{
			Webhooks:        cfg.Notify.Webhooks,
			PingURL:         cfg.Notify.PingURL,
			TelegramToken:   cfg.Notify.TelegramToken,
			TelegramChatID:  cfg.Notify.TelegramChatID,
			NotifyOnSuccess: cfg.Notify.NotifyOnSuccess,
			Timeout:         cfg.NotifyTimeout,
			Logger:          log,
		}),
		store: state.Store{Path: cfg.StateFile},
		log:   log,
		now:   time.Now,
	}
}

// Backup runs hooks, restic backup, retention, records the result and sends
// notifications. The returned error is non-nil when the backup itself failed.
func (r *Runner) Backup(ctx context.Context) (state.Run, error) {
	run := state.Run{
		Kind:      state.KindBackup,
		StartedAt: r.now(),
		Status:    state.StatusFailed,
	}
	var warnings []string

	preErr := hooks.Run(ctx, r.log, "backup-pre", r.cfg.Backup.PreHooks, r.hookEnv(&run, "pre", "running"))
	if preErr != nil {
		warnings = append(warnings, preErr.Error())
	}

	var backupErr error
	if preErr == nil {
		r.log.Info("backup started", "paths", r.cfg.Backup.Paths, "repository", r.cfg.RedactedRepository())

		backupCtx, cancel := context.WithTimeout(ctx, r.cfg.BackupTimeout)
		opts := restic.BackupOptions{
			Host:          r.cfg.Host,
			Paths:         r.cfg.Backup.Paths,
			Excludes:      r.cfg.Backup.Excludes,
			Tags:          r.cfg.Backup.Tags,
			OneFileSystem: r.cfg.Backup.OneFileSystem,
		}
		summary, err := r.restic.Backup(backupCtx, opts)
		if err != nil && restic.IsLockError(err) {
			r.log.Warn("repository is locked, unlocking stale locks and retrying once")
			if unlockErr := r.restic.Unlock(backupCtx); unlockErr != nil {
				r.log.Error("cannot unlock repository", "error", unlockErr)
			} else {
				summary, err = r.restic.Backup(backupCtx, opts)
			}
		}
		cancel()

		if err != nil {
			backupErr = err
		} else {
			run.Status = state.StatusSuccess
			run.SnapshotID = summary.SnapshotID
			run.FilesNew = summary.FilesNew
			run.FilesChanged = summary.FilesChanged
			run.BytesAdded = summary.DataAdded
			r.log.Info("backup finished",
				"snapshot", summary.SnapshotID,
				"files_new", summary.FilesNew,
				"files_changed", summary.FilesChanged,
				"bytes_added", summary.DataAdded,
			)
			if err := r.retention(ctx); err != nil {
				warnings = append(warnings, err.Error())
			}
		}
	}

	status := "failed"
	if run.Status == state.StatusSuccess {
		status = "success"
	}
	if err := hooks.Run(ctx, r.log, "backup-post", r.cfg.Backup.PostHooks, r.hookEnv(&run, "post", status)); err != nil {
		warnings = append(warnings, err.Error())
	}

	if preErr != nil {
		backupErr = fmt.Errorf("pre hook: %w", preErr)
	}
	return r.finish(ctx, run, warnings, backupErr)
}

// Check verifies repository integrity and records the result.
func (r *Runner) Check(ctx context.Context) (state.Run, error) {
	run := state.Run{
		Kind:      state.KindCheck,
		StartedAt: r.now(),
		Status:    state.StatusFailed,
	}

	r.log.Info("check started", "repository", r.cfg.RedactedRepository())
	checkCtx, cancel := context.WithTimeout(ctx, r.cfg.CheckTimeout)
	err := r.restic.Check(checkCtx, r.cfg.Check.ReadDataSubset)
	cancel()
	if err == nil {
		run.Status = state.StatusSuccess
		r.log.Info("check passed")
	}

	return r.finish(ctx, run, nil, err)
}

func (r *Runner) retention(ctx context.Context) error {
	if !r.cfg.HasRetention() {
		return nil
	}
	retentionCtx, cancel := context.WithTimeout(ctx, r.cfg.BackupTimeout)
	defer cancel()

	r.log.Info("retention started", "prune", r.cfg.Retention.Prune)
	if err := r.restic.Forget(retentionCtx, r.cfg.RetentionArgs(), r.cfg.Retention.Prune); err != nil {
		return fmt.Errorf("retention: %w", err)
	}
	return nil
}

func (r *Runner) finish(ctx context.Context, run state.Run, warnings []string, runErr error) (state.Run, error) {
	run.FinishedAt = r.now()
	run.DurationSeconds = run.FinishedAt.Sub(run.StartedAt).Seconds()
	run.Warnings = warnings
	if runErr != nil {
		run.Status = state.StatusFailed
		run.Error = runErr.Error()
	}

	if _, err := r.store.Record(run); err != nil {
		r.log.Error("cannot write state", "error", err)
	}

	ev := notify.Event{
		Event:           run.Kind,
		Status:          run.Status,
		Host:            r.cfg.Host,
		Snapshot:        run.SnapshotID,
		FilesNew:        run.FilesNew,
		FilesChanged:    run.FilesChanged,
		BytesAdded:      run.BytesAdded,
		DurationSeconds: run.DurationSeconds,
		Warnings:        warnings,
		Error:           run.Error,
		Time:            run.FinishedAt,
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.notify.Send(notifyCtx, ev); err != nil {
		r.log.Error("cannot send notification", "error", err)
	}

	return run, runErr
}

func (r *Runner) hookEnv(run *state.Run, phase, status string) map[string]string {
	env := map[string]string{
		"JM_EVENT":  run.Kind,
		"JM_PHASE":  phase,
		"JM_STATUS": status,
		"JM_HOST":   r.cfg.Host,
	}
	if run.SnapshotID != "" {
		env["JM_SNAPSHOT"] = run.SnapshotID
	}
	return env
}
