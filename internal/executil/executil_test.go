// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package executil

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestSanitizeEnvDropsLoaderAndResticOverrides(t *testing.T) {
	out := SanitizeEnv([]string{
		"LD_PRELOAD=/tmp/evil.so",
		"LD_LIBRARY_PATH=/tmp",
		"BASH_ENV=/tmp/env",
		"RESTIC_PASSWORD_COMMAND=id",
		"RESTIC_REPOSITORY=attacker",
		"PATH=/tmp",
		"HOME=/root",
	})
	joined := strings.Join(out, "\n")
	for _, bad := range []string{"LD_PRELOAD", "LD_LIBRARY_PATH", "BASH_ENV", "RESTIC_PASSWORD_COMMAND", "RESTIC_REPOSITORY", "PATH=/tmp"} {
		if strings.Contains(joined, bad) {
			t.Errorf("sanitized env still contains %q:\n%s", bad, joined)
		}
	}
	if !strings.Contains(joined, "PATH="+SafePath) {
		t.Errorf("safe PATH missing:\n%s", joined)
	}
	if !strings.Contains(joined, "HOME=/root") {
		t.Errorf("benign variable was dropped:\n%s", joined)
	}
}

func TestCommandKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var output bytes.Buffer
	cmd := Command(ctx, "/bin/sh", "-c", "sleep 30 & wait")
	cmd.Stdout = &output
	cmd.Stderr = &output

	start := time.Now()
	_ = cmd.Run()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Command waited %s for an orphaned child", elapsed)
	}
}
