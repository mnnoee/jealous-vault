// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestDefaultOneFileSystem(t *testing.T) {
	if !Default().Backup.OneFileSystem {
		t.Fatal("one_file_system must default to true")
	}
}

func TestOmittedOneFileSystemBecomesTrue(t *testing.T) {
	cfg, err := Parse("config.toml", []byte(`
host = "host1"

[repository]
url = "local:/srv/repo"
env = { RESTIC_PASSWORD = "secret" }

[backup]
paths = ["/etc"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Backup.OneFileSystem {
		t.Fatal("omitted one_file_system must become true")
	}
}

func TestValidProfile(t *testing.T) {
	for _, profile := range []string{"host", "vm-dumps", "custom"} {
		if !ValidProfile(profile) {
			t.Errorf("profile %q must be valid", profile)
		}
	}
	for _, profile := range []string{"", "bogus", "HOST"} {
		if ValidProfile(profile) {
			t.Errorf("profile %q must be invalid", profile)
		}
	}
}
