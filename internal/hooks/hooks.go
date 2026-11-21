// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package hooks runs shell commands around backup and restore operations.
package hooks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/executil"
)

// DefaultTimeout bounds one hook, so a broken command cannot hang the agent.
const DefaultTimeout = 10 * time.Minute

// Run executes commands through /bin/sh in order and stops at the first
// failure. Sensitive output is hidden by default.
func Run(ctx context.Context, log *slog.Logger, phase string, commands []string, env map[string]string) error {
	return RunWith(ctx, log, phase, commands, env, false)
}

// RunWith is Run with an explicit sensitive-output preference.
func RunWith(ctx context.Context, log *slog.Logger, phase string, commands []string, env map[string]string, showSensitive bool) error {
	if len(commands) == 0 {
		return nil
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	extra := make([]string, 0, len(env))
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		extra = append(extra, k+"="+env[k])
	}

	for i, command := range commands {
		hookCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
		cmd := executil.Command(hookCtx, "/bin/sh", "-c", command)
		cmd.Env = append(executil.Environment(), extra...)

		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output

		err := cmd.Run()
		cancel()

		if text := strings.TrimSpace(output.String()); text != "" {
			if showSensitive {
				log.Debug("hook output", "phase", phase, "output", truncate(text, 4096))
			} else {
				log.Info("hook output hidden; set logging.show_sensitive = true to print it", "phase", phase, "bytes", len(text))
			}
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%s hook #%d: %w", phase, i+1, err)
		}
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
