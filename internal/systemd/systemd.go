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
	CopyService      = "jealous-vault-copy.service"
	CopyTimer        = "jealous-vault-copy.timer"
)

const checkCalendar = "Sun *-*-* 04:30:00"

// Installer writes units and asks systemctl to load them.
type Installer struct {
	BinaryPath  string
	ConfigPath  string
	Job         string
	UnitDir     string
	Systemctl   string
	Init        string
	RCUpdate    string
	RCService   string
	PeriodicDir string
	SkipStart   bool
	Out         io.Writer
	Log         *slog.Logger
}

type unitNames struct {
	backupService    string
	backupTimer      string
	checkService     string
	checkTimer       string
	freshnessService string
	freshnessTimer   string
	verifyService    string
	verifyTimer      string
	proxmoxService   string
	proxmoxTimer     string
	copyService      string
	copyTimer        string
}

func jobSuffix(job string) string {
	if job == "" || job == "default" {
		return ""
	}
	return "-" + job
}

func unitName(base, job, suffix string) string {
	return "jealous-vault-" + base + jobSuffix(job) + suffix
}

func periodicName(base, job string) string {
	return "jealous-vault-" + base + jobSuffix(job)
}

// PeriodicName returns the OpenRC script name for one job.
func PeriodicName(base, job string) string { return periodicName(base, job) }

// BackupTimerName returns the timer unit for one job.
func BackupTimerName(job string) string { return unitName("backup", job, ".timer") }

// CheckTimerName returns the check timer unit for one job.
func CheckTimerName(job string) string { return unitName("check", job, ".timer") }

// FreshnessTimerName returns the freshness timer unit for one job.
func FreshnessTimerName(job string) string { return unitName("freshness", job, ".timer") }

// VerifyTimerName returns the verify timer unit for one job.
func VerifyTimerName(job string) string { return unitName("verify", job, ".timer") }

// ProxmoxTimerName returns the Proxmox timer unit for one job.
func ProxmoxTimerName(job string) string { return unitName("proxmox", job, ".timer") }

// CopyTimerName returns the copy timer unit for one job.
func CopyTimerName(job string) string { return unitName("copy", job, ".timer") }

func (i *Installer) names() unitNames {
	job := i.Job
	return unitNames{
		backupService:    unitName("backup", job, ".service"),
		backupTimer:      unitName("backup", job, ".timer"),
		checkService:     unitName("check", job, ".service"),
		checkTimer:       unitName("check", job, ".timer"),
		freshnessService: unitName("freshness", job, ".service"),
		freshnessTimer:   unitName("freshness", job, ".timer"),
		verifyService:    unitName("verify", job, ".service"),
		verifyTimer:      unitName("verify", job, ".timer"),
		proxmoxService:   unitName("proxmox", job, ".service"),
		proxmoxTimer:     unitName("proxmox", job, ".timer"),
		copyService:      unitName("copy", job, ".service"),
		copyTimer:        unitName("copy", job, ".timer"),
	}
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

	switch i.initSystem() {
	case "openrc":
		return i.installOpenRC(ctx, cfg, out)
	case "systemd":
	default:
		return errors.New("neither systemctl nor rc-update found, cannot install services")
	}

	n := i.names()
	units := map[string]string{
		n.backupService:    hardened(backupService(i.BinaryPath, i.ConfigPath), "UMask=0077\n"),
		n.backupTimer:      backupTimer(cfg.Schedule.OnCalendar, cfg.Schedule.RandomizedDelay, n.backupService),
		n.checkService:     hardened(checkService(i.BinaryPath, i.ConfigPath), "UMask=0077\n"),
		n.checkTimer:       checkTimer(n.checkService),
		n.freshnessService: hardened(freshnessService(i.BinaryPath, i.ConfigPath), "UMask=0077\n"),
		n.freshnessTimer:   freshnessTimer(cfg.Freshness.Schedule, n.freshnessService),
	}
	if cfg.Verify.Enabled {
		units[n.verifyService] = hardened(verifyService(i.BinaryPath, i.ConfigPath), "")
		units[n.verifyTimer] = verifyTimer(cfg.Verify.Schedule, n.verifyService)
	} else {
		for _, name := range []string{n.verifyTimer, n.verifyService} {
			_ = os.Remove(filepath.Join(i.UnitDir, name))
		}
	}
	if cfg.Proxmox.Enabled {
		if err := os.MkdirAll(cfg.Proxmox.DumpDir, 0o755); err != nil {
			return err
		}
		units[n.proxmoxService] = hardened(proxmoxService(i.BinaryPath, i.ConfigPath), "")
		units[n.proxmoxTimer] = proxmoxTimer(cfg.Proxmox.Schedule, n.proxmoxService)
	} else {
		for _, name := range []string{n.proxmoxTimer, n.proxmoxService} {
			_ = os.Remove(filepath.Join(i.UnitDir, name))
		}
	}
	if cfg.Copy.Enabled {
		units[n.copyService] = hardened(copyService(i.BinaryPath, i.ConfigPath), "UMask=0077\n")
		units[n.copyTimer] = copyTimer(cfg.Copy.Schedule, n.copyService)
	} else {
		for _, name := range []string{n.copyTimer, n.copyService} {
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
	timers := []string{n.backupTimer, n.checkTimer, n.freshnessTimer}
	if cfg.Verify.Enabled {
		timers = append(timers, n.verifyTimer)
	}
	if cfg.Proxmox.Enabled {
		timers = append(timers, n.proxmoxTimer)
	}
	if cfg.Copy.Enabled {
		timers = append(timers, n.copyTimer)
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
	if i.initSystem() == "openrc" {
		return i.uninstallOpenRC(out)
	}

	n := i.names()
	timers := i.installedTimers()
	var errs []error
	if len(timers) > 0 {
		if err := i.systemctl(ctx, append([]string{"disable", "--now"}, timers...)...); err != nil {
			errs = append(errs, err)
		}
	}
	for _, name := range []string{
		n.backupTimer, n.backupService,
		n.checkTimer, n.checkService,
		n.freshnessTimer, n.freshnessService,
		n.verifyTimer, n.verifyService,
		n.proxmoxTimer, n.proxmoxService,
		n.copyTimer, n.copyService,
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

func (i *Installer) installOpenRC(ctx context.Context, cfg *config.Config, out io.Writer) error {
	backupName := periodicName("backup", i.Job)
	checkName := periodicName("check", i.Job)
	freshnessName := periodicName("freshness", i.Job)
	verifyName := periodicName("verify", i.Job)
	proxmoxName := periodicName("proxmox", i.Job)
	copyName := periodicName("copy", i.Job)
	scripts := map[string]string{
		filepath.Join(i.PeriodicDir, "daily", backupName):    periodicScript(i.BinaryPath, "backup", i.ConfigPath),
		filepath.Join(i.PeriodicDir, "weekly", checkName):    periodicScript(i.BinaryPath, "check", i.ConfigPath),
		filepath.Join(i.PeriodicDir, "daily", freshnessName): periodicScript(i.BinaryPath, "freshness", i.ConfigPath),
	}
	if cfg.Verify.Enabled {
		scripts[filepath.Join(i.PeriodicDir, "weekly", verifyName)] = periodicScript(i.BinaryPath, "test-restore", i.ConfigPath)
	}
	if cfg.Proxmox.Enabled {
		if err := os.MkdirAll(cfg.Proxmox.DumpDir, 0o755); err != nil {
			return err
		}
		scripts[filepath.Join(i.PeriodicDir, "daily", proxmoxName)] = periodicScript(i.BinaryPath, "vzdump", i.ConfigPath)
	}
	if cfg.Copy.Enabled {
		scripts[filepath.Join(i.PeriodicDir, "daily", copyName)] = periodicScript(i.BinaryPath, "repo copy", i.ConfigPath)
	}
	for path, body := range scripts {
		if err := writeFile(path, []byte(body)); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o755); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s\n", path)
	}
	for _, name := range []string{verifyName, proxmoxName, copyName} {
		if _, ok := scripts[filepath.Join(i.PeriodicDir, "daily", name)]; ok {
			continue
		}
		if _, ok := scripts[filepath.Join(i.PeriodicDir, "weekly", name)]; ok {
			continue
		}
		_ = os.Remove(filepath.Join(i.PeriodicDir, "daily", name))
		_ = os.Remove(filepath.Join(i.PeriodicDir, "weekly", name))
	}
	if i.SkipStart {
		fmt.Fprintln(out, "periodic scripts written but not started (--no-start)")
		return nil
	}
	if err := i.runRC(ctx, i.RCUpdate, "add", "crond", "default"); err != nil {
		return err
	}
	if err := i.runRC(ctx, i.RCService, "crond", "start"); err != nil {
		return err
	}
	fmt.Fprintln(out, "OpenRC periodic jobs enabled")
	return nil
}

func (i *Installer) uninstallOpenRC(out io.Writer) error {
	names := []string{
		periodicName("backup", i.Job), periodicName("check", i.Job), periodicName("freshness", i.Job),
		periodicName("verify", i.Job), periodicName("proxmox", i.Job), periodicName("copy", i.Job),
	}
	for _, period := range []string{"daily", "weekly"} {
		for _, name := range names {
			path := filepath.Join(i.PeriodicDir, period, name)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			} else {
				fmt.Fprintf(out, "removed %s\n", path)
			}
		}
	}
	fmt.Fprintf(out, "kept %s\n", i.ConfigPath)
	return nil
}

func (i *Installer) runRC(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return nil
}

func periodicScript(binary, command, configPath string) string {
	return "#!/bin/sh\nexec " + shellQuote(binary) + " " + command + " --config " + shellQuote(configPath) + "\n"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (i *Installer) installedTimers() []string {
	n := i.names()
	var timers []string
	for _, name := range []string{n.backupTimer, n.checkTimer, n.freshnessTimer, n.verifyTimer, n.proxmoxTimer, n.copyTimer} {
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
	if i.RCUpdate == "" {
		i.RCUpdate = "rc-update"
	}
	if i.RCService == "" {
		i.RCService = "rc-service"
	}
	if i.PeriodicDir == "" {
		i.PeriodicDir = "/etc/periodic"
	}
}

func (i *Installer) initSystem() string {
	if i.Init != "" {
		return i.Init
	}
	if _, err := exec.LookPath(i.Systemctl); err == nil {
		return "systemd"
	}
	if _, err := exec.LookPath(i.RCUpdate); err == nil {
		return "openrc"
	}
	return "unknown"
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

const serviceHardening = `NoNewPrivileges=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
`

// hardened adds the common sandboxing options to a service. The agent needs
// root to read the whole filesystem and to restore it, so the options focus on
// blocking privilege escalation and kernel changes instead of hiding paths.
func hardened(body, extra string) string {
	marker := "IOSchedulingPriority=7\n"
	return strings.Replace(body, marker, marker+extra+serviceHardening, 1)
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

func backupTimer(onCalendar, randomizedDelay, service string) string {
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
`, onCalendar, randomizedDelay, service)
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

func checkTimer(service string) string {
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
`, checkCalendar, service)
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

func freshnessTimer(schedule, service string) string {
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
`, schedule, service)
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

func verifyTimer(schedule, service string) string {
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
`, schedule, service)
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

func proxmoxTimer(schedule, service string) string {
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
`, schedule, service)
}

func copyService(binary, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=JealousVault repository copy
Documentation=https://github.com/mnnoee/jealous-vault
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s repo copy --config %s
TimeoutStartSec=0
Nice=15
IOSchedulingClass=best-effort
IOSchedulingPriority=7
`, quoteArg(binary), quoteArg(configPath))
}

func copyTimer(schedule, service string) string {
	if schedule == "" {
		schedule = "*-*-* 05:00:00"
	}
	return fmt.Sprintf(`[Unit]
Description=JealousVault repository copy timer

[Timer]
OnCalendar=%s
RandomizedDelaySec=30m
Persistent=true
AccuracySec=1m
Unit=%s

[Install]
WantedBy=timers.target
`, schedule, service)
}

func quoteArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s) + `"`
}

func join(errs []error) error {
	return errors.Join(errs...)
}
