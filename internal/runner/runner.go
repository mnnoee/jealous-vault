// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package runner executes a backup or a check from start to finish: hooks,
// restic, retention, state and notifications.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/mnnoee/jealous-vault/internal/audit"
	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/dump"
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
	dumps  *dump.Manager
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
			Binary:        cfg.ResticBinary,
			Repository:    cfg.Repository.URL,
			PasswordFile:  cfg.Repository.PasswordFile,
			Env:           cfg.Repository.Env,
			CacheDir:      cfg.ResticCacheDir(),
			Extra:         cfg.ResticExtraOptions(),
			Logger:        log,
			Output:        out,
			ShowSensitive: cfg.Logging.ShowSensitive,
		}),
		notify: notify.New(notify.Options{
			Webhooks:         cfg.Notify.Webhooks,
			PingURL:          cfg.Notify.PingURL,
			FreshnessPingURL: cfg.Notify.FreshnessPingURL,
			DedupFile:        cfg.AlertsFile,
			DedupBackup:      cfg.DedupBackup,
			DedupFreshness:   cfg.DedupFreshness,
			Email:            notify.EmailOptionsFromConfig(cfg),
			TelegramToken:    cfg.Notify.TelegramToken,
			TelegramChatID:   cfg.Notify.TelegramChatID,
			TelegramChatIDs:  cfg.Notify.TelegramChatIDs,
			NotifyOnSuccess:  cfg.Notify.NotifyOnSuccess,
			Timeout:          cfg.NotifyTimeout,
			Logger:           log,
		}),
		store: state.Store{Path: cfg.StateFile, HistoryPath: cfg.HistoryFile},
		dumps: dump.NewWithSensitive(cfg.Backup.Dumps, log, cfg.Logging.ShowSensitive),
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

	preErr := hooks.RunWith(ctx, r.log, "backup-pre", r.cfg.Backup.PreHooks, r.hookEnv(&run, "pre", "running"), r.cfg.Logging.ShowSensitive)
	if preErr != nil {
		warnings = append(warnings, preErr.Error())
	}

	var backupErr error
	if preErr == nil {
		r.log.Info("backup started", "paths", r.cfg.Backup.Paths, "repository", r.cfg.RedactedRepository())

		paths := append([]string(nil), r.cfg.Backup.Paths...)
		dumpDir, dumpErr := r.dumps.Run(ctx)
		if dumpErr != nil {
			backupErr = fmt.Errorf("database dump: %w", dumpErr)
		} else {
			if dumpDir != "" {
				paths = append(paths, dumpDir)
				r.log.Info("database dumps ready", "dir", dumpDir)
			}
			backupCtx, cancel := context.WithTimeout(ctx, r.cfg.BackupTimeout)
			opts := r.backupOptions(false)
			opts.Paths = paths
			summary, err := r.backupWithRetry(backupCtx, opts, &warnings)
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
				if err := r.dumps.Prune(); err != nil {
					warnings = append(warnings, "prune database dumps: "+err.Error())
				}
			}
		}
	}

	status := "failed"
	if run.Status == state.StatusSuccess {
		status = "success"
	}
	if err := hooks.RunWith(ctx, r.log, "backup-post", r.cfg.Backup.PostHooks, r.hookEnv(&run, "post", status), r.cfg.Logging.ShowSensitive); err != nil {
		warnings = append(warnings, err.Error())
	}

	if preErr != nil {
		backupErr = fmt.Errorf("pre hook: %w", preErr)
	}
	return r.finish(ctx, run, warnings, backupErr)
}

// backupOptions returns the restic backup flags for this configuration.
func (r *Runner) backupOptions(dryRun bool) restic.BackupOptions {
	return restic.BackupOptions{
		Host:           r.cfg.Host,
		Paths:          r.cfg.Backup.Paths,
		Excludes:       r.cfg.Backup.Excludes,
		Tags:           r.cfg.Backup.Tags,
		OneFileSystem:  r.cfg.Backup.OneFileSystem,
		DryRun:         dryRun,
		LimitUpload:    r.cfg.Backup.LimitUpload,
		Compression:    r.cfg.Backup.Compression,
		PackSize:       r.cfg.Backup.PackSize,
		AttemptTimeout: r.cfg.BackupAttemptTimeout,
	}
}

// backupWithRetry runs restic backup up to the configured number of attempts.
// A lock error also runs restic unlock before the next attempt. Failures are
// recorded as warnings so the final alert still explains what happened.
func (r *Runner) backupWithRetry(ctx context.Context, opts restic.BackupOptions, warnings *[]string) (restic.Summary, error) {
	attempts := r.cfg.Backup.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var summary restic.Summary
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		summary, err = r.restic.Backup(ctx, opts)
		if err == nil {
			return summary, nil
		}
		if ctx.Err() != nil {
			return summary, err
		}
		if opts.AttemptTimeout > 0 && errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("backup attempt timed out after %s: %w", opts.AttemptTimeout, err)
		}
		if restic.IsLockError(err) {
			r.log.Warn("repository is locked, unlocking stale locks before retrying")
			if unlockErr := r.restic.Unlock(ctx); unlockErr != nil {
				r.log.Error("cannot unlock repository", "error", unlockErr)
			}
		}
		if attempt == attempts {
			break
		}
		if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("backup attempt %d failed: %v", attempt, err))
		}
		delay := r.cfg.RetryDelay
		for i := 1; i < attempt && delay > 0; i++ {
			delay *= 2
		}
		r.log.Warn("backup attempt failed, retrying", "attempt", attempt, "delay", delay, "error", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return summary, err
		case <-timer.C:
		}
	}
	return summary, err
}

// DryRun asks restic what a backup would change without writing to the
// repository, running hooks, retention, notifications or state.
func (r *Runner) DryRun(ctx context.Context) (restic.Summary, error) {
	dryCtx, cancel := context.WithTimeout(ctx, r.cfg.BackupTimeout)
	defer cancel()
	return r.restic.Backup(dryCtx, r.backupOptions(true))
}

// Check verifies repository integrity and records the result.
func (r *Runner) Check(ctx context.Context) (state.Run, error) {
	run := state.Run{
		Kind:      state.KindCheck,
		StartedAt: r.now(),
		Status:    state.StatusFailed,
	}

	r.log.Info("check started", "repository", r.cfg.RedactedRepository())
	r.clearStaleLocks(ctx, "check")
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
	r.clearStaleLocks(retentionCtx, "retention")
	if err := r.restic.Forget(retentionCtx, r.cfg.RetentionArgs(), r.cfg.Retention.Prune); err != nil {
		return fmt.Errorf("retention: %w", err)
	}
	return nil
}

// clearStaleLocks removes locks left by crashed runs before an operation that
// needs an exclusive lock. Restic's --retry-lock waits for live locks but does
// not clean orphans, so without this a stale lock turns into a 30 minute wait.
func (r *Runner) clearStaleLocks(ctx context.Context, operation string) {
	unlockCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := r.restic.Unlock(unlockCtx); err != nil {
		r.log.Warn("cannot remove stale locks", "operation", operation, "error", err)
	}
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
	audit.Logger{Path: r.cfg.AuditFile, Log: r.log}.Record(audit.Entry{
		Time:     run.FinishedAt,
		Action:   run.Kind,
		Host:     r.cfg.Host,
		Snapshot: run.SnapshotID,
		Files:    int64(run.FilesNew + run.FilesChanged),
		Bytes:    run.BytesAdded,
		Result:   run.Status,
		Error:    run.Error,
	})

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
