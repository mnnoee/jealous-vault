// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
)

// Normalize fills structured error fields from the raw error text.
func (e *Event) Normalize() {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Error == "" {
		return
	}
	summary, class, detail, action, code := classifyError(e.Error, e.Event)
	e.ErrorSummary = summary
	e.ErrorClass = class
	e.ErrorDetail = detail
	e.Action = action
	e.ErrorCode = code
}

// Text renders a structured message for chat and plain text email.
func (e Event) Text() string {
	if e.Error != "" && e.ErrorSummary == "" {
		e.Normalize()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[JealousVault] %s %s on %s\n\n", e.Event, e.Status, e.Host)

	field := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%-10s %s\n", label+":", value)
		}
	}
	field("Host", e.Host)
	if !e.Time.IsZero() {
		field("Time", e.Time.Format("2006-01-02 15:04:05 MST"))
	}
	if e.DurationSeconds > 0 {
		field("Duration", time.Duration(e.DurationSeconds*float64(time.Second)).Round(time.Second).String())
	}
	field("Snapshot", shortID(e.Snapshot))
	if e.FilesNew > 0 || e.FilesChanged > 0 {
		field("Files", fmt.Sprintf("%d new, %d changed", e.FilesNew, e.FilesChanged))
	}
	if e.BytesAdded > 0 {
		field("Added", humanBytes(e.BytesAdded))
	}

	if e.ErrorSummary != "" {
		b.WriteString("\n")
		field("Error", e.ErrorSummary)
		field("Class", e.ErrorClass)
		if e.ErrorCode != 0 {
			field("Code", strconv.Itoa(e.ErrorCode))
		}
		field("Detail", truncate(e.ErrorDetail, 1200))
		field("Action", e.Action)
	} else if e.Error != "" {
		b.WriteString("\n")
		field("Error", truncate(e.Error, 1200))
	}

	if len(e.Warnings) > 0 {
		b.WriteString("\n")
		for _, warning := range e.Warnings {
			field("Warning", warning)
		}
	}
	if e.Suppressed > 0 {
		fmt.Fprintf(&b, "\nSuppressed: %d earlier alerts\n", e.Suppressed)
	}
	return strings.TrimSpace(b.String())
}

// TelegramText renders the event for Telegram's HTML parse mode. It returns
// the text and the parse mode; when the marked-up message is too long it falls
// back to plain text so Telegram never sees a broken HTML tag.
func (e Event) TelegramText() (string, string) {
	if e.Error != "" && e.ErrorSummary == "" {
		e.Normalize()
	}
	esc := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, "[JealousVault] %s %s on <b>%s</b>\n\n", esc(e.Event), esc(e.Status), esc(e.Host))

	field := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, value)
		}
	}
	field("Host", "<b>"+esc(e.Host)+"</b>")
	if !e.Time.IsZero() {
		field("Time", "<b>"+esc(e.Time.Format("2006-01-02 15:04:05 MST"))+"</b>")
	}
	if e.DurationSeconds > 0 {
		field("Duration", "<b>"+esc(time.Duration(e.DurationSeconds*float64(time.Second)).Round(time.Second).String())+"</b>")
	}
	field("Snapshot", esc(shortID(e.Snapshot)))
	if e.FilesNew > 0 || e.FilesChanged > 0 {
		field("Files", esc(fmt.Sprintf("%d new, %d changed", e.FilesNew, e.FilesChanged)))
	}
	if e.BytesAdded > 0 {
		field("Added", esc(humanBytes(e.BytesAdded)))
	}

	if e.ErrorSummary != "" {
		fmt.Fprintf(&b, "\nError:\n<pre>%s</pre>\n", esc(truncate(e.ErrorSummary, 1200)))
		field("Class", "<code>"+esc(e.ErrorClass)+"</code>")
		if e.ErrorCode != 0 {
			field("Code", "<code>"+esc(strconv.Itoa(e.ErrorCode))+"</code>")
		}
		field("Detail", "<code>"+esc(truncate(e.ErrorDetail, 1200))+"</code>")
		field("Action", esc(e.Action))
	} else if e.Error != "" {
		fmt.Fprintf(&b, "\nError:\n<pre>%s</pre>\n", esc(truncate(e.Error, 1200)))
	}

	if len(e.Warnings) > 0 {
		b.WriteString("\n")
		for _, warning := range e.Warnings {
			fmt.Fprintf(&b, "Warning:\n<pre>%s</pre>\n", esc(truncate(warning, 800)))
		}
	}
	if e.Suppressed > 0 {
		fmt.Fprintf(&b, "\nSuppressed: %d earlier alerts\n", e.Suppressed)
	}
	text := strings.TrimSpace(b.String())
	if len(text) > 4000 {
		return truncate(e.Text(), 4000), ""
	}
	return text, "HTML"
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:8] + "..."
}

func classifyError(raw, event string) (summary, class, detail, action string, code int) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", "", "", "", 0
	}

	if start := strings.Index(text, "{"); start >= 0 {
		var payload struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(text[start:]), &payload); err == nil && payload.Message != "" {
			code = payload.Code
			text = payload.Message
		}
	}
	text = stripExitStatus(text, &code)
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "unknown", "", defaultAction(event), code
	}

	lines := strings.SplitN(text, "\n", 2)
	summary = strings.TrimSpace(lines[0])
	detail = text
	if len(lines) > 1 {
		detail = strings.TrimSpace(lines[1])
	}

	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "repository does not exist"),
		strings.Contains(lower, "unable to open config file"):
		class = "repository.missing"
	case strings.Contains(lower, "wrong password"),
		strings.Contains(lower, "incorrect password"),
		strings.Contains(lower, "password is incorrect"),
		strings.Contains(lower, "no key found"):
		class = "repository.auth"
	case strings.Contains(lower, "already locked"),
		strings.Contains(lower, "create lock"):
		class = "repository.locked"
	case strings.Contains(lower, "no space left"):
		class = "disk.full"
	case strings.Contains(lower, "permission denied"):
		class = "permission"
	case strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "no such host"),
		strings.Contains(lower, "network is unreachable"),
		strings.Contains(lower, "tls handshake"),
		strings.Contains(lower, "i/o timeout"):
		class = "network"
	case strings.Contains(lower, "context deadline exceeded"),
		strings.Contains(lower, "timed out"):
		class = "timeout"
	default:
		class = "unknown"
	}

	action = actionFor(class, event)
	return summary, class, detail, action, code
}

func stripExitStatus(text string, code *int) string {
	const marker = "exit status "
	i := strings.Index(text, marker)
	if i < 0 {
		return text
	}
	rest := text[i+len(marker):]
	colon := strings.Index(rest, ": ")
	if colon < 0 {
		return text
	}
	if *code == 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(rest[:colon])); err == nil {
			*code = parsed
		}
	}
	return strings.TrimSpace(rest[colon+2:])
}

func actionFor(class, event string) string {
	switch class {
	case "repository.missing":
		return `check repository.url and credentials, then run "jealous-vault doctor"`
	case "repository.auth":
		return `check repository.password_file and its permissions`
	case "repository.locked":
		return `wait for the running job or run "jealous-vault repo unlock"`
	case "disk.full":
		return "free disk space on the host and retry"
	case "permission":
		return "check file permissions and the user running the agent"
	case "network":
		return "check network access to the repository"
	case "timeout":
		return "check repository latency and raise the configured timeout"
	}
	return defaultAction(event)
}

func defaultAction(event string) string {
	switch event {
	case "freshness":
		return `run "systemctl status jealous-vault-backup.timer" and "jealous-vault doctor"`
	case "verify":
		return `inspect the verification target and run "jealous-vault doctor"`
	case "copy":
		return "check the secondary repository and its password"
	default:
		return `run "jealous-vault doctor"`
	}
}
