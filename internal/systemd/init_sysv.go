// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package systemd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mnnoee/jealous-vault/internal/config"
)

// installSysVInit writes an LSB init script that keeps a small tick loop in
// the background. sysvinit has no timers, so the loop wakes the agent every
// fifteen minutes and the agent decides what is due.
func (i *Installer) installSysVInit(ctx context.Context, cfg *config.Config, out io.Writer) error {
	name := periodicName("scheduler", i.Job)
	path := filepath.Join("/etc/init.d", name)
	script := sysvScript(name, i.BinaryPath, i.ConfigPath)
	if err := writeFile(path, []byte(script)); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", path)

	if i.SkipStart {
		fmt.Fprintln(out, "sysvinit script written but not enabled (--no-start)")
		return nil
	}
	if _, err := exec.LookPath(i.RCUpdate); err == nil {
		_ = exec.CommandContext(ctx, i.RCUpdate, name, "defaults").Run()
	}
	if err := exec.CommandContext(ctx, path, "start").Run(); err != nil {
		return fmt.Errorf("start %s: %w", path, err)
	}
	fmt.Fprintf(out, "started %s\n", name)
	return nil
}

func (i *Installer) uninstallSysVInit(out io.Writer) error {
	name := periodicName("scheduler", i.Job)
	path := filepath.Join("/etc/init.d", name)
	_ = exec.Command(path, "stop").Run()
	if _, err := exec.LookPath(i.RCUpdate); err == nil {
		_ = exec.Command(i.RCUpdate, "-f", name, "remove").Run()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(out, "removed %s\n", path)
	return nil
}

func sysvScript(name, binary, configPath string) string {
	return `#!/bin/sh
### BEGIN INIT INFO
# Provides:          ` + name + `
# Required-Start:    $network $remote_fs
# Required-Stop:     $network $remote_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: JealousVault scheduler
### END INIT INFO

PIDFILE=/run/` + name + `.pid
LOGDIR=/var/log/jealous-vault
BIN=` + shellQuote(binary) + `
CFG=` + shellQuote(configPath) + `
INTERVAL=900

running() {
	[ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null
}

start() {
	running && return 0
	mkdir -p "$LOGDIR"
	nohup sh -c 'while :; do "$0" tick --config "$1"; sleep "$2"; done' "$BIN" "$CFG" "$INTERVAL" \
		>>"$LOGDIR/scheduler.log" 2>&1 &
	echo $! >"$PIDFILE"
}

stop() {
	running || { rm -f "$PIDFILE"; return 0; }
	kill "$(cat "$PIDFILE")" 2>/dev/null
	rm -f "$PIDFILE"
}

case "$1" in
start) start ;;
stop) stop ;;
restart) stop; start ;;
status)
	if running; then echo "$PIDFILE running"; else echo "$PIDFILE stopped"; exit 3; fi
	;;
*) echo "usage: $0 {start|stop|restart|status}" >&2; exit 2 ;;
esac
`
}
