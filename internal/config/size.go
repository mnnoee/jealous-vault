// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize parses sizes like 10GB, 512MiB or a plain byte count.
func ParseSize(value string) (int64, error) {
	s := strings.TrimSpace(strings.ToUpper(value))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	units := []struct {
		suffix string
		factor float64
	}{
		{"TIB", 1 << 40},
		{"TB", 1e12},
		{"GIB", 1 << 30},
		{"GB", 1e9},
		{"MIB", 1 << 20},
		{"MB", 1e6},
		{"KIB", 1 << 10},
		{"KB", 1e3},
		{"B", 1},
	}
	for _, unit := range units {
		if !strings.HasSuffix(s, unit.suffix) {
			continue
		}
		number := strings.TrimSpace(strings.TrimSuffix(s, unit.suffix))
		parsed, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size %q", value)
		}
		if parsed <= 0 {
			return 0, fmt.Errorf("size %q must be positive", value)
		}
		return int64(parsed * unit.factor), nil
	}
	parsed, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("size %q must be positive", value)
	}
	return int64(parsed), nil
}
