// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package trigger tells the audit journal who started the agent: a systemd
// unit, a cron job or an interactive shell.
package trigger

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info describes how the current process was started.
type Info struct {
	// Source is systemd, cron, shell or unknown.
	Source string `json:"source"`
	// Unit is the systemd service that owns the process, when known.
	Unit string `json:"unit,omitempty"`
	// Trigger names the timer for scheduled runs, "manual" for a manual
	// systemctl start, "cron", "ssh:<addr>" or "tty".
	Trigger string `json:"trigger,omitempty"`
}

var (
	once  sync.Once
	cache Info
)

// Detect inspects the current process and caches the result: one agent run has
// exactly one origin.
func Detect() Info {
	once.Do(func() { cache = detect() })
	return cache
}

func detect() Info {
	cgroup := readFile("/proc/self/cgroup")
	ancestors := parentNames(os.Getpid(), 4)
	ssh := os.Getenv("SSH_CONNECTION")
	info := classify(cgroup, os.Getenv("INVOCATION_ID") != "", ancestors, ssh)
	if info.Source == "systemd" && info.Unit != "" {
		info.Trigger = systemdTrigger(info.Unit)
	}
	return info
}

// classify is the pure part of the detection, kept separate for tests.
func classify(cgroup string, invocation bool, ancestors []string, ssh string) Info {
	unit := cgroupUnit(cgroup)
	if strings.HasSuffix(unit, ".service") && (invocation || isAgentUnit(unit)) {
		return Info{Source: "systemd", Unit: unit}
	}
	for _, name := range ancestors {
		switch name {
		case "cron", "crond":
			return Info{Source: "cron", Trigger: "cron"}
		}
	}
	if ssh != "" {
		return Info{Source: "shell", Trigger: "ssh:" + remoteHost(ssh)}
	}
	if isInteractiveAncestor(ancestors) {
		return Info{Source: "shell", Trigger: "tty"}
	}
	if unit != "" {
		// A systemd session scope without a shell name still means a person.
		return Info{Source: "shell", Trigger: "session"}
	}
	return Info{Source: "unknown"}
}

// cgroupUnit returns the .service or .scope name from /proc/self/cgroup.
func cgroupUnit(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		_, path, ok := strings.Cut(line, "::")
		if !ok {
			continue
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if strings.HasSuffix(parts[i], ".service") || strings.HasSuffix(parts[i], ".scope") {
				return parts[i]
			}
		}
	}
	return ""
}

func isAgentUnit(unit string) bool {
	return strings.HasPrefix(unit, "jealous-vault-")
}

func isInteractiveAncestor(ancestors []string) bool {
	for _, name := range ancestors {
		switch name {
		case "bash", "zsh", "fish", "sh", "dash", "ksh", "sudo":
			return true
		}
	}
	return false
}

// parentNames walks up the process tree and collects process names.
func parentNames(pid int, depth int) []string {
	var names []string
	for i := 0; i < depth; i++ {
		stat := readFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if stat == "" {
			break
		}
		// The comm field is inside parentheses and may contain spaces.
		open := strings.Index(stat, "(")
		close := strings.LastIndex(stat, ")")
		if open < 0 || close < open {
			break
		}
		names = append(names, stat[open+1:close])
		fields := strings.Fields(stat[close+1:])
		if len(fields) < 2 {
			break
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid <= 1 {
			break
		}
		pid = ppid
	}
	return names
}

// systemdTrigger asks systemd whether the timer or a person started the unit.
// TriggeredBy alone is not proof: a manual systemctl start keeps the timer
// relationship. The timer fired only when LastTriggerUSec equals the start
// time of the service.
func systemdTrigger(unit string) string {
	triggeredBy := systemctlValue(2*time.Second, "show", unit, "-p", "TriggeredBy", "--value")
	triggeredBy = strings.TrimSpace(triggeredBy)
	if triggeredBy == "" {
		return "manual"
	}
	serviceStart := strings.TrimSpace(systemctlValue(2*time.Second, "show", unit, "-p", "ExecMainStartTimestamp", "--value"))
	timerStart := strings.TrimSpace(systemctlValue(2*time.Second, "show", triggeredBy, "-p", "LastTriggerUSec", "--value"))
	if serviceStart != "" && serviceStart == timerStart {
		return triggeredBy
	}
	return "manual"
}

func systemctlValue(timeout time.Duration, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func remoteHost(ssh string) string {
	fields := strings.Fields(ssh)
	if len(fields) == 0 {
		return "unknown"
	}
	// SSH_CONNECTION is "client_ip client_port server_ip server_port".
	return fields[0]
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
