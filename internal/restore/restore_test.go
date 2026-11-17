// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package restore

import (
	"strings"
	"testing"
	"time"
)

func TestNewDefaults(t *testing.T) {
	plan, err := New("", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Snapshot != "latest" {
		t.Errorf("snapshot = %q", plan.Snapshot)
	}
	if plan.Target != "/" {
		t.Errorf("target = %q", plan.Target)
	}
	if !plan.Dangerous() {
		t.Error("restoring everything over / must be dangerous")
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	cases := []struct {
		name      string
		snapshot  string
		target    string
		includes  []string
		overwrite string
	}{
		{"relative target", "latest", "etc", nil, ""},
		{"bad overwrite", "latest", "/", nil, "sometimes"},
		{"relative include", "latest", "/", []string{"etc"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.snapshot, tc.target, tc.includes, tc.overwrite); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDangerousWithIncludes(t *testing.T) {
	plan, err := New("latest", "/", []string{"/etc", "/var/www"}, OverwriteNever)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Dangerous() {
		t.Fatal("a restore limited to paths is not the whole-snapshot case")
	}
}

func TestDescribe(t *testing.T) {
	plan, err := New("abc", "/srv/restore", []string{"/etc"}, OverwriteAlways)
	if err != nil {
		t.Fatal(err)
	}
	text := plan.Describe()
	for _, want := range []string{"abc", "/srv/restore", "/etc", "always"} {
		if !strings.Contains(text, want) {
			t.Errorf("description %q does not contain %q", text, want)
		}
	}
}

func TestTestTarget(t *testing.T) {
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	got := TestTarget("/srv/test", now)
	want := "/srv/test/20260203-040506"
	if got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
}
