// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
)

func TestValidateDrillLevel(t *testing.T) {
	cfg := config.Default()
	if err := validateDrillLevel("files", cfg); err != nil {
		t.Fatalf("files: %v", err)
	}
	if err := validateDrillLevel("service", cfg); err == nil {
		t.Fatal("service without hook must fail")
	}
	cfg.Drill.ServiceHook = "true"
	if err := validateDrillLevel("service", cfg); err != nil {
		t.Fatalf("service: %v", err)
	}
	if err := validateDrillLevel("db", cfg); err == nil {
		t.Fatal("db without script must fail")
	}
	cfg.Drill.DBScript = "true"
	if err := validateDrillLevel("db", cfg); err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := validateDrillLevel("full", cfg); err != nil {
		t.Fatalf("full: %v", err)
	}
	if err := validateDrillLevel("bogus", cfg); err == nil {
		t.Fatal("bad level must fail")
	}
}

func TestDrillEnv(t *testing.T) {
	cfg := config.Default()
	cfg.Host = "host1"
	env := drillEnv(cfg, "db", "snap123", "/var/lib/jealous-vault/drill/run", "/var/lib/jealous-vault/drill/run/var/lib/jealous-vault/dumps")
	for key, want := range map[string]string{
		"JM_EVENT":          "drill",
		"JM_PHASE":          "db",
		"JM_STATUS":         "success",
		"JM_HOST":           "host1",
		"JM_SNAPSHOT":       "snap123",
		"JM_DRILL_LEVEL":    "db",
		"JM_DRILL_TARGET":   "/var/lib/jealous-vault/drill/run",
		"JM_DRILL_DUMP_DIR": "/var/lib/jealous-vault/drill/run/var/lib/jealous-vault/dumps",
	} {
		if env[key] != want {
			t.Errorf("env[%s] = %q, want %q", key, env[key], want)
		}
	}
}

func TestRunDrillShell(t *testing.T) {
	out, err := runDrillShell(context.Background(), `printf '%s' "$JM_DRILL_LEVEL"`, map[string]string{"JM_DRILL_LEVEL": "db"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out != "db" {
		t.Fatalf("output = %q", out)
	}

	out, err = runDrillShell(context.Background(), `echo boom >&2; exit 7`, nil, time.Second)
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("output = %q", out)
	}
}
