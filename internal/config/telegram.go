// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"strconv"
	"strings"
)

func validateTelegramChat(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("notify.telegram_chat_ids contains an empty value")
	}
	id := raw
	thread := ""
	if before, after, ok := strings.Cut(raw, ":"); ok {
		id, thread = before, after
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return fmt.Errorf("notify.telegram chat id %q is not numeric", raw)
	}
	if thread != "" {
		n, err := strconv.Atoi(thread)
		if err != nil || n <= 0 {
			return fmt.Errorf("notify.telegram thread %q must be a positive number", thread)
		}
	}
	return nil
}
