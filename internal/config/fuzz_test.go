// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"testing"
)

// fuzzConfig avoids keys that make Parse read secrets from the filesystem.
// The parser itself is still exercised with arbitrary TOML shapes.
const fuzzConfig = `
host = "host1"

[repository]
url = "local:/tmp/repo"
env = { RESTIC_PASSWORD = "fuzz" }

[backup]
profile = "custom"
paths = ["/etc"]

[schedule]
on_calendar = "*-*-* 03:30:00"
`

func FuzzParse(f *testing.F) {
	f.Add([]byte(fuzzConfig))
	f.Add([]byte(""))
	f.Add([]byte("host = \""))
	f.Add([]byte("[[[["))
	f.Add([]byte("host = 1"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if bytes.Contains(data, []byte("_file")) || bytes.Contains(data, []byte("password_env")) {
			t.Skip("file-backed secrets are outside this fuzz target")
		}
		cfg, err := Parse("fuzz.toml", data)
		if err != nil {
			return
		}
		if cfg == nil {
			t.Fatal("Parse returned a nil config without an error")
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Parse returned a config that Validate rejects: %v", err)
		}
	})
}
