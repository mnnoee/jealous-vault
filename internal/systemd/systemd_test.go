// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package systemd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func fakeSystemctl(t *testing.T, dir string) (path, logFile string) {
	t.Helper()
	logFile = filepath.Join(dir, "systemctl.log")
	path = filepath.Join(dir, "systemctl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logFile + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, logFile
}

func TestInstallWritesUnitsAndStartsTimers(t *testing.T) {
	dir := t.TempDir()
	units := filepath.Join(dir, "units")
	binary := filepath.Join(dir, "bin", "jealous-vault")
	systemctl, logFile := fakeSystemctl(t, dir)

	cfg := config.Default()
	cfg.StateFile = filepath.Join(dir, "state", "state.json")
	cfg.Schedule.OnCalendar = "*-*-* 02:15:00"
	cfg.Schedule.RandomizedDelay = "20m"

	installer := &Installer{
		BinaryPath: binary,
		ConfigPath: filepath.Join(dir, "etc", "config.toml"),
		UnitDir:    units,
		Systemctl:  systemctl,
	}
	if err := installer.Install(context.Background(), cfg); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("binary was not installed: %v", err)
	}
	for _, name := range []string{BackupService, BackupTimer, CheckService, CheckTimer, FreshnessService, FreshnessTimer, VerifyService, VerifyTimer} {
		if _, err := os.Stat(filepath.Join(units, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}

	timer, err := os.ReadFile(filepath.Join(units, BackupTimer))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OnCalendar=*-*-* 02:15:00", "RandomizedDelaySec=20m", "Persistent=true"} {
		if !strings.Contains(string(timer), want) {
			t.Errorf("timer does not contain %q:\n%s", want, timer)
		}
	}

	service, err := os.ReadFile(filepath.Join(units, BackupService))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), "ExecStart=\""+binary+"\" backup --config \""+filepath.Join(dir, "etc", "config.toml")+"\"") {
		t.Errorf("unexpected ExecStart:\n%s", service)
	}
	if !strings.Contains(string(service), "TimeoutStartSec=0") {
		t.Errorf("long backups would be killed by the default timeout:\n%s", service)
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"daemon-reload", "enable --now " + BackupTimer + " " + CheckTimer + " " + FreshnessTimer + " " + VerifyTimer} {
		if !strings.Contains(string(log), want) {
			t.Errorf("systemctl log does not contain %q:\n%s", want, log)
		}
	}
}

func TestInstallNoStart(t *testing.T) {
	dir := t.TempDir()
	systemctl, logFile := fakeSystemctl(t, dir)
	cfg := config.Default()
	cfg.StateFile = filepath.Join(dir, "state.json")

	installer := &Installer{
		BinaryPath: filepath.Join(dir, "bin", "jealous-vault"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		UnitDir:    filepath.Join(dir, "units"),
		Systemctl:  systemctl,
		SkipStart:  true,
	}
	if err := installer.Install(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "enable") {
		t.Errorf("timers started despite --no-start:\n%s", log)
	}
}

func TestUninstallRemovesUnits(t *testing.T) {
	dir := t.TempDir()
	units := filepath.Join(dir, "units")
	systemctl, logFile := fakeSystemctl(t, dir)
	cfg := config.Default()
	cfg.StateFile = filepath.Join(dir, "state.json")

	installer := &Installer{
		BinaryPath: filepath.Join(dir, "bin", "jealous-vault"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		UnitDir:    units,
		Systemctl:  systemctl,
	}
	if err := installer.Install(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := installer.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	entries, err := os.ReadDir(units)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("units left behind: %v", entries)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "disable --now "+BackupTimer+" "+CheckTimer+" "+FreshnessTimer+" "+VerifyTimer) {
		t.Errorf("timers were not disabled:\n%s", log)
	}
}

func TestQuoteArg(t *testing.T) {
	got := quoteArg(`/opt/my backup/100%/bin`)
	want := `"/opt/my backup/100%%/bin"`
	if got != want {
		t.Fatalf("quoteArg = %q, want %q", got, want)
	}
}

func TestInstallProxmoxEnabled(t *testing.T) {
	dir := t.TempDir()
	units := filepath.Join(dir, "units")
	systemctl, logFile := fakeSystemctl(t, dir)
	dumpDir := filepath.Join(dir, "dumps")

	cfg := config.Default()
	cfg.StateFile = filepath.Join(dir, "state.json")
	cfg.Proxmox.Enabled = true
	cfg.Proxmox.DumpDir = dumpDir

	installer := &Installer{
		BinaryPath: filepath.Join(dir, "bin", "jealous-vault"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		UnitDir:    units,
		Systemctl:  systemctl,
		SkipStart:  false,
	}
	if err := installer.Install(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ProxmoxService, ProxmoxTimer} {
		if _, err := os.Stat(filepath.Join(units, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	if _, err := os.Stat(dumpDir); err != nil {
		t.Errorf("dump dir was not created: %v", err)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), ProxmoxTimer) {
		t.Errorf("proxmox timer was not enabled:\n%s", log)
	}
}

func TestInstallCopyEnabled(t *testing.T) {
	dir := t.TempDir()
	units := filepath.Join(dir, "units")
	systemctl, logFile := fakeSystemctl(t, dir)

	cfg := config.Default()
	cfg.StateFile = filepath.Join(dir, "state.json")
	cfg.Copy.Enabled = true
	cfg.Copy.Repository = "local:/copy"
	cfg.Copy.PasswordFile = filepath.Join(dir, "copy.pass")

	installer := &Installer{
		BinaryPath: filepath.Join(dir, "bin", "jealous-vault"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		UnitDir:    units,
		Systemctl:  systemctl,
	}
	if err := installer.Install(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{CopyService, CopyTimer} {
		if _, err := os.Stat(filepath.Join(units, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), CopyTimer) {
		t.Errorf("copy timer was not enabled:\n%s", log)
	}
}
