// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"10GB":   10_000_000_000,
		"10GiB":  10 << 30,
		"512MiB": 512 << 20,
		"1.5GB":  1_500_000_000,
		"1024":   1024,
		"2048 B": 2048,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}

	for _, in := range []string{"", "abc", "0", "-5GB", "10XB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) did not fail", in)
		}
	}
}
