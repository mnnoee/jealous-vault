// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package systemd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mnnoee/jealous-vault/internal/config"
	"github.com/mnnoee/jealous-vault/internal/executil"
)

// installRunit writes a supervised run script. runit restarts it if it ever
// exits, and the loop calls the tick command every fifteen minutes.
func (i *Installer) installRunit(cfg *config.Config, out io.Writer) error {
	name := periodicName("scheduler", i.Job)
	base := runitServiceBase()
	dir := filepath.Join(base, name)
	script := runitScript(i.BinaryPath, i.ConfigPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	runPath := filepath.Join(dir, "run")
	if err := writeFile(runPath, []byte(script)); err != nil {
		return err
	}
	if err := os.Chmod(runPath, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", runPath)

	linkDir := runitScanDir()
	if linkDir == "" {
		fmt.Fprintln(out, "no runit service directory found, link the service manually")
		return nil
	}
	link := filepath.Join(linkDir, name)
	_ = os.Remove(link)
	if err := os.Symlink(dir, link); err != nil {
		return err
	}
	fmt.Fprintf(out, "linked %s -> %s\n", link, dir)

	if i.SkipStart {
		fmt.Fprintln(out, "runit service linked but not started (--no-start)")
		return nil
	}
	if _, err := executil.LookPath("sv"); err == nil {
		cmd := executil.Command(context.Background(), "sv", "up", name)
		cmd.Env = executil.Environment()
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("sv up %s: %w", name, err)
		}
		fmt.Fprintf(out, "started %s\n", name)
	}
	return nil
}

func (i *Installer) uninstallRunit(out io.Writer) error {
	name := periodicName("scheduler", i.Job)
	if _, err := executil.LookPath("sv"); err == nil {
		cmd := executil.Command(context.Background(), "sv", "down", name)
		cmd.Env = executil.Environment()
		_ = cmd.Run()
	}
	for _, dir := range []string{runitScanDir(), runitServiceBase()} {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	fmt.Fprintf(out, "removed runit service %s\n", name)
	return nil
}

func runitServiceBase() string {
	for _, dir := range []string{"/etc/sv", "/etc/runit/sv"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return "/etc/sv"
}

func runitScanDir() string {
	for _, dir := range []string{"/var/service", "/run/runit/service", "/etc/service"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

func runitScript(binary, configPath string) string {
	return `#!/bin/sh
exec 2>&1
while :; do
	` + shellQuote(binary) + ` tick --config ` + shellQuote(configPath) + `
	sleep 900
done
`
}
