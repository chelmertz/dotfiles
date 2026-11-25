package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestNextTickFollowsTheTimer(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for now, want := range map[string]string{
		"2026-11-20 10:02": "2026-11-20 10:05", // Friday, working hours
		"2026-11-20 22:57": "2026-11-21 00:00", // last weekday slot passed
		"2026-11-21 13:10": "2026-11-21 14:00", // Saturday
		"2026-11-23 06:30": "2026-11-23 08:00", // Monday morning
		"2026-11-23 07:10": "2026-11-23 08:00",
		"2026-11-22 23:00": "2026-11-23 00:00",
	} {
		if got := nextTick(at(now)); !got.Equal(at(want)) {
			t.Errorf("after %s: %s, want %s", now, got.Format("2006-01-02 15:04"), want)
		}
	}
	// A row outlives the gap to the next tick by half an hour.
	if ttl := rowTTL(at("2026-11-21 13:10")); ttl != 80*time.Minute {
		t.Errorf("weekend ttl %v", ttl)
	}
	if ttl := rowTTL(at("2026-11-20 10:02")); ttl != 33*time.Minute {
		t.Errorf("weekday ttl %v", ttl)
	}
}

// The timer in nix/home.nix and nextTick are one schedule; each names the
// other. Asserted against the literals. Skipped where the nix file is not
// beside this module, as in the nix build's sandbox.
func TestTheNixTimerMatchesNextTick(t *testing.T) {
	b, err := os.ReadFile("../nix/home.nix")
	if err != nil {
		t.Skip("nix/home.nix not beside this module")
	}
	for _, want := range []string{`"Mon..Fri *-*-* 08..22:00/5:00"`, `"*-*-* 00/2:00:00"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("nix/home.nix lacks OnCalendar %s", want)
		}
	}
}

// Working ticks run on weekdays from 08:00 through the 22:00 hour.
func TestWorkingTickHours(t *testing.T) {
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 23, h, m, 0, 0, time.Local) }
	for at, want := range map[time.Time]bool{
		mon(7, 59): false, mon(8, 0): true, mon(22, 59): true, mon(23, 0): false,
		time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local): false, // Saturday
	} {
		if got := workingTick(at); got != want {
			t.Errorf("%s: %v, want %v", at.Format("Mon 15:04"), got, want)
		}
	}
}
