// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package systemd installs the agent as systemd services and timers.
package systemd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mnnoee/jealous-vault/internal/config"
)

// Unit names written by Install.
const (
	BackupService    = "jealous-vault-backup.service"
	BackupTimer      = "jealous-vault-backup.timer"
	CheckService     = "jealous-vault-check.service"
	CheckTimer       = "jealous-vault-check.timer"
	FreshnessService = "jealous-vault-freshness.service"
	FreshnessTimer   = "jealous-vault-freshness.timer"
	VerifyService    = "jealous-vault-verify.service"
	VerifyTimer      = "jealous-vault-verify.timer"
	ProxmoxService   = "jealous-vault-proxmox.service"
	ProxmoxTimer     = "jealous-vault-proxmox.timer"
)

const checkCalendar = "Sun *-*-* 04:30:00"

// Installer writes units and asks systemctl to load them.
type Installer struct {
	BinaryPath string
	ConfigPath string
	UnitDir    string
	Systemctl  string
	SkipStart  bool
	Out        io.Writer
	Log        *slog.Logger
}

// Install copies the binary, writes the units and starts the timers.
func (i *Installer) Install(ctx context.Context, cfg *config.Config) error {
	i.defaults()
	out := i.Out
	if out == nil {
		out = io.Discard
	}

	if err := os.MkdirAll(filepath.Dir(i.ConfigPath), 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StateFile), 0o750); err != nil {
		return err
	}
	if err := i.installBinary(); err != nil {
		return err
	}

	units := map[string]string{
		BackupService:    backupService(i.BinaryPath, i.ConfigPath),
		BackupTimer:      backupTimer(cfg.Schedule.OnCalendar, cfg.Schedule.RandomizedDelay),
		CheckService:     checkService(i.BinaryPath, i.ConfigPath),
		CheckTimer:       checkTimer(),
		FreshnessService: freshnessService(i.BinaryPath, i.ConfigPath),
		FreshnessTimer:   freshnessTimer(cfg.Freshness.Schedule),
	}
	if cfg.Verify.Enabled {
		units[VerifyService] = verifyService(i.BinaryPath, i.ConfigPath)
		units[VerifyTimer] = verifyTimer(cfg.Verify.Schedule)
	} else {
		for _, name := range []string{VerifyTimer, VerifyService} {
			_ = os.Remove(filepath.Join(i.UnitDir, name))
		}
	}
	if cfg.Proxmox.Enabled {
		if err := os.MkdirAll(cfg.Proxmox.DumpDir, 0o750); err != nil {
			return err
		}
		units[ProxmoxService] = proxmoxService(i.BinaryPath, i.ConfigPath)
		units[ProxmoxTimer] = proxmoxTimer(cfg.Proxmox.Schedule)
	} else {
		for _, name := range []string{ProxmoxTimer, ProxmoxService} {
			_ = os.Remove(filepath.Join(i.UnitDir, name))
		}
	}
	for name, body := range units {
		if err := writeFile(filepath.Join(i.UnitDir, name), []byte(body)); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s\n", filepath.Join(i.UnitDir, name))
	}

	if err := i.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if i.SkipStart {
		fmt.Fprintln(out, "timers written but not started (--no-start)")
		return nil
	}
	timers := []string{BackupTimer, CheckTimer, FreshnessTimer}
	if cfg.Verify.Enabled {
		timers = append(timers, VerifyTimer)
	}
	if cfg.Proxmox.Enabled {
		timers = append(timers, ProxmoxTimer)
	}
	if err := i.systemctl(ctx, append([]string{"enable", "--now"}, timers...)...); err != nil {
		return err
	}
	fmt.Fprintln(out, "timers enabled")
	return nil
}

// Uninstall stops the timers and removes the unit files. Configuration and
// backups are left alone.
func (i *Installer) Uninstall(ctx context.Context) error {
	i.defaults()
	out := i.Out
	if out == nil {
		out = io.Discard
	}

	timers := i.installedTimers()
	var errs []error
	if len(timers) > 0 {
		if err := i.systemctl(ctx, append([]string{"disable", "--now"}, timers...)...); err != nil {
			errs = append(errs, err)
		}
	}
	for _, name := range []string{
		BackupTimer, BackupService,
		CheckTimer, CheckService,
		FreshnessTimer, FreshnessService,
		VerifyTimer, VerifyService,
		ProxmoxTimer, ProxmoxService,
	} {
		path := filepath.Join(i.UnitDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		} else {
			fmt.Fprintf(out, "removed %s\n", path)
		}
	}
	if err := i.systemctl(ctx, "daemon-reload"); err != nil {
		errs = append(errs, err)
	}
	fmt.Fprintf(out, "kept %s\n", i.ConfigPath)
	return join(errs)
}

func (i *Installer) installedTimers() []string {
	var timers []string
	for _, name := range []string{BackupTimer, CheckTimer, FreshnessTimer, VerifyTimer, ProxmoxTimer} {
		if _, err := os.Stat(filepath.Join(i.UnitDir, name)); err == nil {
			timers = append(timers, name)
		}
	}
	return timers
}

func (i *Installer) defaults() {
	if i.BinaryPath == "" {
		i.BinaryPath = "/usr/local/bin/jealous-vault"
	}
	if i.ConfigPath == "" {
		i.ConfigPath = config.DefaultPath
	}
	if i.UnitDir == "" {
		i.UnitDir = "/etc/systemd/system"
	}
	if i.Systemctl == "" {
		i.Systemctl = "systemctl"
	}
}

func (i *Installer) installBinary() error {
	current, err := os.Executable()
	if err != nil {
		return err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return err
	}
	target := filepath.Clean(i.BinaryPath)
	if current == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	data, err := os.ReadFile(current)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".jealous-vault-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

func (i *Installer) systemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, i.Systemctl, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err != nil {
		text := strings.TrimSpace(output.String())
		if text == "" {
			return fmt.Errorf("%s %s: %w", i.Systemctl, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %w: %s", i.Systemctl, strings.Join(args, " "), err, text)
	}
	if i.Log != nil {
		i.Log.Debug("systemctl", "args", args)
	}
	return nil
}

func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".unit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(body); err != nil {
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
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func backupService(binary, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=JealousVault backup
Documentation=https://github.com/mnnoee/jealous-vault
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s backup --config %s
TimeoutStartSec=0
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=7
`, quoteArg(binary), quoteArg(configPath))
}

func backupTimer(onCalendar, randomizedDelay string) string {
	if randomizedDelay == "" {
		randomizedDelay = "15m"
	}
	return fmt.Sprintf(`[Unit]
Description=JealousVault backup timer

[Timer]
OnCalendar=%s
RandomizedDelaySec=%s
Persistent=true
AccuracySec=1m
Unit=%s

[Install]
WantedBy=timers.target
`, onCalendar, randomizedDelay, BackupService)
}

func checkService(binary, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=JealousVault repository check
Documentation=https://github.com/mnnoee/jealous-vault
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s check --config %s
TimeoutStartSec=0
Nice=15
IOSchedulingClass=best-effort
IOSchedulingPriority=7
`, quoteArg(binary), quoteArg(configPath))
}

func checkTimer() string {
	return fmt.Sprintf(`[Unit]
Description=JealousVault repository check timer

[Timer]
OnCalendar=%s
RandomizedDelaySec=1h
Persistent=true
AccuracySec=1m
Unit=%s

[Install]
WantedBy=timers.target
`, checkCalendar, CheckService)
}

func freshnessService(binary, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=JealousVault backup freshness check
Documentation=https://github.com/mnnoee/jealous-vault
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s freshness --config %s
TimeoutStartSec=2m
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=7
`, quoteArg(binary), quoteArg(configPath))
}

func freshnessTimer(schedule string) string {
	if schedule == "" {
		schedule = "*-*-* 00,06,12,18:15:00"
	}
	return fmt.Sprintf(`[Unit]
Description=JealousVault backup freshness timer

[Timer]
OnCalendar=%s
Persistent=true
AccuracySec=1m
Unit=%s

[Install]
WantedBy=timers.target
`, schedule, FreshnessService)
}

func verifyService(binary, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=JealousVault scheduled test restore
Documentation=https://github.com/mnnoee/jealous-vault
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s test-restore --config %s
TimeoutStartSec=0
Nice=15
IOSchedulingClass=best-effort
IOSchedulingPriority=7
`, quoteArg(binary), quoteArg(configPath))
}

func verifyTimer(schedule string) string {
	if schedule == "" {
		schedule = "Sun *-*-* 05:00:00"
	}
	return fmt.Sprintf(`[Unit]
Description=JealousVault test restore timer

[Timer]
OnCalendar=%s
RandomizedDelaySec=1h
Persistent=true
AccuracySec=1m
Unit=%s

[Install]
WantedBy=timers.target
`, schedule, VerifyService)
}

func proxmoxService(binary, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=JealousVault Proxmox dump and backup
Documentation=https://github.com/mnnoee/jealous-vault
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s vzdump --config %s
TimeoutStartSec=0
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=7
`, quoteArg(binary), quoteArg(configPath))
}

func proxmoxTimer(schedule string) string {
	if schedule == "" {
		schedule = "*-*-* 02:00:00"
	}
	return fmt.Sprintf(`[Unit]
Description=JealousVault Proxmox dump timer

[Timer]
OnCalendar=%s
RandomizedDelaySec=15m
Persistent=true
AccuracySec=1m
Unit=%s

[Install]
WantedBy=timers.target
`, schedule, ProxmoxService)
}

func quoteArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s) + `"`
}

func join(errs []error) error {
	return errors.Join(errs...)
}
