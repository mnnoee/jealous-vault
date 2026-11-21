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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mnnoee/jealous-vault/internal/restic"
	"github.com/mnnoee/jealous-vault/internal/safepath"
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
	// RetryLock is passed to restic restore. Empty keeps the default
	// fail-fast behaviour; drill sets it so a lock from another host waits.
	RetryLock string
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
		if filepath.Clean(p) == "/" {
			return nil, fmt.Errorf("include path %q is the whole snapshot; use a full restore", p)
		}
	}
	resolved, err := ResolveTarget(filepath.Clean(target))
	if err != nil {
		return nil, err
	}
	return &Plan{
		Snapshot:  snapshot,
		Target:    resolved,
		Includes:  append([]string(nil), includes...),
		Overwrite: overwrite,
	}, nil
}

// ResolveTarget resolves symlinks in the longest existing prefix of target,
// so a symlinked target cannot bypass the Dangerous check. The leaf may not
// exist yet; its future path is appended to the resolved prefix.
func ResolveTarget(target string) (string, error) {
	clean := filepath.Clean(target)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("target %q must be an absolute path", target)
	}
	existing := clean
	rest := ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolve target %q: %w", target, err)
	}
	if rest == "" {
		return filepath.Clean(resolved), nil
	}
	return filepath.Join(resolved, rest), nil
}

// systemDirs are the top-level directories whose contents make up a running
// system. Writing a snapshot into them is never a routine restore.
var systemDirs = map[string]bool{
	"etc": true, "boot": true, "root": true, "usr": true,
	"bin": true, "sbin": true, "lib": true, "lib32": true,
	"lib64": true, "libx32": true, "var": true, "home": true,
	"opt": true, "srv": true, "proc": true, "sys": true,
	"dev": true, "run": true,
}

// Dangerous reports a restore that writes into a system location. safeRoots
// are directories owned by the agent where an automatic test restore is
// expected, for example /var/lib/jealous-vault/verify.
func (p *Plan) Dangerous(safeRoots ...string) bool {
	if p.Target == "/" {
		return true
	}
	for _, root := range safeRoots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if p.Target == root || strings.HasPrefix(p.Target, root+string(filepath.Separator)) {
			return false
		}
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(p.Target, string(filepath.Separator)), string(filepath.Separator))
	return systemDirs[first]
}

// PrepareScratch creates or validates a restore target. Automatic targets must
// be newly created and owner-only; an explicit target is tightened to 0700 and
// rejected if it belongs to another user or is group/other accessible.
func PrepareScratch(target string, auto bool) error {
	clean := filepath.Clean(target)
	if clean == string(filepath.Separator) {
		return errors.New("refusing to use / as a scratch target")
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	if systemDirs[first] && filepath.Dir(clean) == string(filepath.Separator) {
		return fmt.Errorf("refusing to change permissions of system directory %s", clean)
	}
	if auto {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.Mkdir(target, 0o700); err != nil {
			return fmt.Errorf("create scratch target %s: %w", target, err)
		}
	} else {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(target, 0o700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("scratch target %s is not a directory", target)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("scratch target %s must not be group or world accessible (%o)", target, perm)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("scratch target %s is owned by uid %d, expected %d", target, st.Uid, os.Geteuid())
	}
	return nil
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

// CheckTarget rejects a restore target that another local user can modify.
func CheckTarget(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return safepath.CheckParent(target)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("restore target %s is not a directory", target)
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("restore target %s is group or world writable (%o)", target, perm)
	}
	return nil
}

// Execute runs the restore.
func (p *Plan) Execute(ctx context.Context, client *restic.Client) error {
	if err := client.CheckRestoreVersion(ctx); err != nil {
		return err
	}
	return client.Restore(ctx, restic.RestoreOptions{
		Snapshot:  p.Snapshot,
		Target:    p.Target,
		Host:      p.Host,
		Includes:  p.Includes,
		Overwrite: p.Overwrite,
		Verify:    p.Verify,
		RetryLock: p.RetryLock,
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
var ErrDangerous = errors.New("refusing to restore a whole snapshot into a system directory without confirmation")
