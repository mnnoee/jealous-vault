// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package restore

import (
	"os"
	"path/filepath"
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
	if !plan.Dangerous() {
		t.Fatal("includes must not bypass the target check")
	}

	safe, err := New("latest", "/tmp/restore", []string{"/etc"}, OverwriteNever)
	if err != nil {
		t.Fatal(err)
	}
	if safe.Dangerous() {
		t.Fatal("a non-system target with includes should not be dangerous")
	}
}

func TestDangerousSystemTargets(t *testing.T) {
	for _, target := range []string{"/etc", "/etc/nginx", "/srv/restore", "/var/lib/app", "/boot", "/root"} {
		plan, err := New("latest", target, nil, OverwriteAlways)
		if err != nil {
			t.Fatal(err)
		}
		if !plan.Dangerous() {
			t.Errorf("restore into %s must require confirmation", target)
		}
	}
	for _, target := range []string{"/data/restore", "/tmp/restore", "/mnt/backup"} {
		plan, err := New("latest", target, nil, OverwriteAlways)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Dangerous() {
			t.Errorf("restore into %s must not require confirmation", target)
		}
	}
}

func TestDangerousMessage(t *testing.T) {
	if !strings.Contains(ErrDangerous.Error(), "system directory") {
		t.Fatalf("error text does not explain the real cause: %q", ErrDangerous)
	}
}

func TestDangerousSafeRoot(t *testing.T) {
	plan, err := New("latest", "/var/lib/jealous-vault/verify/20260101-000000", nil, OverwriteAlways)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Dangerous("/var/lib/jealous-vault/verify") {
		t.Fatal("the automatic verify directory must be treated as safe")
	}
	if !plan.Dangerous() {
		t.Fatal("without a safe root the same target is a system path")
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

func TestResolveTargetFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatal(err)
	}
	planned, err := New("latest", link, nil, OverwriteNever)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks("/etc")
	if err != nil {
		t.Fatal(err)
	}
	if planned.Target != want {
		t.Fatalf("target = %q, want %q", planned.Target, want)
	}
	if !planned.Dangerous() {
		t.Fatal("a symlinked system target must be dangerous")
	}
}

func TestPrepareScratch(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "run")
	if err := PrepareScratch(target, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %o, want 700", info.Mode().Perm())
	}
	if err := PrepareScratch(target, true); err == nil {
		t.Fatal("expected an EEXIST error for an automatic target")
	}
	if err := PrepareScratch("/", true); err == nil {
		t.Fatal("expected / to be rejected")
	}
}
