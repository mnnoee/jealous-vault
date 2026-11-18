// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

// Package schedule decides whether a scheduled action is due. It is used by
// the tick command, which cron or a tiny init loop wakes up periodically.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MostRecent returns the last time the calendar fired at or before now.
func MostRecent(calendar string, now time.Time) (time.Time, error) {
	day, hour, minute, err := parse(calendar)
	if err != nil {
		return time.Time{}, err
	}
	now = now.Truncate(time.Minute)

	candidate := func(dayOffset int, h int) time.Time {
		d := now.AddDate(0, 0, dayOffset)
		return time.Date(d.Year(), d.Month(), d.Day(), h, minute, 0, 0, now.Location())
	}

	switch day.kind {
	case "daily":
		var latest time.Time
		for _, h := range hour {
			c := candidate(0, h)
			if !c.After(now) && c.After(latest) {
				latest = c
			}
		}
		if !latest.IsZero() {
			return latest, nil
		}
		// Yesterday's last hour.
		return candidate(-1, hour[len(hour)-1]), nil
	case "weekly":
		for offset := 0; offset < 8; offset++ {
			d := now.AddDate(0, 0, -offset)
			if int(d.Weekday()) != day.weekday {
				continue
			}
			c := candidate(-offset, hour[0])
			if !c.After(now) {
				return c, nil
			}
		}
		return now.AddDate(0, 0, -7), nil
	}
	return time.Time{}, fmt.Errorf("unsupported calendar %q", calendar)
}

// Due reports whether the action should run now: the last run predates the
// most recent scheduled time.
func Due(calendar string, last, now time.Time) (bool, time.Time, error) {
	at, err := MostRecent(calendar, now)
	if err != nil {
		return false, time.Time{}, err
	}
	return last.IsZero() || last.Before(at), at, nil
}

type daySpec struct {
	kind    string
	weekday int
}

func parse(calendar string) (daySpec, []int, int, error) {
	fields := strings.Fields(calendar)
	var dayField, clock string
	switch len(fields) {
	case 2:
		dayField, clock = fields[0], fields[1]
	case 3:
		dayField, clock = fields[0], fields[2]
	default:
		return daySpec{}, nil, 0, fmt.Errorf("cannot parse schedule %q", calendar)
	}

	parts := strings.Split(clock, ":")
	if len(parts) != 3 {
		return daySpec{}, nil, 0, fmt.Errorf("cannot parse schedule %q", calendar)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return daySpec{}, nil, 0, fmt.Errorf("cannot parse schedule %q", calendar)
	}
	hours := make([]int, 0, 4)
	for _, raw := range strings.Split(parts[0], ",") {
		if raw == "*" {
			return daySpec{}, nil, 0, fmt.Errorf("schedule %q needs explicit hours", calendar)
		}
		h, err := strconv.Atoi(raw)
		if err != nil || h < 0 || h > 23 {
			return daySpec{}, nil, 0, fmt.Errorf("cannot parse schedule %q", calendar)
		}
		hours = append(hours, h)
	}
	if len(hours) == 0 {
		return daySpec{}, nil, 0, fmt.Errorf("cannot parse schedule %q", calendar)
	}

	if dayField == "*-*-*" || dayField == "daily" {
		return daySpec{kind: "daily"}, hours, minute, nil
	}
	weekdays := map[string]int{
		"Sun": 0, "Mon": 1, "Tue": 2, "Wed": 3, "Thu": 4, "Fri": 5, "Sat": 6,
	}
	for prefix, weekday := range weekdays {
		if strings.HasPrefix(dayField, prefix) {
			return daySpec{kind: "weekly", weekday: weekday}, hours, minute, nil
		}
	}
	return daySpec{}, nil, 0, fmt.Errorf("cannot parse schedule %q", calendar)
}
