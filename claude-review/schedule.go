package main

import "time"

// The tick timer's schedule: every 5 minutes on weekdays 08:00-22:59, and
// every 2 hours on the even hour otherwise (in 195 rounds to 2026-11-20, 3
// started on a weekend and none between 02:00 and 09:00). The timer in
// nix/home.nix (OnCalendar "Mon..Fri *-*-* 08..22:00/5:00" and
// "*-*-* 00/2:00:00") is the same schedule; TestTheNixTimerMatchesNextTick
// pins the two.
const (
	tickStep    = 5 * time.Minute
	rowOverhang = 30 * time.Minute
)

func workingTick(t time.Time) bool {
	wd := t.Weekday()
	return wd != time.Saturday && wd != time.Sunday && t.Hour() >= 8 && t.Hour() <= 22
}

// nextTick is the first scheduled tick after now, in now's location.
func nextTick(now time.Time) time.Time {
	t := now.Truncate(tickStep).Add(tickStep)
	for {
		if workingTick(t) || (t.Minute() == 0 && t.Hour()%2 == 0) {
			return t
		}
		t = t.Add(tickStep)
	}
}

// rowTTL is how long a row tick sends now stays up: past the next scheduled
// tick by rowOverhang, so a row never lapses between ticks, and goes stale
// within that half hour of a tick that should have run and did not.
func rowTTL(now time.Time) time.Duration {
	return nextTick(now).Sub(now) + rowOverhang
}
