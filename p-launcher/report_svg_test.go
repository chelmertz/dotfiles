package main

import (
	"regexp"
	"strings"
	"testing"
)

func mustSVG(t *testing.T, s string, w, h int) {
	t.Helper()
	if !strings.HasPrefix(s, "<svg") || !strings.HasSuffix(strings.TrimSpace(s), "</svg>") {
		t.Fatalf("not one svg: %.80s", s)
	}
	if !strings.Contains(s, ` width="`+itoa(w)+`"`) || !strings.Contains(s, ` height="`+itoa(h)+`"`) {
		t.Fatalf("size %dx%d missing: %.120s", w, h, s)
	}
}

func TestSparkline(t *testing.T) {
	T := themes["dark"]
	s := string(sparkline([]int{6, 9, 11, 8}, 60, 16, T.Friction, T))
	mustSVG(t, s, 60, 16)
	if !strings.Contains(s, `<path d="M0 `) || !strings.Contains(s, `<circle`) || !strings.Contains(s, T.Friction) {
		t.Fatalf("%s", s)
	}
	// degenerate inputs must not panic or divide by zero
	mustSVG(t, string(sparkline(nil, 60, 16, T.Friction, T)), 60, 16)
	mustSVG(t, string(sparkline([]int{0}, 60, 16, T.Friction, T)), 60, 16)
}

func TestKneeChart(t *testing.T) {
	T := themes["dark"]
	b := []Bucket{{Label: "1", Hours: 61, PRsPerHour: 0.21, WaitSec: 110}, {Label: "2", Hours: 88, PRsPerHour: 0.56, WaitSec: 210}, {Label: "3"}, {Label: "4"}, {Label: "5"}, {Label: "6+"}}
	s := string(kneeChart(b, 1, 1090, 292, T))
	mustSVG(t, s, 1090, 292)
	for _, want := range []string{`fill="` + T.ClaudeTint + `"`, "finished PRs per hour", "agents waiting for me", ">0.56<", ">3m 30s<", ">88 h<", "hours seen"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
	// no knee, no data: still one well-formed svg
	mustSVG(t, string(kneeChart(make([]Bucket, 6), -1, 1090, 292, T)), 1090, 292)
}

func TestSplitBars(t *testing.T) {
	T := themes["dark"]
	rows := []CompRow{{Path: "m/a", Auto: 0.45, Manual: 0.10}, {Path: "other (3)", Auto: 0.08}}
	s := string(splitBars(rows, 690, 22, T))
	mustSVG(t, s, 690, 44)
	if strings.Count(s, `fill-opacity="0.45"`) != 1 || !strings.Contains(s, ">0.55<") || !strings.Contains(s, ">m/a<") {
		t.Fatalf("%s", s)
	}
}

func TestStripSVGUniquePatterns(t *testing.T) {
	T := themes["dark"]
	segs := []StripSeg{{"idle", 8}, {"claude", 20}, {"away", 4}, {"you", 28}}
	a := string(stripSVG(segs, 1080, 8, T, 1))
	b := string(stripSVG(segs, 1080, 8, T, 2))
	mustSVG(t, a, 1080, 8)
	ids := regexp.MustCompile(`pattern id="([^"]+)"`)
	ia, ib := ids.FindStringSubmatch(a), ids.FindStringSubmatch(b)
	if ia == nil || ib == nil || ia[1] == ib[1] {
		t.Fatalf("pattern ids not unique: %v %v", ia, ib)
	}
	if !strings.Contains(a, `fill="url(#`+ia[1]+`)"`) || strings.Count(a, "<rect") != 5 {
		t.Fatalf("%s", a)
	}
	mustSVG(t, string(awaySwatch(T, 3)), 10, 8)
}
