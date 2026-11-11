package main

import (
	"fmt"
	"strings"
	"time"
)

// stopwatch times a click's stages for its log line, so a slow click
// names the stage that was slow.
type stopwatch struct {
	now         func() time.Time
	start, last time.Time
	parts       []string
}

// newStopwatch starts at now(); a nil now (a test host that sets no clock)
// reads the real one.
func newStopwatch(now func() time.Time) *stopwatch {
	if now == nil {
		now = time.Now
	}
	t := now()
	return &stopwatch{now: now, start: t, last: t}
}

// mark ends the stage named stage at now.
func (s *stopwatch) mark(stage string) {
	t := s.now()
	s.parts = append(s.parts, fmt.Sprintf("%s %.1fs", stage, t.Sub(s.last).Seconds()))
	s.last = t
}

func (s *stopwatch) String() string {
	return strings.Join(append(append([]string{}, s.parts...), fmt.Sprintf("total %.1fs", s.last.Sub(s.start).Seconds())), " ")
}
