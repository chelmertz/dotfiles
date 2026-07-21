// Package i3cfg parses `bindsym` lines from an i3 config into the modmask+keycode
// form the metrics use, so the report can flag configured-but-never-fired
// bindings. It is deliberately partial: common keys and modifiers only; unknown
// specs are skipped (and counted by the caller if it cares).
package i3cfg

import (
	"bufio"
	"os"
	"strings"

	"github.com/chelmertz/dotfiles/keylogger/internal/metrics"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

var keyName = buildKeyNames()

func buildKeyNames() map[string]int {
	m := map[string]int{
		"return": 28, "space": 57, "escape": 1, "esc": 1, "tab": 15,
		"backspace": 14, "minus": 53, "plus": 12, "comma": 51, "period": 52,
		"slash": 53, "up": 103, "down": 108, "left": 105, "right": 106,
	}
	for i, ch := range "qwertyuiop" {
		m[string(ch)] = 16 + i
	}
	for i, ch := range "asdfghjkl" {
		m[string(ch)] = 30 + i
	}
	for i, ch := range "zxcvbnm" {
		m[string(ch)] = 44 + i
	}
	for i, ch := range "1234567890" {
		m[string(ch)] = 2 + i
	}
	return m
}

// Parse reads an i3 config file and returns its bindings.
func Parse(path string) ([]metrics.Binding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parse(f), nil
}

func parse(r interface{ Read([]byte) (int, error) }) []metrics.Binding {
	var out []metrics.Binding
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "bindsym ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		spec := fields[1]
		if strings.HasPrefix(spec, "--") { // e.g. --release; skip flag, take next
			if len(fields) < 3 {
				continue
			}
			spec = fields[2]
		}
		if b, ok := parseSpec(spec); ok {
			out = append(out, b)
		}
	}
	return out
}

func parseSpec(spec string) (metrics.Binding, bool) {
	parts := strings.Split(spec, "+")
	var mod int
	var kc int
	var found bool
	for _, p := range parts {
		switch strings.ToLower(p) {
		case "$mod", "mod4", "super":
			mod |= model.ModSuper
		case "mod1", "alt":
			mod |= model.ModAlt
		case "shift":
			mod |= model.ModShift
		case "control", "ctrl":
			mod |= model.ModCtrl
		default:
			if code, ok := keyName[strings.ToLower(p)]; ok {
				kc = code
				found = true
			}
		}
	}
	if !found {
		return metrics.Binding{}, false
	}
	return metrics.Binding{Keycode: kc, Modmask: mod, Combo: combo(spec)}, true
}

// combo renders a display string, normalising $mod to "Mod".
func combo(spec string) string {
	s := strings.ReplaceAll(spec, "$mod", "Mod")
	s = strings.ReplaceAll(s, "Mod4", "Mod")
	return s
}
