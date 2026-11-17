// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"testing"
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
