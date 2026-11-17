// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestRedactEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://api.telegram.org/bot123:SECRET/sendMessage": "https://api.telegram.org/***",
		"https://hooks.slack.com/services/T/B/SECRET":        "https://hooks.slack.com/***",
		"http://127.0.0.1:1/hook?token=SECRET":               "http://127.0.0.1:1/***",
		"local:/srv/repo":                                    "endpoint",
		"://bad":                                             "endpoint",
	}
	for in, want := range cases {
		if got := redactEndpoint(in); got != want {
			t.Errorf("redactEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func failingClient() *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, permanentError{errors.New("boom")}
		}),
	}
}

func TestWebhookErrorsDoNotLeakSecrets(t *testing.T) {
	n := New(Options{
		Webhooks: []string{"https://hooks.example.com/services/T000/B000/SUPERSECRET"},
		Client:   failingClient(),
		Timeout:  time.Second,
	})
	err := n.Send(context.Background(), Event{
		Event: "backup", Status: "failed", Host: "node-1", Error: "disk full", Time: time.Now(),
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	text := err.Error()
	if strings.Contains(text, "SUPERSECRET") {
		t.Fatalf("webhook secret leaked into the error: %s", text)
	}
	if !strings.Contains(text, "https://hooks.example.com/***") {
		t.Fatalf("redacted endpoint missing from the error: %s", text)
	}
}

func TestTelegramErrorsDoNotLeakTheToken(t *testing.T) {
	n := New(Options{
		TelegramToken:   "123456:AAHsecretTOKEN",
		TelegramChatID:  "1",
		TelegramAPIBase: "https://api.telegram.org",
		Client:          failingClient(),
		Timeout:         time.Second,
	})
	err := n.Send(context.Background(), Event{
		Event: "backup", Status: "failed", Host: "node-1", Error: "disk full", Time: time.Now(),
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	text := err.Error()
	if strings.Contains(text, "AAHsecretTOKEN") {
		t.Fatalf("telegram bot token leaked into the error: %s", text)
	}
	if !strings.Contains(text, "https://api.telegram.org/***") {
		t.Fatalf("redacted endpoint missing from the error: %s", text)
	}
}

func TestPingErrorsDoNotLeakSecrets(t *testing.T) {
	n := New(Options{
		PingURL: "https://hc.example.com/ping/SECRETUUID",
		Client:  failingClient(),
		Timeout: time.Second,
	})
	err := n.Send(context.Background(), Event{
		Event: "backup", Status: "success", Host: "node-1", Time: time.Now(),
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	text := err.Error()
	if strings.Contains(text, "SECRETUUID") {
		t.Fatalf("ping secret leaked into the error: %s", text)
	}
	if !strings.Contains(text, "https://hc.example.com/***") {
		t.Fatalf("redacted endpoint missing from the error: %s", text)
	}
}
