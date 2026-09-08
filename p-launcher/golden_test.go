package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Expect-style screenshot tests (see blog.janestreet.com/the-joy-of-expect-tests):
// render the real UI headlessly, compare to a golden PNG in testdata/, and
// accept a change by re-running with P_LAUNCHER_E2E_UPDATE=1. Opt-in with
// P_LAUNCHER_E2E=1 because they need Xvfb, rofi, ImageMagick's import and
// firefox, and the user's fonts. rofi runs from the caller's environment (icon
// loaders, theme); only the X server is virtual.

const goldenTolerance = 0.002 // fraction of pixels allowed to differ

func e2e(t *testing.T) {
	t.Helper()
	if os.Getenv("P_LAUNCHER_E2E") == "" {
		t.Skip("set P_LAUNCHER_E2E=1 (and P_LAUNCHER_E2E_UPDATE=1 to accept) to run screenshot tests")
	}
}

func needBins(t *testing.T, bins ...string) {
	t.Helper()
	for _, bin := range bins {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
}

// startXvfb returns the DISPLAY of a fresh virtual server and a stop func.
func startXvfb(t *testing.T) (string, func()) {
	t.Helper()
	display := ":" + strconv.Itoa(90+os.Getpid()%50)
	cmd := exec.Command("Xvfb", display, "-screen", "0", "1920x1080x24", "-nolisten", "tcp")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	return display, func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
}

func screenshot(t *testing.T, display, out string) {
	t.Helper()
	cmd := exec.Command("import", "-window", "root", out)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("import: %v %s", err, b)
	}
}

// compareGolden diffs out against testdata/<name>.png; with the update env
// it writes the golden instead. Differences are reported with a count so the
// diff is readable in CI output; the actual image is left next to the golden.
func compareGolden(t *testing.T, name, out string) {
	t.Helper()
	golden := filepath.Join("testdata", name+".png")
	if os.Getenv("P_LAUNCHER_E2E_UPDATE") != "" {
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", golden)
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("no golden %s: run with P_LAUNCHER_E2E_UPDATE=1 to create it", golden)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	diff, total, err := pixelDiff(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if float64(diff)/float64(total) > goldenTolerance {
		actual := filepath.Join("testdata", name+".actual.png")
		_ = os.WriteFile(actual, got, 0o644)
		t.Fatalf("%s: %d of %d pixels differ (%.2f%%); actual written to %s", name, diff, total, 100*float64(diff)/float64(total), actual)
	}
}

func pixelDiff(a, b []byte) (diff, total int, err error) {
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return 0, 0, err
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, 0, err
	}
	if ia.Bounds() != ib.Bounds() {
		return 0, 0, fmt.Errorf("size differs: %v vs %v", ia.Bounds(), ib.Bounds())
	}
	r := ia.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			total++
			if !sameColor(ia.At(x, y), ib.At(x, y)) {
				diff++
			}
		}
	}
	return diff, total, nil
}

func sameColor(a, b interface{ RGBA() (r, g, b, a uint32) }) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	const tol = 8 << 8 // anti-aliasing noise
	return absDiff(ar, br) <= tol && absDiff(ag, bg) <= tol && absDiff(ab, bb) <= tol
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

var _ = image.Rect // keep image imported for Bounds types in error paths

func TestGoldenRofiMenu(t *testing.T) {
	e2e(t)
	needBins(t, "Xvfb", "rofi", "import")
	display, stop := startXvfb(t)
	defer stop()
	dir := t.TempDir()
	icons, err := writeIcons(filepath.Join(dir, "icons"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ps := []Project{
		{Path: "m/reputation", Name: "reputation", Label: "matchi", Ball: "you", Reason: "question", Since: now.Add(-3 * time.Minute)},
		{Path: "m/dependabot", Name: "dependabot", Label: "matchi", Ball: "claude", Since: now.Add(-42 * time.Second), Description: "keep every service's dependencies current without breaking deploys"},
		{Path: "m/nginx-ingress", Name: "nginx-ingress", Label: "matchi"},
		{Path: "personal/p-launcher", Name: "p-launcher", Label: "personal"},
		{Path: "personal/health", Name: "health", Label: "personal"},
	}
	open := map[string]bool{"p:m/reputation": true, "p:m/dependabot": true, "p:m/nginx-ingress": true}
	rows := rowsAt(ps, open, true, now) // fixed clock: stable durations in the golden
	// the same arguments menuMode uses for the main list, hint line included
	cmd := exec.Command("rofi", append(append(rofiArgs("project", "F5"), mainMenuExtra()...), rowHeightArgs(ps)...)...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	cmd.Stdin = bytes.NewReader(rofiInput(rows, icons))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(1500 * time.Millisecond)
	out := filepath.Join(dir, "rofi.png")
	screenshot(t, display, out)
	compareGolden(t, "rofi-menu", out)
}

func TestGoldenRofiVerbs(t *testing.T) {
	e2e(t)
	needBins(t, "Xvfb", "rofi", "import")
	display, stop := startXvfb(t)
	defer stop()
	dir := t.TempDir()
	icons, err := writeIcons(filepath.Join(dir, "icons"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("rofi", rofiArgs("m/reputation", "F5")...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	cmd.Stdin = bytes.NewReader(verbInput(verbRows(Project{Path: "m/reputation", Name: "reputation"}), icons))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(1500 * time.Millisecond)
	out := filepath.Join(dir, "verbs.png")
	screenshot(t, display, out)
	compareGolden(t, "rofi-verbs", out)
}

func TestGoldenReportDemo(t *testing.T) {
	e2e(t)
	needBins(t, "firefox")
	dir := t.TempDir()
	r := demoReport(ts("2026-09-07T14:32:07Z"), themes["dark"])
	f, err := os.Create(filepath.Join(dir, "report-30d.html"))
	if err != nil {
		t.Fatal(err)
	}
	if err := renderReport(f, r); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// The profile directory must exist: with a missing one firefox shows a
	// modal error and never exits (that looked like a hang from the outside).
	profile := filepath.Join(dir, "ffprof")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "report.png")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// No captured output: helper processes would keep the pipes open.
	cmd := exec.CommandContext(ctx, "firefox", "--headless", "--no-remote", "--profile", profile, "--window-size=1920,1080", "--screenshot", out, "file://"+f.Name())
	if err := cmd.Run(); err != nil {
		t.Fatalf("firefox: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("firefox wrote no screenshot: %v", err)
	}
	compareGolden(t, "report-demo", out)
}
