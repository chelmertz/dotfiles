// Package report renders metrics + findings as a terminal digest or a
// self-contained HTML page (matching keylog-report-mock.html).
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/chelmertz/dotfiles/keylogger/internal/metrics"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
	"github.com/chelmertz/dotfiles/keylogger/internal/rules"
)

func sevTag(s rules.Severity) string {
	switch s {
	case rules.Critical:
		return "▲ CRITICAL"
	case rules.Optimise:
		return "◆ OPTIMISE"
	case rules.NoAction:
		return "● OK      "
	default:
		return "· INFO    "
	}
}

func bar(share, max float64, width int) string {
	if max <= 0 {
		return ""
	}
	n := int(share / max * float64(width))
	if n < 0 {
		n = 0
	}
	if n > width {
		n = width
	}
	return strings.Repeat("█", n)
}

func dur(s model.Session) string {
	end := s.EndedAt
	if end == 0 {
		return "running"
	}
	secs := end - s.StartedAt
	return fmt.Sprintf("%dh%02dm", secs/3600, (secs%3600)/60)
}

// Terminal writes a plain-text digest.
func Terminal(w io.Writer, s model.Session, m metrics.Metrics, fs []rules.Finding) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	if s.ID == 0 { // --all: lifetime view, no single duration
		p("keylog · all sessions (%s) · %s · %d keydowns\n", s.Note, s.Host, m.TotalKeydowns)
	} else {
		p("keylog · session #%d · %s · %s · %d keydowns\n", s.ID, s.Host, dur(s), m.TotalKeydowns)
	}
	if len(m.Devices) > 0 {
		var parts []string
		for _, d := range m.Devices {
			parts = append(parts, fmt.Sprintf("%s %s", nameOr(d.Device, "?"), pct(d.Share)))
		}
		p("devices: %s\n", strings.Join(parts, " · "))
	}

	p("\n── VERDICTS ─────────────────────────────────────────────\n")
	if len(fs) == 0 {
		p("  (no findings)\n")
	}
	for _, f := range fs {
		p("\n  %s  %s\n", sevTag(f.Severity), f.Title)
		for _, line := range wrap(f.Body, 66) {
			p("      %s\n", line)
		}
	}

	p("\n── KEY FREQUENCY (top 20) ───────────────────────────────\n")
	max := float64(0)
	if len(m.Keys) > 0 {
		max = m.Keys[0].Share
	}
	for i, k := range m.Keys {
		if i == 20 {
			break
		}
		dead := ""
		if k.Share < metrics.DeadThreshold {
			dead = "  (near-dead)"
		}
		p("  %-10s %-25s %5s%s\n", k.Char, bar(k.Share, max, 25), pct(k.Share), dead)
	}

	p("\n── PER-FINGER LOAD ──────────────────────────────────────\n")
	fmax := float64(0)
	for _, f := range m.Fingers {
		if f.Share > fmax {
			fmax = f.Share
		}
	}
	for _, f := range m.Fingers {
		p("  %s %-7s %-20s %5s\n", f.Hand, f.Finger, bar(f.Share, fmax, 20), pct(f.Share))
	}
	p("  hand balance: %s left / %s right\n", pct(m.HandBalanceL), pct(m.HandBalanceR))

	p("\n── SAME-FINGER BIGRAMS ──  %s of bigrams\n", pct(m.SFBPercent))
	for _, pr := range m.SFBTop {
		p("  %s%-6s %-10s %6d  %s\n", pr.Char1, pr.Char2, "", pr.Count, pr.Finger)
	}
	if m.SFSPercent > 0 {
		p("  same-finger skipgrams: %s\n", pct(m.SFSPercent))
	}

	if len(m.Mistypes) > 0 {
		p("\n── MOST-MISTYPED KEYS (delete-and-replace) ──────────────\n")
		for i, mk := range m.Mistypes {
			if i == 10 {
				break
			}
			p("  %-8s %4d mistypes  %5s of use   usually meant %s\n",
				mk.Char, mk.Count, pct(mk.Rate), mk.TopSub)
		}
	}

	if len(m.Contexts) > 0 {
		p("\n── BY CONTEXT ───────────────────────────────────────────\n")
		for _, c := range m.Contexts {
			var top []string
			for i, k := range c.TopKeys {
				if i == 5 {
					break
				}
				top = append(top, k.Char)
			}
			p("  %-24s %5s   %s\n", c.Label, pct(c.Share), strings.Join(top, " "))
		}
	}

	if len(m.Bindings) > 0 {
		p("\n── i3 BINDINGS ──────────────────────────────────────────\n")
		dead := 0
		for _, b := range m.Bindings {
			if b.InConfig && !b.Fired {
				dead++
				continue
			}
			p("  %-18s %6d\n", b.Combo, b.Count)
		}
		if dead > 0 {
			p("  (%d configured bindings never fired)\n", dead)
		}
	}
	p("\n")
}

func nameOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func pct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// wrap is a tiny greedy word-wrapper for terminal verdict bodies.
func wrap(s string, width int) []string {
	words := strings.Fields(s)
	var lines []string
	var cur string
	for _, w := range words {
		if cur == "" {
			cur = w
		} else if len(cur)+1+len(w) <= width {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
