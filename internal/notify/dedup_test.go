// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLimiterCustomCooldown(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	l := newLimiter(filepath.Join(t.TempDir(), "alerts.json"), func() time.Time { return now }, 10*time.Minute, 2*time.Hour)
	ev := Event{Host: "node-1", Event: "backup", Status: "failed", Error: "s3 auth failed"}

	if allowed, _, _ := l.Allow(ev); !allowed {
		t.Fatal("first alert was suppressed")
	}
	if err := l.MarkSent(ev); err != nil {
		t.Fatal(err)
	}
	if allowed, _, _ := l.Allow(ev); allowed {
		t.Fatal("alert inside the custom cooldown was allowed")
	}
	now = now.Add(10*time.Minute + time.Second)
	if allowed, _, _ := l.Allow(ev); !allowed {
		t.Fatal("alert after the custom cooldown was suppressed")
	}
}

func TestLimiterDisabled(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	l := newLimiter(filepath.Join(t.TempDir(), "alerts.json"), func() time.Time { return now }, 0, 0)
	ev := Event{Host: "node-1", Event: "backup", Status: "failed", Error: "s3 auth failed"}

	if err := l.MarkSent(ev); err != nil {
		t.Fatal(err)
	}
	if allowed, suppressed, _ := l.Allow(ev); !allowed || suppressed != 0 {
		t.Fatalf("dedup disabled but alert was suppressed: allowed=%v suppressed=%d", allowed, suppressed)
	}
}

func TestLimiterCooldownAndSuppressed(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	l := newLimiter(filepath.Join(t.TempDir(), "alerts.json"), func() time.Time { return now }, 3*time.Hour, 6*time.Hour)
	ev := Event{Host: "node-1", Event: "backup", Status: "failed", Error: "s3 auth failed"}

	allowed, suppressed, err := l.Allow(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed || suppressed != 0 {
		t.Fatalf("first alert: allowed=%v suppressed=%d", allowed, suppressed)
	}
	if err := l.MarkSent(ev); err != nil {
		t.Fatal(err)
	}

	allowed, suppressed, err = l.Allow(ev)
	if err != nil {
		t.Fatal(err)
	}
	if allowed || suppressed != 1 {
		t.Fatalf("second alert: allowed=%v suppressed=%d", allowed, suppressed)
	}

	allowed, suppressed, err = l.Allow(ev)
	if err != nil {
		t.Fatal(err)
	}
	if allowed || suppressed != 2 {
		t.Fatalf("third alert: allowed=%v suppressed=%d", allowed, suppressed)
	}

	now = now.Add(3*time.Hour + time.Second)
	allowed, suppressed, err = l.Allow(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed || suppressed != 2 {
		t.Fatalf("after cooldown: allowed=%v suppressed=%d", allowed, suppressed)
	}

	if err := l.MarkSent(ev); err != nil {
		t.Fatal(err)
	}
	allowed, suppressed, err = l.Allow(ev)
	if err != nil {
		t.Fatal(err)
	}
	if allowed || suppressed != 1 {
		t.Fatalf("after reset: allowed=%v suppressed=%d", allowed, suppressed)
	}
}

func TestLimiterNewErrorSendsImmediately(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	l := newLimiter(filepath.Join(t.TempDir(), "alerts.json"), func() time.Time { return now }, 3*time.Hour, 6*time.Hour)

	first := Event{Host: "node-1", Event: "backup", Status: "failed", Error: "s3 auth failed"}
	if allowed, _, _ := l.Allow(first); !allowed {
		t.Fatal("first alert must be allowed")
	}
	_ = l.MarkSent(first)

	second := Event{Host: "node-1", Event: "backup", Status: "failed", Error: "s3 timeout"}
	allowed, suppressed, err := l.Allow(second)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed || suppressed != 0 {
		t.Fatalf("new error hash must pass: allowed=%v suppressed=%d", allowed, suppressed)
	}
}

func TestNotifierDeduplicatesAndReportsSuppressed(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	srv, requests := recorder(t, http.StatusOK)
	notifier := New(Options{
		Webhooks:       []string{srv.URL + "/hook"},
		DedupFile:      filepath.Join(t.TempDir(), "alerts.json"),
		DedupBackup:    3 * time.Hour,
		DedupFreshness: 6 * time.Hour,
		Now:            func() time.Time { return now },
		Timeout:        time.Second,
	})

	ev := Event{Event: "backup", Status: "failed", Host: "node-1", Error: "disk full", Time: now}
	if err := notifier.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(got) != 1 {
		t.Fatalf("first send requests = %d", len(got))
	}

	if err := notifier.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(got) != 1 {
		t.Fatalf("suppressed send made a request: %d", len(got))
	}

	now = now.Add(3*time.Hour + time.Second)
	if err := notifier.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	got := requests()
	if len(got) != 2 {
		t.Fatalf("after cooldown requests = %d", len(got))
	}
	var decoded Event
	if err := json.Unmarshal([]byte(got[1].body), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Suppressed != 1 {
		t.Fatalf("suppressed field = %d, want 1", decoded.Suppressed)
	}
}

func TestSuccessIsNeverDeduplicated(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	srv, requests := recorder(t, http.StatusOK)
	notifier := New(Options{
		Webhooks:        []string{srv.URL + "/hook"},
		DedupFile:       filepath.Join(t.TempDir(), "alerts.json"),
		DedupBackup:     3 * time.Hour,
		DedupFreshness:  6 * time.Hour,
		Now:             func() time.Time { return now },
		NotifyOnSuccess: true,
		Timeout:         time.Second,
	})

	failure := Event{Event: "backup", Status: "failed", Host: "node-1", Error: "disk full", Time: now}
	if err := notifier.Send(context.Background(), failure); err != nil {
		t.Fatal(err)
	}
	if err := notifier.Send(context.Background(), failure); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(got) != 1 {
		t.Fatalf("failure was not deduplicated: %d", len(got))
	}

	success := Event{Event: "backup", Status: "success", Host: "node-1", Time: now}
	if err := notifier.Send(context.Background(), success); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(got) != 2 {
		t.Fatalf("success was deduplicated: %d", len(got))
	}
}

func TestSuppressedAlertStillPings(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	srv, requests := recorder(t, http.StatusOK)
	notifier := New(Options{
		Webhooks:       []string{srv.URL + "/hook"},
		PingURL:        srv.URL + "/ping",
		DedupFile:      filepath.Join(t.TempDir(), "alerts.json"),
		DedupBackup:    3 * time.Hour,
		DedupFreshness: 6 * time.Hour,
		Now:            func() time.Time { return now },
		Timeout:        time.Second,
	})

	ev := Event{Event: "backup", Status: "failed", Host: "node-1", Error: "disk full", Time: now}
	if err := notifier.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if err := notifier.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	var hooks, pings int
	for _, r := range requests() {
		switch r.path {
		case "/hook":
			hooks++
		case "/ping/fail":
			pings++
		}
	}
	if hooks != 1 {
		t.Errorf("webhook calls = %d, want 1", hooks)
	}
	if pings != 2 {
		t.Errorf("ping calls = %d, want 2", pings)
	}
}

func TestDedupMarksWhenOneChannelSucceeds(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	srv, requests := recorder(t, http.StatusBadRequest)

	dir := t.TempDir()
	messageFile := filepath.Join(dir, "message")
	sendmail := filepath.Join(dir, "sendmail")
	script := "#!/bin/sh\ncat >> " + messageFile + "\n"
	if err := os.WriteFile(sendmail, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	notifier := New(Options{
		Webhooks:       []string{srv.URL + "/hook"},
		DedupFile:      filepath.Join(dir, "alerts.json"),
		DedupBackup:    3 * time.Hour,
		DedupFreshness: 6 * time.Hour,
		Now:            func() time.Time { return now },
		Timeout:        time.Second,
		Email: EmailOptions{
			Enabled:  true,
			Mode:     "local",
			From:     "backups@example.com",
			To:       []string{"admin@example.com"},
			Sendmail: sendmail,
			Timeout:  time.Second,
		},
	})

	ev := Event{Event: "backup", Status: "failed", Host: "node-1", Error: "disk full", Time: now}
	if err := notifier.Send(context.Background(), ev); err == nil {
		t.Fatal("expected the webhook error")
	}
	if err := notifier.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	if got := requests(); len(got) != 1 {
		t.Fatalf("webhook calls = %d, want 1", len(got))
	}
	data, err := os.ReadFile(messageFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "Subject: [JealousVault]"); got != 1 {
		t.Fatalf("email messages = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "alerts.json")); err != nil {
		t.Fatalf("dedup state was not saved: %v", err)
	}
}
