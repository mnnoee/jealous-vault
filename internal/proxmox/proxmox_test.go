// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDumpFindsNewArchive(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	dumpDir := filepath.Join(dir, "dump")
	vzdump := fakeBin(t, dir, "vzdump", `dir=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--dumpdir" ]; then dir="$a"; fi
  prev="$a"
done
mkdir -p "$dir"
echo data > "$dir/vzdump-qemu-$1-2026_11_17-02_00_00.vma.zst"
printf '%s\n' "$*" >> `+argsFile+`
`)

	m := New(Options{
		Vzdump:   vzdump,
		DumpDir:  dumpDir,
		Mode:     "snapshot",
		Compress: "zstd",
	})
	archive, err := m.Dump(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(archive, "vzdump-qemu-100-2026_11_17-02_00_00.vma.zst") {
		t.Fatalf("archive = %q", archive)
	}
	info, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("archive mode = %o, want 600", info.Mode().Perm())
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"100 --dumpdir", "--mode snapshot", "--compress zstd"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestGuests(t *testing.T) {
	dir := t.TempDir()
	qm := fakeBin(t, dir, "qm", `printf '      VMID NAME STATUS MEM(MB) BOOTDISK(GB) PID\n       100 vm stopped 1024 1.00 0\n'`)
	pct := fakeBin(t, dir, "pct", `printf 'VMID Status Lock Name\n101 stopped  ct\n'`)

	m := New(Options{QM: qm, PCT: pct})
	guests, err := m.Guests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(guests) != 2 || guests[0] != 100 || guests[1] != 101 {
		t.Fatalf("guests = %v", guests)
	}
}

func TestRestoreQEMU(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "restore.log")
	qm := fakeBin(t, dir, "qm", "exit 0\n")
	fakeBin(t, dir, "qmrestore", `printf '%s\n' "$*" >> `+argsFile+"\n")
	archive := filepath.Join(dir, "vzdump-qemu-100-2026_11_17-02_00_00.vma.zst")
	if err := os.WriteFile(archive, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := New(Options{QM: qm, Storage: "local-lvm"})
	if err := m.Restore(context.Background(), 100, archive, true); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{archive, "100 --storage local-lvm", "--force"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestRestoreLXC(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "restore.log")
	pct := fakeBin(t, dir, "pct", `if [ "$1" = "restore" ]; then printf '%s\n' "$*" >> `+argsFile+`; fi
`)
	archive := filepath.Join(dir, "vzdump-lxc-101-2026_11_17-02_00_00.tar.zst")
	if err := os.WriteFile(archive, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := New(Options{PCT: pct, Storage: "local-lvm"})
	if err := m.Restore(context.Background(), 101, archive, false); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"restore 101", archive, "--storage local-lvm"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
}

func TestFindArchiveAndPrune(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "vzdump-qemu-100-2026_11_16-02_00_00.vma.zst")
	mid := filepath.Join(dir, "vzdump-qemu-100-2026_11_17-02_00_00.vma.zst")
	newest := filepath.Join(dir, "vzdump-qemu-100-2026_11_18-02_00_00.vma.zst")
	other := filepath.Join(dir, "vzdump-lxc-101-2026_11_18-02_00_00.tar.zst")
	for _, path := range []string{old, mid, newest, other} {
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(old, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(mid, now.Add(-24*time.Hour), now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newest, now, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	found, err := FindArchive(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if found != newest {
		t.Fatalf("found = %q, want %q", found, newest)
	}

	m := New(Options{DumpDir: dir})
	if err := m.Prune(1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("newest archive was removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other guest archive was removed: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old archive was not removed")
	}
}
