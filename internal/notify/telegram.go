// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

type telegramChat struct {
	ID       string
	ThreadID int
}

func parseTelegramChats(single string, many []string, log *slog.Logger) []telegramChat {
	raw := append([]string(nil), many...)
	if single != "" {
		raw = append(raw, single)
	}
	seen := map[string]bool{}
	var chats []telegramChat
	for _, value := range raw {
		chat, err := parseTelegramChat(value)
		if err != nil {
			log.Warn("skip invalid telegram chat", "chat", value, "error", err)
			continue
		}
		key := fmt.Sprintf("%s:%d", chat.ID, chat.ThreadID)
		if seen[key] {
			continue
		}
		seen[key] = true
		chats = append(chats, chat)
	}
	return chats
}

func parseTelegramChat(raw string) (telegramChat, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return telegramChat{}, fmt.Errorf("empty chat id")
	}
	id := raw
	thread := 0
	if before, after, ok := strings.Cut(raw, ":"); ok {
		id = before
		parsed, err := strconv.Atoi(after)
		if err != nil || parsed <= 0 {
			return telegramChat{}, fmt.Errorf("thread id %q must be a positive number", after)
		}
		thread = parsed
	}
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return telegramChat{}, fmt.Errorf("chat id %q is not numeric", raw)
	}
	return telegramChat{ID: strconv.FormatInt(number, 10), ThreadID: thread}, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
