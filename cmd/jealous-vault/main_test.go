// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/mnnoee/jealous-vault/internal/restic"
)

func TestSplitComma(t *testing.T) {
	got := splitComma(" /etc, /var/www ,,")
	if len(got) != 2 || got[0] != "/etc" || got[1] != "/var/www" {
		t.Fatalf("splitComma = %v", got)
	}
	if splitComma("  ") != nil {
		t.Fatal("empty input must produce nil")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:             "512 B",
		2048:            "2.0 KiB",
		5 * 1024 * 1024: "5.0 MiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestExitCode(t *testing.T) {
	if got := exitCode(errors.New("plain")); got != 1 {
		t.Errorf("plain error code = %d", got)
	}
	if got := exitCode(&exitError{code: 3, err: errors.New("custom")}); got != 3 {
		t.Errorf("custom error code = %d", got)
	}
	if got := exitCode(nil); got != 0 {
		t.Errorf("nil error code = %d", got)
	}
}

func TestResolveSnapshot(t *testing.T) {
	now := time.Now()
	snapshots := []restic.Snapshot{
		{ID: "aaaa1111", ShortID: "aaaa", Hostname: "node-1", Time: now.Add(-time.Hour)},
		{ID: "bbbb2222", ShortID: "bbbb", Hostname: "node-1", Time: now},
		{ID: "cccc3333", ShortID: "cccc", Hostname: "node-2", Time: now.Add(time.Hour)},
	}

	latest, err := resolveSnapshot(snapshots, "latest", "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != "bbbb2222" {
		t.Errorf("latest = %q", latest.ID)
	}

	byPrefix, err := resolveSnapshot(snapshots, "aaaa", "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if byPrefix.ID != "aaaa1111" {
		t.Errorf("prefix = %q", byPrefix.ID)
	}

	if _, err := resolveSnapshot(snapshots, "cccc", "node-1"); err == nil {
		t.Error("snapshot from another host must not resolve")
	}
	if _, err := resolveSnapshot(snapshots, "dddd", "node-1"); err == nil {
		t.Error("missing snapshot must not resolve")
	}
}

func TestVerifySkipReason(t *testing.T) {
	if reason := verifySkipReason(11_000_000_000, 0, 10_000_000_000, 0); reason == "" {
		t.Error("oversized snapshot must be skipped")
	}
	if reason := verifySkipReason(1_000_000_000, 5_000_000_000, 10_000_000_000, 20_000_000_000); reason == "" {
		t.Error("low free space must be skipped")
	}
	if reason := verifySkipReason(1_000_000_000, 50_000_000_000, 10_000_000_000, 20_000_000_000); reason != "" {
		t.Errorf("unexpected skip: %s", reason)
	}
}
