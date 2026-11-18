// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseTelegramChats(t *testing.T) {
	chats := parseTelegramChats("1909549916", []string{"1909549916", "-5365208231", "-1001234567890:42"}, nil)
	if len(chats) != 3 {
		t.Fatalf("chats = %+v", chats)
	}
	if chats[0].ID != "1909549916" || chats[0].ThreadID != 0 {
		t.Errorf("chat 0 = %+v", chats[0])
	}
	if chats[1].ID != "-5365208231" {
		t.Errorf("chat 1 = %+v", chats[1])
	}
	if chats[2].ID != "-1001234567890" || chats[2].ThreadID != 42 {
		t.Errorf("chat 2 = %+v", chats[2])
	}
}

func TestTelegramMultipleChats(t *testing.T) {
	type sent struct {
		chatID string
		thread string
		mode   string
		text   string
	}
	var messages []sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		messages = append(messages, sent{
			chatID: r.FormValue("chat_id"),
			thread: r.FormValue("message_thread_id"),
			mode:   r.FormValue("parse_mode"),
			text:   r.FormValue("text"),
		})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	notifier := New(Options{
		TelegramToken:   "token",
		TelegramChatIDs: []string{"1909549916", "-5365208231:42"},
		TelegramAPIBase: srv.URL,
		Timeout:         time.Second,
	})
	ev := Event{Event: "backup", Status: "failed", Host: "jv-vm", Error: "disk full", Time: time.Now()}
	if err := notifier.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[0].chatID != "1909549916" || messages[0].thread != "" {
		t.Errorf("first message = %+v", messages[0])
	}
	if messages[1].chatID != "-5365208231" || messages[1].thread != "42" {
		t.Errorf("second message = %+v", messages[1])
	}
	if messages[0].mode != "HTML" {
		t.Errorf("parse mode = %q", messages[0].mode)
	}
	for _, want := range []string{"<b>jv-vm</b>", "<pre>disk full</pre>"} {
		if !strings.Contains(messages[0].text, want) {
			t.Errorf("telegram text does not contain %q:\n%s", want, messages[0].text)
		}
	}
}
