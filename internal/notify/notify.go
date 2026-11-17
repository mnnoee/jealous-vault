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
	"strconv"
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
	ErrorSummary    string    `json:"error_summary,omitempty"`
	ErrorClass      string    `json:"error_class,omitempty"`
	ErrorCode       int       `json:"error_code,omitempty"`
	ErrorDetail     string    `json:"error_detail,omitempty"`
	Action          string    `json:"action,omitempty"`
	Suppressed      int       `json:"suppressed,omitempty"`
	Time            time.Time `json:"time"`
}

// Options configures a Notifier.
type Options struct {
	Webhooks         []string
	PingURL          string
	FreshnessPingURL string
	DedupFile        string
	Now              func() time.Time
	Email            EmailOptions
	TelegramToken    string
	TelegramChatID   string
	TelegramChatIDs  []string
	TelegramAPIBase  string
	NotifyOnSuccess  bool
	Timeout          time.Duration
	Client           *http.Client
	Logger           *slog.Logger
}

// Notifier sends events to every configured channel.
type Notifier struct {
	webhooks         []string
	pingURL          string
	freshnessPingURL string
	telegramToken    string
	telegramChats    []telegramChat
	telegramAPIBase  string
	notifyOnSuccess  bool
	timeout          time.Duration
	client           *http.Client
	log              *slog.Logger
	limiter          *limiter
	email            *emailSender
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
	notifier := &Notifier{
		webhooks:         append([]string(nil), o.Webhooks...),
		pingURL:          o.PingURL,
		freshnessPingURL: o.FreshnessPingURL,
		telegramToken:    o.TelegramToken,
		telegramChats:    parseTelegramChats(o.TelegramChatID, o.TelegramChatIDs, log),
		telegramAPIBase:  firstNonEmptyString(o.TelegramAPIBase, "https://api.telegram.org"),
		notifyOnSuccess:  o.NotifyOnSuccess,
		timeout:          timeout,
		client:           client,
		log:              log,
		limiter:          newLimiter(o.DedupFile, o.Now),
	}
	if o.Email.Enabled {
		notifier.email = newEmailSender(o.Email)
	}
	return notifier
}

// Send delivers the event and joins the errors from all channels.
func (n *Notifier) Send(ctx context.Context, ev Event) error {
	ev.Normalize()

	notify := ev.Status != statusSuccess || n.notifyOnSuccess || len(ev.Warnings) > 0

	if notify && ev.Status != statusSuccess {
		allowed, suppressed, err := n.limiter.Allow(ev)
		if err != nil {
			n.log.Warn("alert deduplication failed", "error", err)
		}
		if !allowed {
			n.log.Info("alert suppressed", "event", ev.Event, "suppressed", suppressed)
			return n.sendPing(ctx, ev)
		}
		ev.Suppressed = suppressed
	}

	var channelErrs []error
	sent := false
	if notify {
		for _, hook := range n.webhooks {
			if err := n.postJSON(ctx, hook, ev); err != nil {
				channelErrs = append(channelErrs, fmt.Errorf("webhook %s: %w", redactEndpoint(hook), err))
			} else {
				sent = true
			}
		}
		if n.telegramToken != "" && len(n.telegramChats) > 0 {
			if err := n.telegram(ctx, ev); err != nil {
				channelErrs = append(channelErrs, fmt.Errorf("telegram: %w", err))
			} else {
				sent = true
			}
		}
		if n.email != nil {
			if err := n.email.Send(ctx, ev); err != nil {
				channelErrs = append(channelErrs, fmt.Errorf("email: %w", err))
			} else {
				sent = true
			}
		}
	}
	if notify && ev.Status != statusSuccess && n.hasChannels() && (sent || len(channelErrs) == 0) {
		if err := n.limiter.MarkSent(ev); err != nil {
			n.log.Warn("cannot save alert deduplication state", "error", err)
		}
	}

	pingErr := n.sendPing(ctx, ev)
	return errors.Join(append(channelErrs, pingErr)...)
}

func (n *Notifier) hasChannels() bool {
	return len(n.webhooks) > 0 || (n.telegramToken != "" && len(n.telegramChats) > 0) || n.email != nil
}

// CheckEmail verifies the email channel configuration and connection.
func (n *Notifier) CheckEmail(ctx context.Context) error {
	if n.email == nil {
		return errors.New("email channel is not enabled")
	}
	return n.email.Check(ctx)
}

// TestEmail sends a test email.
func (n *Notifier) TestEmail(ctx context.Context) error {
	if n.email == nil {
		return errors.New("email channel is not enabled")
	}
	return n.email.Test(ctx)
}

func (n *Notifier) sendPing(ctx context.Context, ev Event) error {
	endpoint := n.pingEndpoint(ev.Event)
	if endpoint == "" {
		return nil
	}
	if err := n.ping(ctx, endpoint, ev); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
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
			return errors.New("invalid webhook endpoint")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "jealous-vault")

		resp, err := n.client.Do(req)
		if err != nil {
			return redactHTTPError(err)
		}
		defer resp.Body.Close()
		return responseError(resp)
	})
}

func (n *Notifier) telegram(ctx context.Context, ev Event) error {
	var errs []error
	for _, chat := range n.telegramChats {
		form := url.Values{}
		form.Set("chat_id", chat.ID)
		if chat.ThreadID > 0 {
			form.Set("message_thread_id", strconv.Itoa(chat.ThreadID))
		}
		form.Set("text", truncate(ev.Text(), 4000))
		form.Set("disable_web_page_preview", "true")
		endpoint := n.telegramAPIBase + "/bot" + n.telegramToken + "/sendMessage"

		err := n.withRetry(ctx, func() error {
			reqCtx, cancel := context.WithTimeout(ctx, n.timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint,
				strings.NewReader(form.Encode()))
			if err != nil {
				return errors.New("invalid telegram endpoint")
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("User-Agent", "jealous-vault")

			resp, err := n.client.Do(req)
			if err != nil {
				return redactHTTPError(err)
			}
			defer resp.Body.Close()
			return responseError(resp)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("chat %s: %w", chat.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (n *Notifier) pingEndpoint(event string) string {
	switch event {
	case eventBackup:
		return n.pingURL
	case "freshness":
		return n.freshnessPingURL
	default:
		return ""
	}
}

func (n *Notifier) ping(ctx context.Context, base string, ev Event) error {
	endpoint := base
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
			return errors.New("invalid ping endpoint")
		}
		req.Header.Set("User-Agent", "jealous-vault")

		resp, err := n.client.Do(req)
		if err != nil {
			return redactHTTPError(err)
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
