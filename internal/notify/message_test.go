// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const resticMissingRepoError = `restic backup: exit status 10: {"message_type":"exit_error","code":10,"message":"Fatal: repository does not exist: unable to open config file: stat /root/jv-notify/missing1/config: no such file or directory\nIs there a repository at the following location?\nlocal:/root/jv-notify/missing1"}`

func TestNormalizeResticError(t *testing.T) {
	ev := Event{
		Event:  "backup",
		Status: "failed",
		Host:   "jv-vm",
		Error:  resticMissingRepoError,
		Time:   time.Date(2026, 11, 17, 16, 26, 0, 0, time.UTC),
	}
	ev.Normalize()

	if ev.ErrorCode != 10 {
		t.Errorf("code = %d", ev.ErrorCode)
	}
	if ev.ErrorClass != "repository.missing" {
		t.Errorf("class = %q", ev.ErrorClass)
	}
	if !strings.Contains(ev.ErrorSummary, "repository does not exist") {
		t.Errorf("summary = %q", ev.ErrorSummary)
	}
	if ev.Action == "" {
		t.Error("action is empty")
	}

	text := ev.Text()
	for _, want := range []string{"Host:", "Time:", "Error:", "Class:     repository.missing", "Code:      10", "Detail:", "Action:"} {
		if !strings.Contains(text, want) {
			t.Errorf("text does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"message_type"`) {
		t.Errorf("text still contains raw restic JSON:\n%s", text)
	}
}

func TestTelegramHTMLFormatting(t *testing.T) {
	ev := Event{
		Event:           "backup",
		Status:          "failed",
		Host:            "node<1>",
		Time:            time.Date(2026, 11, 17, 16, 26, 0, 0, time.UTC),
		DurationSeconds: 12,
		Error:           resticMissingRepoError,
		Warnings:        []string{"retention failed: <bad>"},
	}
	text, mode := ev.TelegramText()
	if mode != "HTML" {
		t.Fatalf("mode = %q", mode)
	}
	for _, want := range []string{
		"<b>node&lt;1&gt;</b>",
		"<b>2026-11-17 16:26:00 UTC</b>",
		"<b>12s</b>",
		"<pre>",
		"<code>repository.missing</code>",
		"<code>10</code>",
		"retention failed: &lt;bad&gt;",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("telegram text does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<bad>") {
		t.Errorf("untrusted HTML was not escaped:\n%s", text)
	}
}

func TestDedupGroupsByErrorClass(t *testing.T) {
	now := time.Date(2026, 11, 17, 16, 26, 0, 0, time.UTC)
	l := newLimiter(filepath.Join(t.TempDir(), "alerts.json"), func() time.Time { return now }, 3*time.Hour, 6*time.Hour)

	first := Event{Event: "backup", Status: "failed", Host: "jv-vm", Error: resticMissingRepoError}
	first.Normalize()
	second := Event{Event: "backup", Status: "failed", Host: "jv-vm", Error: strings.ReplaceAll(resticMissingRepoError, "missing1", "missing2")}
	second.Normalize()

	allowed, _, err := l.Allow(first)
	if err != nil || !allowed {
		t.Fatalf("first alert: allowed=%v err=%v", allowed, err)
	}
	if err := l.MarkSent(first); err != nil {
		t.Fatal(err)
	}
	allowed, suppressed, err := l.Allow(second)
	if err != nil {
		t.Fatal(err)
	}
	if allowed || suppressed != 1 {
		t.Fatalf("same error class must be deduplicated: allowed=%v suppressed=%d", allowed, suppressed)
	}
}
