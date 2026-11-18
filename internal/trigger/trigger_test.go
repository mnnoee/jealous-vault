// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package trigger

import "testing"

func TestCgroupUnit(t *testing.T) {
	cases := map[string]string{
		"0::/system.slice/jealous-vault-backup.service":    "jealous-vault-backup.service",
		"0::/user.slice/user-1000.slice/session-191.scope": "session-191.scope",
		"0::/system.slice/cron.service":                    "cron.service",
		"0::/":                                             "",
		"12:devices:/system.slice/docker-abc.scope\n0::/system.slice/jealous-vault-check.service": "jealous-vault-check.service",
	}
	for cgroup, want := range cases {
		if got := cgroupUnit(cgroup); got != want {
			t.Errorf("cgroupUnit(%q) = %q, want %q", cgroup, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		cgroup     string
		invocation bool
		ancestors  []string
		ssh        string
		want       Info
	}{
		{
			name:       "timer service",
			cgroup:     "0::/system.slice/jealous-vault-backup.service",
			invocation: true,
			ancestors:  []string{"jealous-vault", "systemd"},
			want:       Info{Source: "systemd", Unit: "jealous-vault-backup.service"},
		},
		{
			name:      "cron job",
			cgroup:    "0::/system.slice/cron.service",
			ancestors: []string{"jealous-vault", "sh", "crond", "systemd"},
			want:      Info{Source: "cron", Trigger: "cron"},
		},
		{
			name:      "ssh shell",
			cgroup:    "0::/user.slice/user-1000.slice/session-191.scope",
			ancestors: []string{"jealous-vault", "bash", "sshd"},
			ssh:       "203.0.113.5 51234 10.0.0.1 22",
			want:      Info{Source: "shell", Trigger: "ssh:203.0.113.5"},
		},
		{
			name:      "local tty",
			cgroup:    "0::/user.slice/user-0.slice/session-2.scope",
			ancestors: []string{"jealous-vault", "zsh", "login"},
			want:      Info{Source: "shell", Trigger: "tty"},
		},
		{
			name:   "container without init",
			cgroup: "0::/",
			want:   Info{Source: "unknown"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.cgroup, tc.invocation, tc.ancestors, tc.ssh)
			if got != tc.want {
				t.Fatalf("classify = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseLoginuid(t *testing.T) {
	cases := map[string]int{
		"1000\n":     1000,
		"0":          0,
		"":           0,
		"4294967295": 0,
		"junk":       0,
	}
	for value, want := range cases {
		if got := parseLoginuid(value); got != want {
			t.Errorf("parseLoginuid(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestDetectInvokerSudo(t *testing.T) {
	t.Setenv("SUDO_USER", "mnnoee")
	t.Setenv("SUDO_UID", "1000")
	t.Setenv("DOAS_USER", "")
	name, uid := detectInvoker("shell")
	if name != "mnnoee" || uid != 1000 {
		t.Fatalf("detectInvoker = %q/%d, want mnnoee/1000", name, uid)
	}
}

func TestRemoteHost(t *testing.T) {
	if got := remoteHost("198.51.100.7 40000 10.0.0.1 22"); got != "198.51.100.7" {
		t.Fatalf("remoteHost = %q", got)
	}
	if got := remoteHost(""); got != "unknown" {
		t.Fatalf("empty remoteHost = %q", got)
	}
}
