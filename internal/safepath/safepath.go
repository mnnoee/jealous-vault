// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package safepath opens secret and state files without following symlinks and
// rejects parent directories that another local user could modify.
package safepath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// CheckParent resolves the immediate parent directory and rejects it when it is
// group/world writable or owned by another user. A sticky /tmp is not accepted
// for secret files; use a private subdirectory.
func CheckParent(path string) error {
	dir := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dir, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", resolved)
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("%s is group or world writable (%o)", resolved, perm)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is owned by uid %d, expected %d", resolved, st.Uid, os.Geteuid())
	}
	return nil
}

// ReadFile opens path with O_NOFOLLOW, verifies it is a regular file owned by
// the current user, and reads it.
func ReadFile(path string) ([]byte, error) {
	if err := CheckParent(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("%s is owned by uid %d, expected %d", path, st.Uid, os.Geteuid())
	}
	return io.ReadAll(f)
}
