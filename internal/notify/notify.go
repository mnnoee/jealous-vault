// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package notify delivers outbound alerts. It never listens for inbound
// traffic: the agent stays a client even when it reports a failure.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	statusSuccess = "success"
	eventBackup   = "backup"
)

// Event is what a notification channel receives.
type Event struct {
	Event           string    `json:"event"`
	Status          string    `json:"status"`
	Host            string    `json:"host"`
	Snapshot        string    `json:"snapshot,omitempty"`
	FilesNew        int       `json:"files_new,omitempty"`
	FilesChanged    int       `json:"files_changed,omitempty"`
	BytesAdded      int64     `json:"bytes_added,omitempty"`
	DurationSeconds float64   `json:"duration_seconds"`
	Warnings        []string  `json:"warnings,omitempty"`
	Error           string    `json:"error,omitempty"`
	Time            time.Time `json:"time"`
}

// Options configures a Notifier.
type Options struct {
	Webhooks        []string
	PingURL         string
	TelegramToken   string
	TelegramChatID  string
	NotifyOnSuccess bool
	Timeout         time.Duration
	Client          *http.Client
	Logger          *slog.Logger
}

// Notifier sends events to every configured channel.
type Notifier struct {
	webhooks        []string
	pingURL         string
	telegramToken   string
	telegramChatID  string
	notifyOnSuccess bool
	timeout         time.Duration
	client          *http.Client
	log             *slog.Logger
}

// New returns a Notifier. A nil logger discards messages.
func New(o Options) *Notifier {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := o.Client
	if client == nil {
		client = &http.Client{}
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Notifier{
		webhooks:        append([]string(nil), o.Webhooks...),
		pingURL:         o.PingURL,
		telegramToken:   o.TelegramToken,
		telegramChatID:  o.TelegramChatID,
		notifyOnSuccess: o.NotifyOnSuccess,
		timeout:         timeout,
		client:          client,
		log:             log,
	}
}

// Send delivers the event and joins the errors from all channels.
func (n *Notifier) Send(ctx context.Context, ev Event) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}

	notify := ev.Status != statusSuccess || n.notifyOnSuccess || len(ev.Warnings) > 0
	var errs []error
	if notify {
		for _, hook := range n.webhooks {
			if err := n.postJSON(ctx, hook, ev); err != nil {
				errs = append(errs, fmt.Errorf("webhook %s: %w", hook, err))
			}
		}
		if n.telegramToken != "" {
			if err := n.telegram(ctx, ev); err != nil {
				errs = append(errs, fmt.Errorf("telegram: %w", err))
			}
		}
	}

	if n.pingURL != "" && ev.Event == eventBackup {
		if err := n.ping(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("ping: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (n *Notifier) postJSON(ctx context.Context, endpoint string, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return n.withRetry(ctx, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, n.timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "jealous-vault")

		resp, err := n.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return responseError(resp)
	})
}

func (n *Notifier) telegram(ctx context.Context, ev Event) error {
	endpoint := "https://api.telegram.org/bot" + n.telegramToken + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", n.telegramChatID)
	form.Set("text", truncate(ev.Text(), 4000))
	form.Set("disable_web_page_preview", "true")

	return n.withRetry(ctx, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, n.timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint,
			strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "jealous-vault")

		resp, err := n.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return responseError(resp)
	})
}

func (n *Notifier) ping(ctx context.Context, ev Event) error {
	endpoint := n.pingURL
	if ev.Status != statusSuccess {
		if u, err := url.Parse(endpoint); err == nil {
			u.Path = strings.TrimRight(u.Path, "/") + "/fail"
			endpoint = u.String()
		} else {
			endpoint = strings.TrimRight(endpoint, "/") + "/fail"
		}
	}
	return n.withRetry(ctx, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, n.timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "jealous-vault")

		resp, err := n.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return responseError(resp)
	})
}

func (n *Notifier) withRetry(ctx context.Context, fn func() error) error {
	const attempts = 3
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			delay := time.Duration(1<<(i-1)) * time.Second
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err = fn(); err == nil {
			return nil
		}
		var permanent permanentError
		if errors.As(err, &permanent) {
			return err
		}
		n.log.Warn("notification attempt failed", "attempt", i+1, "error", err)
	}
	return err
}

type permanentError struct {
	err error
}

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	err := fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return permanentError{err: err}
	}
	return err
}

// Text renders a short human readable message for chat channels.
func (e Event) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[JealousVault] %s %s on %s", e.Event, e.Status, e.Host)
	if e.Snapshot != "" {
		fmt.Fprintf(&b, ", snapshot %s", e.Snapshot)
	}
	if e.BytesAdded > 0 {
		fmt.Fprintf(&b, ", added %s", humanBytes(e.BytesAdded))
	}
	if e.DurationSeconds > 0 {
		fmt.Fprintf(&b, ", took %s", time.Duration(e.DurationSeconds*float64(time.Second)).Round(time.Second))
	}
	if e.Error != "" {
		fmt.Fprintf(&b, "\nerror: %s", e.Error)
	}
	for _, w := range e.Warnings {
		fmt.Fprintf(&b, "\nwarning: %s", w)
	}
	return b.String()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
