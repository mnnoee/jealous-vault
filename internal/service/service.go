// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package service stops and starts systemd units for restore recipes.
package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/executil"
)

// Timeout bounds one systemctl action.
const Timeout = 2 * time.Minute

// Run executes systemctl action name. The service name is validated by the
// configuration loader, so it is passed as a separate argument and never
// through a shell.
func Run(ctx context.Context, log *slog.Logger, action, name string) error {
	if action == "" || name == "" {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	cmd := executil.Command(runCtx, "systemctl", action, name)
	cmd.Env = executil.Environment()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if log != nil {
		log.Info("service command", "action", action, "service", name, "error", err)
	}
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(output.String())
	if text == "" {
		return fmt.Errorf("systemctl %s %s: %w", action, name, err)
	}
	return fmt.Errorf("systemctl %s %s: %w: %s", action, name, err, text)
}
