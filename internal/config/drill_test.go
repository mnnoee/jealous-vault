// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestDrillDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Drill.Enabled {
		t.Fatal("drill must be disabled by default")
	}
	if cfg.Drill.Level != "files" {
		t.Fatalf("drill level = %q", cfg.Drill.Level)
	}
	if cfg.Drill.Schedule == "" {
		t.Fatal("drill schedule is empty")
	}
}

func TestDrillValidation(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		level   string
		hook    string
		db      string
		wantErr string
	}{
		{name: "disabled full without hooks", level: "full"},
		{name: "enabled files", enabled: true, level: "files"},
		{name: "enabled service without hook", enabled: true, level: "service", wantErr: "service_hook"},
		{name: "enabled db without script", enabled: true, level: "db", wantErr: "db_script"},
		{name: "enabled full with hooks", enabled: true, level: "full", hook: "true", db: "true"},
		{name: "bad level", level: "everything", wantErr: "drill.level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfigForValidation()
			cfg.Drill.Enabled = tc.enabled
			cfg.Drill.Level = tc.level
			cfg.Drill.ServiceHook = tc.hook
			cfg.Drill.DBScript = tc.db
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("valid config rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
