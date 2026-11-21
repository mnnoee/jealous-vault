// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/audit"
	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/executil"
	"github.com/mnnoee/jealous-vault/internal/notify"
	"github.com/mnnoee/jealous-vault/internal/restic"
	"github.com/mnnoee/jealous-vault/internal/restore"
	"github.com/mnnoee/jealous-vault/internal/state"
)

type drillStep struct {
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	Detail          string  `json:"detail,omitempty"`
	Output          string  `json:"output,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type drillReport struct {
	Level           string      `json:"level"`
	Snapshot        string      `json:"snapshot"`
	Target          string      `json:"target,omitempty"`
	Status          string      `json:"status"`
	StartedAt       time.Time   `json:"started_at"`
	FinishedAt      time.Time   `json:"finished_at"`
	DurationSeconds float64     `json:"duration_seconds"`
	Steps           []drillStep `json:"steps"`
	Error           string      `json:"error,omitempty"`
}

func (r *drillReport) step(name, status, detail, output string, started time.Time) {
	r.Steps = append(r.Steps, drillStep{
		Name:            name,
		Status:          status,
		Detail:          detail,
		Output:          output,
		DurationSeconds: time.Since(started).Seconds(),
	})
}

func cmdDrill(ctx context.Context, args []string) error {
	fs := newFlagSet("drill")
	configPath := stringFlag(fs, "config", config.DefaultPath, "configuration file")
	snapshotRef := stringFlag(fs, "snapshot", "latest", "snapshot ID or latest")
	levelFlag := stringFlag(fs, "level", "", "drill level: files, service, db or full")
	targetFlag := stringFlag(fs, "target", "", "directory for the drill restore")
	keep := fs.Bool("keep", false, "keep the automatic drill directory")
	noNotify := fs.Bool("no-notify", false, "do not send alerts")
	yes := fs.Bool("yes", false, "confirm a restore into a system directory")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := prepare(cfg); err != nil {
		return err
	}
	level := cfg.Drill.Level
	if *levelFlag != "" {
		level = *levelFlag
	}
	if err := validateDrillLevel(level, cfg); err != nil {
		return err
	}
	runLock, busy, err := acquireRunLock(cfg, "drill")
	if err != nil {
		return err
	}
	if busy {
		return nil
	}
	defer runLock.Release()

	log := newLogger(*verbose)
	client := resticClientTo(cfg, log, os.Stderr)
	store := state.Store{Path: cfg.StateFile, HistoryPath: cfg.HistoryFile}
	now := time.Now()
	report := drillReport{Level: level, StartedAt: now, Status: state.StatusFailed}
	var snapshotID string
	var cause error

	finish := func(status string, err error) error {
		report.Status = status
		report.FinishedAt = time.Now()
		report.DurationSeconds = report.FinishedAt.Sub(report.StartedAt).Seconds()
		if err != nil {
			report.Error = err.Error()
			cause = err
		}
		details := make([]string, 0, len(report.Steps))
		for _, step := range report.Steps {
			details = append(details, step.Name+": "+step.Status)
		}
		run := state.Run{
			Kind:            state.KindDrill,
			Level:           level,
			StartedAt:       report.StartedAt,
			FinishedAt:      report.FinishedAt,
			Status:          status,
			SnapshotID:      snapshotID,
			DurationSeconds: report.DurationSeconds,
			Details:         details,
			Error:           report.Error,
		}
		if _, saveErr := store.Record(run); saveErr != nil {
			log.Error("cannot write drill state", "error", saveErr)
		}
		recordAudit(cfg, log, auditEntryForDrill(level, snapshotID, report.Target, status, report.Error))
		if !*noNotify {
			sendDrillAlert(ctx, cfg, log, report)
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if encErr := enc.Encode(report); encErr != nil && cause == nil {
				cause = encErr
			}
		} else {
			printDrillReport(report)
		}
		return cause
	}

	snapshots, err := client.Snapshots(ctx, "")
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	snapshot, err := resolveSnapshot(snapshots, *snapshotRef, cfg.Host)
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	snapshotID = snapshot.ID
	report.Snapshot = snapshot.ID

	autoTarget := *targetFlag == "" && cfg.Drill.Target == ""
	target := *targetFlag
	if target == "" {
		target = cfg.Drill.Target
	}
	if target == "" {
		target = restore.TestTarget(filepath.Join(filepath.Dir(cfg.StateFile), "drill"), now)
	}
	report.Target = target
	keepTarget := *keep || cfg.Drill.Keep

	stats, err := client.StatsFor(ctx, snapshot.ID, "restore-size")
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	if reason := verifySkipReason(stats.TotalSize, 0, cfg.DrillMaxSize, 0); reason != "" {
		report.step("size", state.StatusSkipped, reason, "", now)
		return finish(state.StatusSkipped, errors.New(reason))
	}

	includes := []string(nil)
	if level == "db" {
		includes = []string{cfg.Backup.Dumps.Dir}
	}
	plan, err := restore.New(snapshot.ID, target, includes, restore.OverwriteAlways)
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	plan.Host = cfg.Host
	plan.RetryLock = restic.LockRetry
	plan.Verify = level != "service"
	safeRoot := filepath.Join(filepath.Dir(cfg.StateFile), "drill")
	if plan.Dangerous(safeRoot) && !*yes {
		return finish(state.StatusFailed, fmt.Errorf("%w, pass --yes", restore.ErrDangerous))
	}
	target = plan.Target
	report.Target = target
	if err := restore.PrepareScratch(target, autoTarget); err != nil {
		return finish(state.StatusFailed, err)
	}
	cleaned := false
	if autoTarget && !keepTarget {
		defer func() {
			if !cleaned {
				_ = os.RemoveAll(target)
			}
		}()
	}

	free, err := freeBytes(target)
	if err != nil {
		return finish(state.StatusFailed, err)
	}
	if reason := verifySkipReason(0, free, 0, cfg.DrillMinFree); reason != "" {
		report.step("space", state.StatusSkipped, reason, "", now)
		return finish(state.StatusSkipped, errors.New(reason))
	}

	restoreCtx, cancel := context.WithTimeout(ctx, cfg.DrillTimeout)
	restoreStarted := time.Now()
	err = plan.Execute(restoreCtx, client)
	cancel()
	if err != nil {
		report.step("restore", state.StatusFailed, err.Error(), "", restoreStarted)
		return finish(state.StatusFailed, err)
	}
	report.step("restore", state.StatusSuccess, "snapshot restored", "", restoreStarted)
	if level == "files" {
		report.step("files", state.StatusSuccess, "restic --verify passed", "", restoreStarted)
	}

	if level == "service" || level == "full" {
		hookStarted := time.Now()
		output, err := runDrillShell(ctx, cfg.Drill.ServiceHook, drillEnv(cfg, level, snapshot.ID, target, ""), cfg.DrillTimeout)
		if err != nil {
			report.step("service", state.StatusFailed, err.Error(), output, hookStarted)
			return finish(state.StatusFailed, err)
		}
		report.step("service", state.StatusSuccess, "service hook passed", output, hookStarted)
	}

	if level == "db" || level == "full" {
		dbStarted := time.Now()
		dumpDir := filepath.Join(target, strings.TrimPrefix(cfg.Backup.Dumps.Dir, string(filepath.Separator)))
		rel, relErr := filepath.Rel(target, dumpDir)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			report.step("db", state.StatusFailed, "dump directory escapes restore target", "", dbStarted)
			return finish(state.StatusFailed, fmt.Errorf("drill db: dump directory escapes restore target: %s", dumpDir))
		}
		if info, err := os.Stat(dumpDir); err != nil {
			report.step("db", state.StatusFailed, "dump directory not found: "+dumpDir, "", dbStarted)
			return finish(state.StatusFailed, fmt.Errorf("drill db: dump directory not found: %s", dumpDir))
		} else if !info.IsDir() {
			report.step("db", state.StatusFailed, "dump path is not a directory: "+dumpDir, "", dbStarted)
			return finish(state.StatusFailed, fmt.Errorf("drill db: not a directory: %s", dumpDir))
		}
		output, err := runDrillShell(ctx, cfg.Drill.DBScript, drillEnv(cfg, level, snapshot.ID, target, dumpDir), cfg.DrillDBTimeout)
		if err != nil {
			report.step("db", state.StatusFailed, err.Error(), output, dbStarted)
			return finish(state.StatusFailed, err)
		}
		report.step("db", state.StatusSuccess, "database check passed", output, dbStarted)
		if level == "db" {
			report.step("files", state.StatusSuccess, "dump files restored and verified", "", dbStarted)
		}
	}

	if autoTarget && !keepTarget {
		cleanupStarted := time.Now()
		if err := os.RemoveAll(target); err != nil {
			report.step("cleanup", state.StatusFailed, err.Error(), "", cleanupStarted)
			return finish(state.StatusFailed, err)
		}
		cleaned = true
		report.step("cleanup", state.StatusSuccess, "removed "+target, "", cleanupStarted)
	}
	return finish(state.StatusSuccess, nil)
}

func validateDrillLevel(level string, cfg *config.Config) error {
	switch level {
	case "files", "service", "db", "full":
	default:
		return fmt.Errorf("unknown drill level %q, use files, service, db or full", level)
	}
	if (level == "service" || level == "full") && cfg.Drill.ServiceHook == "" {
		return errors.New("drill.service_hook is required for the service and full levels")
	}
	if (level == "db" || level == "full") && cfg.Drill.DBScript == "" {
		return errors.New("drill.db_script is required for the db and full levels")
	}
	return nil
}

func drillEnv(cfg *config.Config, level, snapshot, target, dumpDir string) map[string]string {
	env := map[string]string{
		"JM_EVENT":          "drill",
		"JM_PHASE":          level,
		"JM_STATUS":         "success",
		"JM_HOST":           cfg.Host,
		"JM_SNAPSHOT":       snapshot,
		"JM_DRILL_LEVEL":    level,
		"JM_DRILL_TARGET":   target,
		"JM_DRILL_DUMP_DIR": dumpDir,
	}
	return env
}

func runDrillShell(ctx context.Context, command string, env map[string]string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	extra := make([]string, 0, len(keys))
	for _, key := range keys {
		extra = append(extra, key+"="+env[key])
	}

	cmd := executil.Command(runCtx, "/bin/sh", "-c", command)
	cmd.Env = append(executil.Environment(), extra...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	text := strings.TrimSpace(output.String())
	if runCtx.Err() != nil {
		return text, fmt.Errorf("drill command timed out after %s", timeout)
	}
	if err != nil {
		return text, fmt.Errorf("drill command failed: %w", err)
	}
	return text, nil
}

func printDrillReport(report drillReport) {
	fmt.Printf("drill %s %s\n", report.Level, report.Status)
	if report.Snapshot != "" {
		fmt.Printf("  snapshot: %s\n", report.Snapshot)
	}
	if report.Target != "" {
		fmt.Printf("  target:   %s\n", report.Target)
	}
	for _, step := range report.Steps {
		mark := "ok"
		switch step.Status {
		case state.StatusFailed:
			mark = "fail"
		case state.StatusSkipped:
			mark = "skip"
		}
		fmt.Printf("  [%s] %s", mark, step.Name)
		if step.Detail != "" {
			fmt.Printf(": %s", step.Detail)
		}
		fmt.Printf(" (%.1fs)\n", step.DurationSeconds)
		if step.Status == state.StatusFailed && step.Output != "" {
			for _, line := range strings.Split(step.Output, "\n") {
				fmt.Printf("        %s\n", line)
			}
		}
	}
	if report.Error != "" {
		fmt.Printf("  error: %s\n", report.Error)
	}
}

func auditEntryForDrill(level, snapshot, target, result, message string) audit.Entry {
	return audit.Entry{
		Action:   "drill",
		Snapshot: snapshot,
		Target:   target,
		Result:   result,
		Error:    message,
	}
}

func sendDrillAlert(ctx context.Context, cfg *config.Config, log *slog.Logger, report drillReport) {
	notifier := notify.New(notify.Options{
		Webhooks:        cfg.Notify.Webhooks,
		DedupFile:       cfg.AlertsFile,
		DedupBackup:     cfg.DedupBackup,
		DedupFreshness:  cfg.DedupFreshness,
		Email:           notify.EmailOptionsFromConfig(cfg),
		TelegramToken:   cfg.Notify.TelegramToken,
		TelegramChatID:  cfg.Notify.TelegramChatID,
		TelegramChatIDs: cfg.Notify.TelegramChatIDs,
		NotifyOnSuccess: cfg.Notify.NotifyOnSuccess,
		Timeout:         cfg.NotifyTimeout,
		Logger:          log,
	})
	ev := notify.Event{
		Event:           "drill",
		Status:          report.Status,
		Host:            cfg.Host,
		Snapshot:        report.Snapshot,
		DurationSeconds: report.DurationSeconds,
		Error:           report.Error,
		Action:          "level=" + report.Level,
		Time:            report.FinishedAt,
	}
	if err := notifier.Send(ctx, ev); err != nil {
		fmt.Fprintf(os.Stderr, "jealous-vault: cannot send drill alert: %v\n", err)
	}
}
