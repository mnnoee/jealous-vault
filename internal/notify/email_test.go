// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmailLocalSendmail(t *testing.T) {
	dir := t.TempDir()
	messageFile := filepath.Join(dir, "message")
	argsFile := filepath.Join(dir, "args")
	sendmail := filepath.Join(dir, "sendmail")
	script := "#!/bin/sh\ncat > " + messageFile + "\nprintf '%s\\n' \"$*\" > " + argsFile + "\n"
	if err := os.WriteFile(sendmail, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	sender := newEmailSender(EmailOptions{
		Enabled:  true,
		Mode:     "local",
		From:     "backups@example.com",
		To:       []string{"admin@example.com"},
		Sendmail: sendmail,
		Timeout:  time.Second,
	})
	ev := Event{
		Event:  "backup",
		Status: "failed",
		Host:   "node-1",
		Error:  "disk full",
		Time:   time.Now(),
	}
	if err := sender.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(args)) != "-t -i" {
		t.Errorf("sendmail args = %q", args)
	}

	body, err := os.ReadFile(messageFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"From: backups@example.com",
		"To: admin@example.com",
		"Subject: [JealousVault] backup failed on node-1",
		"Message-ID:",
		"Content-Type: multipart/alternative",
		"Content-Type: text/plain",
		"Content-Type: text/html",
		"disk full",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message does not contain %q:\n%s", want, text)
		}
	}
}

func TestEmailCheckLocal(t *testing.T) {
	dir := t.TempDir()
	sendmail := filepath.Join(dir, "sendmail")
	if err := os.WriteFile(sendmail, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sender := newEmailSender(EmailOptions{
		Enabled:  true,
		Mode:     "local",
		From:     "backups@example.com",
		To:       []string{"admin@example.com"},
		Sendmail: sendmail,
		Timeout:  time.Second,
	})
	if err := sender.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEmailTestSubject(t *testing.T) {
	dir := t.TempDir()
	messageFile := filepath.Join(dir, "message")
	sendmail := filepath.Join(dir, "sendmail")
	script := "#!/bin/sh\ncat > " + messageFile + "\n"
	if err := os.WriteFile(sendmail, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sender := newEmailSender(EmailOptions{
		Enabled:  true,
		Mode:     "local",
		From:     "backups@example.com",
		To:       []string{"admin@example.com"},
		Sendmail: sendmail,
		Timeout:  time.Second,
	})
	if err := sender.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(messageFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Subject: [JealousVault] test email") {
		t.Fatalf("unexpected test message:\n%s", body)
	}
}

func TestEmailSMTPPlain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- ""
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 test\r\n")
		var data strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- data.String()
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					fmt.Fprint(conn, "250 ok\r\n")
					continue
				}
				data.WriteString(line)
				continue
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"):
				fmt.Fprint(conn, "250-test\r\n250 OK\r\n")
			case strings.HasPrefix(command, "HELO"):
				fmt.Fprint(conn, "250 OK\r\n")
			case strings.HasPrefix(command, "MAIL FROM"):
				fmt.Fprint(conn, "250 OK\r\n")
			case strings.HasPrefix(command, "RCPT TO"):
				fmt.Fprint(conn, "250 OK\r\n")
			case command == "DATA":
				fmt.Fprint(conn, "354 go\r\n")
				inData = true
			case command == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				done <- data.String()
				return
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	sender := newEmailSender(EmailOptions{
		Enabled:  true,
		Mode:     "smtp",
		From:     "backups@example.com",
		To:       []string{"admin@example.com"},
		Host:     "127.0.0.1",
		Port:     port,
		Security: "none",
		Timeout:  time.Second,
	})
	ev := Event{Event: "check", Status: "failed", Host: "node-1", Error: "broken", Time: time.Now()}
	if err := sender.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	select {
	case message := <-done:
		if !strings.Contains(message, "Subject: [JealousVault] check failed on node-1") {
			t.Fatalf("unexpected message:\n%s", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP server did not receive the message")
	}
}
