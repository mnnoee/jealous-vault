// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/smtp"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/config"
)

//go:embed templates/email_success.html templates/email_failure.html
var emailTemplates embed.FS

var (
	emailSuccess = template.Must(template.ParseFS(emailTemplates, "templates/email_success.html"))
	emailFailure = template.Must(template.ParseFS(emailTemplates, "templates/email_failure.html"))
)

// EmailOptions is a resolved email configuration.
type EmailOptions struct {
	Enabled  bool
	Mode     string
	From     string
	To       []string
	Host     string
	Port     int
	Username string
	Password string
	Security string
	Timeout  time.Duration
	Sendmail string
}

// EmailOptionsFromConfig builds resolved options for the notifier.
func EmailOptionsFromConfig(cfg *config.Config) EmailOptions {
	e := cfg.Notify.Email
	timeout := cfg.EmailTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return EmailOptions{
		Enabled:  e.Enabled,
		Mode:     e.Mode,
		From:     e.From,
		To:       append([]string(nil), e.To...),
		Host:     e.Host,
		Port:     e.Port,
		Username: e.Username,
		Password: cfg.EmailPassword,
		Security: e.Security,
		Timeout:  timeout,
		Sendmail: "/usr/sbin/sendmail",
	}
}

type emailSender struct {
	opts EmailOptions
}

func newEmailSender(o EmailOptions) *emailSender {
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	if o.Sendmail == "" {
		o.Sendmail = "/usr/sbin/sendmail"
	}
	return &emailSender{opts: o}
}

func (s *emailSender) Send(ctx context.Context, ev Event) error {
	message, err := s.message(ev)
	if err != nil {
		return err
	}
	switch s.opts.Mode {
	case "local":
		return s.sendLocal(ctx, message)
	case "smtp":
		return s.sendSMTP(ctx, message)
	default:
		return fmt.Errorf("unknown email mode %q", s.opts.Mode)
	}
}

func (s *emailSender) sendLocal(ctx context.Context, message []byte) error {
	if path, err := exec.LookPath(s.opts.Sendmail); err == nil {
		sendCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
		defer cancel()
		cmd := exec.CommandContext(sendCtx, path, "-t", "-i")
		cmd.Stdin = bytes.NewReader(message)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("sendmail: %w: %s", err, strings.TrimSpace(output.String()))
		}
		return nil
	}
	conn, err := (&net.Dialer{Timeout: s.opts.Timeout}).DialContext(ctx, "tcp", "127.0.0.1:25")
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if s.opts.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.opts.Username, s.opts.Password, "localhost")); err != nil {
			return err
		}
	}
	return s.sendWithClient(client, message)
}

func (s *emailSender) sendSMTP(ctx context.Context, message []byte) error {
	client, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	return s.sendWithClient(client, message)
}

func (s *emailSender) sendWithClient(client *smtp.Client, message []byte) error {
	if err := client.Mail(s.opts.From); err != nil {
		return err
	}
	for _, address := range s.opts.To {
		if err := client.Rcpt(address); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (s *emailSender) connect(ctx context.Context) (*smtp.Client, error) {
	host := s.opts.Host
	port := s.opts.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: s.opts.Timeout}

	var conn net.Conn
	var err error
	if s.opts.Security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if s.opts.Security == "starttls" {
		if err := client.StartTLS(&tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			client.Close()
			return nil, err
		}
	}
	if s.opts.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.opts.Username, s.opts.Password, host)); err != nil {
			client.Close()
			return nil, err
		}
	}
	return client, nil
}

// Check verifies that the local MTA or SMTP relay is reachable.
func (s *emailSender) Check(ctx context.Context) error {
	if s.opts.Mode == "local" {
		if _, err := exec.LookPath(s.opts.Sendmail); err == nil {
			return nil
		}
		conn, err := (&net.Dialer{Timeout: s.opts.Timeout}).DialContext(ctx, "tcp", "127.0.0.1:25")
		if err != nil {
			return fmt.Errorf("no sendmail and localhost:25 is not reachable: %w", err)
		}
		client, err := smtp.NewClient(conn, "localhost")
		if err != nil {
			conn.Close()
			return err
		}
		defer client.Close()
		if s.opts.Username != "" {
			if err := client.Auth(smtp.PlainAuth("", s.opts.Username, s.opts.Password, "localhost")); err != nil {
				return err
			}
		}
		return client.Quit()
	}
	client, err := s.connect(ctx)
	if err != nil {
		return err
	}
	return client.Quit()
}

// Test sends a test email.
// CheckEmail verifies an email configuration without sending a message.
func CheckEmail(ctx context.Context, o EmailOptions) error {
	if !o.Enabled {
		return errors.New("email channel is not enabled")
	}
	return newEmailSender(o).Check(ctx)
}

// SendTestEmail sends one test message.
func SendTestEmail(ctx context.Context, o EmailOptions) error {
	if !o.Enabled {
		return errors.New("email channel is not enabled")
	}
	return newEmailSender(o).Test(ctx)
}

func (s *emailSender) Test(ctx context.Context) error {
	event := Event{
		Event:  "test",
		Status: "success",
		Host:   "jealous-vault",
		Time:   time.Now(),
	}
	return s.Send(ctx, event)
}

func (s *emailSender) message(ev Event) ([]byte, error) {
	ev.Normalize()
	data := struct {
		Event      string
		Status     string
		Host       string
		Time       string
		Duration   string
		Snapshot   string
		Files      int
		Bytes      string
		Error      string
		Summary    string
		Class      string
		Code       int
		Detail     string
		Action     string
		Warnings   []string
		Suppressed int
	}{
		Event:      ev.Event,
		Status:     ev.Status,
		Host:       ev.Host,
		Time:       ev.Time.Format(time.RFC1123Z),
		Duration:   time.Duration(ev.DurationSeconds * float64(time.Second)).Round(time.Second).String(),
		Snapshot:   ev.Snapshot,
		Files:      ev.FilesNew,
		Bytes:      humanBytes(ev.BytesAdded),
		Error:      ev.Error,
		Summary:    ev.ErrorSummary,
		Class:      ev.ErrorClass,
		Code:       ev.ErrorCode,
		Detail:     ev.ErrorDetail,
		Action:     ev.Action,
		Warnings:   ev.Warnings,
		Suppressed: ev.Suppressed,
	}

	body := emailSuccess
	if ev.Status != statusSuccess {
		body = emailFailure
	}
	var html bytes.Buffer
	if err := body.Execute(&html, data); err != nil {
		return nil, err
	}

	text := ev.Text()
	if ev.Event == "test" {
		text = "JealousVault test email. The email channel works."
	}

	boundary := "jv-" + randomToken(12)
	subject := fmt.Sprintf("[JealousVault] %s %s on %s", ev.Event, ev.Status, ev.Host)
	if ev.Event == "test" {
		subject = "[JealousVault] test email"
	}

	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\n", header(s.opts.From))
	fmt.Fprintf(&message, "To: %s\r\n", header(strings.Join(s.opts.To, ", ")))
	fmt.Fprintf(&message, "Subject: %s\r\n", header(subject))
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&message, "Message-ID: <%s@jealous-vault>\r\n", randomToken(16))
	message.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)
	fmt.Fprintf(&message, "--%s\r\n", boundary)
	message.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n\r\n")
	message.WriteString(text)
	message.WriteString("\r\n\r\n")
	fmt.Fprintf(&message, "--%s\r\n", boundary)
	message.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n\r\n")
	message.Write(html.Bytes())
	message.WriteString("\r\n\r\n")
	fmt.Fprintf(&message, "--%s--\r\n", boundary)
	return message.Bytes(), nil
}

func header(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}

func randomToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
