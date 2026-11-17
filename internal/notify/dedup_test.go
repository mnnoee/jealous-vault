// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestLimiterCooldownAndSuppressed(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	l := newLimiter(filepath.Join(t.TempDir(), "alerts.json"), func() time.Time { return now })
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
	l := newLimiter(filepath.Join(t.TempDir(), "alerts.json"), func() time.Time { return now })

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
		Webhooks:  []string{srv.URL + "/hook"},
		DedupFile: filepath.Join(t.TempDir(), "alerts.json"),
		Now:       func() time.Time { return now },
		Timeout:   time.Second,
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
		Webhooks:  []string{srv.URL + "/hook"},
		PingURL:   srv.URL + "/ping",
		DedupFile: filepath.Join(t.TempDir(), "alerts.json"),
		Now:       func() time.Time { return now },
		Timeout:   time.Second,
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
