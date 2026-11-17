// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorded struct {
	path string
	body string
}

func recorder(t *testing.T, status int) (*httptest.Server, func() []recorded) {
	t.Helper()
	var mu sync.Mutex
	var requests []recorded

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, recorded{path: r.URL.Path, body: string(body)})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	return srv, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), requests...)
	}
}

func TestSuccessPingsWithoutAlert(t *testing.T) {
	srv, requests := recorder(t, http.StatusOK)
	n := New(Options{
		Webhooks: []string{srv.URL + "/hook"},
		PingURL:  srv.URL + "/ping",
		Timeout:  time.Second,
	})

	ev := Event{Event: "backup", Status: "success", Host: "node-1", Time: time.Now()}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	got := requests()
	if len(got) != 1 {
		t.Fatalf("got %d requests: %+v", len(got), got)
	}
	if got[0].path != "/ping" {
		t.Errorf("path = %q, want /ping", got[0].path)
	}
}

func TestFailureSendsAlertAndFailPing(t *testing.T) {
	srv, requests := recorder(t, http.StatusOK)
	n := New(Options{
		Webhooks: []string{srv.URL + "/hook"},
		PingURL:  srv.URL + "/ping",
		Timeout:  time.Second,
	})

	ev := Event{Event: "backup", Status: "failed", Host: "node-1", Error: "disk full", Time: time.Now()}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	got := requests()
	if len(got) != 2 {
		t.Fatalf("got %d requests: %+v", len(got), got)
	}
	var hook, ping bool
	for _, r := range got {
		switch r.path {
		case "/hook":
			hook = true
			var decoded Event
			if err := json.Unmarshal([]byte(r.body), &decoded); err != nil {
				t.Fatalf("webhook body is not JSON: %v", err)
			}
			if decoded.Status != "failed" || decoded.Error != "disk full" {
				t.Errorf("webhook event = %+v", decoded)
			}
		case "/ping/fail":
			ping = true
		}
	}
	if !hook || !ping {
		t.Fatalf("missing channels: hook=%v ping=%v", hook, ping)
	}
}

func TestSuccessAlertWhenRequested(t *testing.T) {
	srv, requests := recorder(t, http.StatusOK)
	n := New(Options{
		Webhooks:        []string{srv.URL + "/hook"},
		NotifyOnSuccess: true,
		Timeout:         time.Second,
	})

	ev := Event{Event: "backup", Status: "success", Host: "node-1", Snapshot: "abc", Time: time.Now()}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(got) != 1 || got[0].path != "/hook" {
		t.Fatalf("requests = %+v", got)
	}
}

func TestWarningsTriggerAlert(t *testing.T) {
	srv, requests := recorder(t, http.StatusOK)
	n := New(Options{
		Webhooks: []string{srv.URL + "/hook"},
		Timeout:  time.Second,
	})

	ev := Event{
		Event:    "backup",
		Status:   "success",
		Host:     "node-1",
		Warnings: []string{"retention failed"},
		Time:     time.Now(),
	}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(got) != 1 || got[0].path != "/hook" {
		t.Fatalf("requests = %+v", got)
	}
}

func TestClientErrorIsNotRetried(t *testing.T) {
	srv, requests := recorder(t, http.StatusBadRequest)
	n := New(Options{
		Webhooks: []string{srv.URL + "/hook"},
		Timeout:  time.Second,
	})

	ev := Event{Event: "backup", Status: "failed", Host: "node-1", Time: time.Now()}
	if err := n.Send(context.Background(), ev); err == nil {
		t.Fatal("expected an error")
	}
	if got := requests(); len(got) != 1 {
		t.Fatalf("got %d requests, want 1", len(got))
	}
}

func TestText(t *testing.T) {
	ev := Event{
		Event:           "backup",
		Status:          "failed",
		Host:            "node-1",
		BytesAdded:      2048,
		Error:           "disk full",
		DurationSeconds: 12,
	}
	text := ev.Text()
	for _, want := range []string{"backup failed", "node-1", "2.0 KiB", "disk full", "12s"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q does not contain %q", text, want)
		}
	}
}
