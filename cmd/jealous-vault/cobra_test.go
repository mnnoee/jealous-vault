// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestRebuildArgsRoundTrip(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("config", "/etc/default.toml", "")
	cmd.Flags().Bool("yes", false, "")
	cmd.Flags().Int("limit", 20, "")
	cmd.Flags().Duration("max-age", 25*time.Hour, "")
	cmd.Flags().StringArray("include", nil, "")

	if err := cmd.Flags().Set("config", "/tmp/c.toml"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("limit", "5"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("max-age", "2h"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("include", "/a"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("include", "/b"); err != nil {
		t.Fatal(err)
	}

	args, err := rebuildArgs(cmd, []string{"positional"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--config=/tmp/c.toml",
		"--include=/a",
		"--include=/b",
		"--limit=5",
		"--max-age=2h0m0s",
		"--yes=true",
		"positional",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %v, want %v", args, want)
		}
	}
}

func TestRebuildArgsSkipsUnchanged(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("config", "/etc/default.toml", "")
	args, err := rebuildArgs(cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 0 {
		t.Fatalf("unchanged flags were forwarded: %v", args)
	}
}

func TestRootCommandListsEverything(t *testing.T) {
	root := newRootCommand(context.Background())
	expected := []string{
		"init", "repo", "install", "uninstall", "backup", "vzdump",
		"restore-vm", "snapshots", "mount", "restore", "test-restore",
		"check", "doctor", "freshness", "status", "history",
		"recovery-kit", "version", "completion",
	}
	for _, name := range expected {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd == nil || cmd.Name() != name {
			t.Errorf("command %q is missing: %v", name, err)
		}
	}
}

func TestRepoKeySubcommands(t *testing.T) {
	root := newRootCommand(context.Background())
	for _, name := range []string{"list", "remove"} {
		cmd, _, err := root.Find([]string{"repo", "key", name})
		if err != nil || cmd.Name() != name {
			t.Errorf("repo key %s is missing: %v", name, err)
		}
	}
}
