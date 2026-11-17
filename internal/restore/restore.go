// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package restore builds and runs a restore plan. Restoring over live data is
// the most dangerous operation in the product, so the plan is explicit about
// what will be written and where.
package restore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/restic"
)

// Overwrite modes accepted by restic.
const (
	OverwriteAlways    = "always"
	OverwriteNever     = "never"
	OverwriteIfChanged = "if-changed"
)

// Plan describes one restore before it touches the filesystem.
type Plan struct {
	Snapshot  string
	Target    string
	Host      string
	Includes  []string
	Overwrite string
	Verify    bool
}

// New validates the arguments and fills in defaults.
func New(snapshot, target string, includes []string, overwrite string) (*Plan, error) {
	if snapshot == "" {
		snapshot = "latest"
	}
	if target == "" {
		target = "/"
	}
	if !filepath.IsAbs(target) {
		return nil, fmt.Errorf("target %q must be an absolute path", target)
	}
	switch overwrite {
	case "", OverwriteAlways, OverwriteNever, OverwriteIfChanged:
	default:
		return nil, fmt.Errorf("unknown overwrite mode %q", overwrite)
	}
	for _, p := range includes {
		if !strings.HasPrefix(p, "/") {
			return nil, fmt.Errorf("include path %q must be absolute inside the snapshot", p)
		}
	}
	return &Plan{
		Snapshot:  snapshot,
		Target:    filepath.Clean(target),
		Includes:  append([]string(nil), includes...),
		Overwrite: overwrite,
	}, nil
}

// Dangerous reports a restore that writes a whole snapshot over the running
// system. Such a run needs an explicit confirmation.
func (p *Plan) Dangerous() bool {
	return p.Target == "/" && len(p.Includes) == 0
}

// Describe renders a short human readable summary.
func (p *Plan) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "snapshot: %s\n", p.Snapshot)
	fmt.Fprintf(&b, "target:   %s\n", p.Target)
	if len(p.Includes) == 0 {
		b.WriteString("include:  everything in the snapshot\n")
	} else {
		fmt.Fprintf(&b, "include:  %s\n", strings.Join(p.Includes, ", "))
	}
	if p.Overwrite != "" {
		fmt.Fprintf(&b, "overwrite: %s\n", p.Overwrite)
	}
	return b.String()
}

// Execute runs the restore.
func (p *Plan) Execute(ctx context.Context, client *restic.Client) error {
	return client.Restore(ctx, restic.RestoreOptions{
		Snapshot:  p.Snapshot,
		Target:    p.Target,
		Host:      p.Host,
		Includes:  p.Includes,
		Overwrite: p.Overwrite,
		Verify:    p.Verify,
	})
}

// TestTarget returns a fresh directory path for a test restore.
func TestTarget(base string, now time.Time) string {
	if base == "" {
		base = "/var/lib/jealous-vault/test-restore"
	}
	return filepath.Join(base, now.UTC().Format("20060102-150405"))
}

// ErrDangerous is returned when a dangerous restore lacks confirmation.
var ErrDangerous = errors.New("refusing to restore a whole snapshot over / without confirmation")
