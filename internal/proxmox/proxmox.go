// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package proxmox wraps vzdump, qmrestore and pct restore.
package proxmox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mnnoee/jealous-vault/internal/executil"
)

// Options configures the Proxmox commands.
type Options struct {
	Vzdump   string
	QM       string
	PCT      string
	DumpDir  string
	Mode     string
	Compress string
	Storage  string
	Logger   *slog.Logger
}

// Manager runs Proxmox commands with fixed settings.
type Manager struct {
	opts Options
	log  *slog.Logger
}

// New returns a Manager with safe defaults.
func New(o Options) *Manager {
	if o.Vzdump == "" {
		o.Vzdump = "vzdump"
	}
	if o.QM == "" {
		o.QM = "qm"
	}
	if o.PCT == "" {
		o.PCT = "pct"
	}
	if o.Mode == "" {
		o.Mode = "snapshot"
	}
	if o.Compress == "" {
		o.Compress = "zstd"
	}
	if o.Storage == "" {
		o.Storage = "local-lvm"
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{opts: o, log: log}
}

// Dump creates one vzdump archive and returns its path.
func (m *Manager) Dump(ctx context.Context, id int) (string, error) {
	if err := os.MkdirAll(m.opts.DumpDir, 0o755); err != nil {
		return "", err
	}
	beforePaths, err := archives(m.opts.DumpDir)
	if err != nil {
		return "", err
	}
	before := make(map[string]bool, len(beforePaths))
	for _, path := range beforePaths {
		before[path] = true
	}

	compress := m.opts.Compress
	if compress == "none" {
		compress = "0"
	}
	args := []string{
		strconv.Itoa(id),
		"--dumpdir", m.opts.DumpDir,
		"--mode", m.opts.Mode,
		"--compress", compress,
		"--quiet", "1",
	}
	out, err := m.run(ctx, m.opts.Vzdump, args...)
	if err != nil {
		if text := strings.TrimSpace(out); text != "" {
			m.log.Error("vzdump output", "output", truncate(text, 4096))
		}
		return "", err
	}

	after, err := archives(m.opts.DumpDir)
	if err != nil {
		return "", err
	}
	for _, path := range after {
		if _, ok := before[path]; !ok && archiveID(path) == id {
			if err := chmodArchive(path); err != nil {
				return "", err
			}
			return path, nil
		}
	}
	return "", fmt.Errorf("vzdump finished but no archive for %d appeared in %s", id, m.opts.DumpDir)
}

// chmodArchive keeps guest data readable only by root. vzdump inherits the
// process umask, which would otherwise leave a 0644 archive in a 0755 dump
// directory on many hosts.
func chmodArchive(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	for _, suffix := range []string{".log", ".notes"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Guests lists QEMU VMs and LXC containers.
func (m *Manager) Guests(ctx context.Context) ([]int, error) {
	seen := map[int]bool{}
	var guests []int

	if out, err := m.run(ctx, m.opts.QM, "list"); err == nil {
		for _, id := range parseGuestList(out) {
			if !seen[id] {
				seen[id] = true
				guests = append(guests, id)
			}
		}
	}
	if out, err := m.run(ctx, m.opts.PCT, "list"); err == nil {
		for _, id := range parseGuestList(out) {
			if !seen[id] {
				seen[id] = true
				guests = append(guests, id)
			}
		}
	}
	if len(guests) == 0 {
		return nil, fmt.Errorf("no guests found, is this a Proxmox host")
	}
	sort.Ints(guests)
	return guests, nil
}

func parseGuestList(output string) []int {
	var ids []int
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// Restore restores one archive into the given VM or CT ID.
func (m *Manager) Restore(ctx context.Context, id int, archive string, force bool) error {
	base := filepath.Base(archive)
	switch {
	case strings.HasPrefix(base, "vzdump-qemu-"):
		args := []string{archive, strconv.Itoa(id), "--storage", m.opts.Storage}
		if force {
			args = append(args, "--force")
		}
		_, err := m.run(ctx, sibling(m.opts.QM, "qmrestore"), args...)
		return err
	case strings.HasPrefix(base, "vzdump-lxc-"):
		args := []string{strconv.Itoa(id), archive, "--storage", m.opts.Storage}
		if force {
			args = append(args, "--force")
		}
		_, err := m.run(ctx, m.opts.PCT, append([]string{"restore"}, args...)...)
		return err
	default:
		return fmt.Errorf("%s is not a vzdump archive", base)
	}
}

// RestoreFromDir finds the archive for id in dir and restores it.
func (m *Manager) RestoreFromDir(ctx context.Context, id int, dir string, force bool) error {
	archive, err := FindArchive(dir, id)
	if err != nil {
		return err
	}
	return m.Restore(ctx, id, archive, force)
}

// FindArchive returns the newest archive for id in dir.
func FindArchive(dir string, id int) (string, error) {
	paths, err := archives(dir)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, path := range paths {
		if archiveID(path) == id {
			matches = append(matches, path)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no archive for %d in %s", id, dir)
	}
	sortByModTime(matches)
	return matches[0], nil
}

// Prune keeps the newest keep archives per guest and deletes the rest with
// their logs.
func (m *Manager) Prune(keep int) error {
	if keep < 0 {
		return fmt.Errorf("keep must not be negative")
	}
	paths, err := archives(m.opts.DumpDir)
	if err != nil {
		return err
	}
	groups := map[int][]string{}
	for _, path := range paths {
		groups[archiveID(path)] = append(groups[archiveID(path)], path)
	}
	for _, group := range groups {
		sortByModTime(group)
		for i, path := range group {
			if i < keep {
				continue
			}
			for _, candidate := range []string{path, path + ".log", path + ".notes"} {
				if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
	}
	return nil
}

func archives(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "vzdump-") {
			continue
		}
		if strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".notes") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

func archiveID(path string) int {
	fields := strings.Split(filepath.Base(path), "-")
	if len(fields) < 3 {
		return 0
	}
	id, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0
	}
	return id
}

func sortByModTime(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		left, errLeft := os.Stat(paths[i])
		right, errRight := os.Stat(paths[j])
		if errLeft != nil || errRight != nil {
			return paths[i] > paths[j]
		}
		if left.ModTime().Equal(right.ModTime()) {
			return paths[i] > paths[j]
		}
		return left.ModTime().After(right.ModTime())
	})
}

func (m *Manager) run(ctx context.Context, bin string, args ...string) (string, error) {
	start := time.Now()
	cmd := executil.Command(ctx, bin, args...)
	cmd.Env = executil.Environment()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	text := strings.TrimSpace(output.String())
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return text, ctxErr
		}
		if text == "" {
			return text, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
		}
		return text, fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, truncate(text, 2048))
	}
	m.log.Debug("proxmox command", "command", bin, "args", args, "duration", time.Since(start))
	return text, nil
}

func sibling(bin, name string) string {
	if strings.Contains(bin, "/") {
		return filepath.Join(filepath.Dir(bin), name)
	}
	return name
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
