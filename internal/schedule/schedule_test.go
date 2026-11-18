// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package schedule

import (
	"testing"
	"time"
)

func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.Local)
}

func TestMostRecentDaily(t *testing.T) {
	now := at(2026, 11, 18, 10, 0)
	got, err := MostRecent("*-*-* 03:30:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := at(2026, 11, 18, 3, 30); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	early := at(2026, 11, 18, 2, 0)
	got, err = MostRecent("*-*-* 03:30:00", early)
	if err != nil {
		t.Fatal(err)
	}
	if want := at(2026, 11, 17, 3, 30); !got.Equal(want) {
		t.Fatalf("before the time: got %v, want %v", got, want)
	}
}

func TestMostRecentHourList(t *testing.T) {
	now := at(2026, 11, 18, 13, 0)
	got, err := MostRecent("*-*-* 00,06,12,18:15:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := at(2026, 11, 18, 12, 15); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMostRecentWeekly(t *testing.T) {
	// 2026-11-18 is a Wednesday.
	now := at(2026, 11, 18, 10, 0)
	got, err := MostRecent("Sun *-*-* 04:30:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := at(2026, 11, 15, 4, 30); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDue(t *testing.T) {
	now := at(2026, 11, 18, 10, 0)
	due, _, err := Due("*-*-* 03:30:00", time.Time{}, now)
	if err != nil || !due {
		t.Fatalf("never run must be due: %v %v", due, err)
	}
	due, _, err = Due("*-*-* 03:30:00", at(2026, 11, 18, 4, 0), now)
	if err != nil || due {
		t.Fatalf("already run after the schedule: %v %v", due, err)
	}
	due, _, err = Due("*-*-* 03:30:00", at(2026, 11, 17, 4, 0), now)
	if err != nil || !due {
		t.Fatalf("yesterday's run must be due: %v %v", due, err)
	}
}

func TestParseRejectsUnknown(t *testing.T) {
	for _, calendar := range []string{"2026-11-18 03:00:00", "*-*-* *:*:00", "garbage"} {
		if _, err := MostRecent(calendar, time.Now()); err == nil {
			t.Errorf("%q was accepted", calendar)
		}
	}
}
