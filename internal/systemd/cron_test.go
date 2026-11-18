// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package systemd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func TestCronSchedule(t *testing.T) {
	cases := map[string]string{
		"*-*-* 03:30:00":          "30 3 * * *",
		"*-*-* 00,06,12,18:15:00": "15 0,6,12,18 * * *",
		"Sun *-*-* 04:30:00":      "30 4 * * 0",
		"Mon *-*-* 01:05:00":      "5 1 * * 1",
		"*-*-* *:*:00":            "* * * * *",
	}
	for calendar, want := range cases {
		got, err := cronSchedule(calendar)
		if err != nil {
			t.Errorf("%q: %v", calendar, err)
			continue
		}
		if got != want {
			t.Errorf("%q = %q, want %q", calendar, got, want)
		}
	}
	if _, err := cronSchedule("2026-11-18 03:00:00"); err == nil {
		t.Error("explicit date was accepted, cron mode should refuse it")
	}
}

func TestInstallCron(t *testing.T) {
	dir := t.TempDir()
	cron := filepath.Join(dir, "cron.d")
	cfg := config.Default()
	cfg.StateFile = filepath.Join(dir, "state.json")
	cfg.Schedule.OnCalendar = "*-*-* 03:30:00"
	cfg.Freshness.Schedule = "*-*-* 00,06,12,18:15:00"
	cfg.Verify.Schedule = "Sun *-*-* 05:00:00"
	cfg.RandomizedDelay = 30 * time.Minute

	installer := &Installer{
		BinaryPath: filepath.Join(dir, "jealous-vault"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		Init:       "cron",
		CronDir:    cron,
		SkipStart:  true,
	}
	if err := installer.Install(context.Background(), cfg); err != nil {
		t.Fatalf("Install: %v", err)
	}

	path := filepath.Join(cron, "jealous-vault")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"30 3 * * * root",
		"backup --config",
		"15 0,6,12,18 * * * root",
		"freshness --config",
		"30 4 * * 0 root",
		"check --config",
		"0 5 * * 0 root",
		"test-restore --config",
		"sleep $(awk",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("cron file does not contain %q:\n%s", want, text)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("cron file mode = %o, want 644", info.Mode().Perm())
	}

	if err := installer.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cron file still exists: %v", err)
	}
}

func TestInstallCronRejectsExplicitDate(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Schedule.OnCalendar = "2026-11-18 03:30:00"
	installer := &Installer{
		BinaryPath: filepath.Join(dir, "jealous-vault"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		Init:       "cron",
		CronDir:    filepath.Join(dir, "cron.d"),
	}
	if err := installer.Install(context.Background(), cfg); err == nil {
		t.Fatal("explicit calendar date was accepted in cron mode")
	}
}
