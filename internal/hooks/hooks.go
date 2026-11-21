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
// failure. Output is logged, never returned as an error message.
func Run(ctx context.Context, log *slog.Logger, phase string, commands []string, env map[string]string) error {
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
			log.Debug("hook output", "phase", phase, "output", truncate(text, 4096))
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
