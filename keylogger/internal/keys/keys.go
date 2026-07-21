// Package keys turns raw Linux keycodes into meaning — the display character
// (Swedish layout) and the touch-typing finger/hand — from embedded JSON maps.
// Everything downstream (metrics, rules, report) resolves keycodes through here
// so the daemon itself stays layout-agnostic and stores only raw keycodes.
package keys

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
)

//go:embed data/layout-sv.json
var layoutJSON []byte

//go:embed data/fingers-qwerty.json
var fingersJSON []byte

// Well-known keycodes referenced by rules/metrics/seed by name.
const (
	Backspace = 14
	Enter     = 28
	Space     = 57
	Tab       = 15
	Esc       = 1
	LeftShift = 42
)

// Finger identifies a physical finger. Hand is "L" or "R".
type Finger struct {
	Hand   string `json:"hand"`
	Finger string `json:"finger"`
}

// Key is a single-hand.finger.char resolution of a keycode.
type Key struct {
	Char   string
	Finger Finger
}

type layoutEntry struct {
	Base  string `json:"base"`
	Shift string `json:"shift"`
}

var (
	layout  map[int]layoutEntry
	fingers map[int]Finger
)

func init() {
	if err := load(); err != nil {
		panic(fmt.Sprintf("keys: loading embedded maps: %v", err))
	}
}

func load() error {
	layout = map[int]layoutEntry{}
	if err := unmarshalKeyed(layoutJSON, func(kc int, raw json.RawMessage) error {
		var e layoutEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		layout[kc] = e
		return nil
	}); err != nil {
		return fmt.Errorf("layout: %w", err)
	}
	fingers = map[int]Finger{}
	if err := unmarshalKeyed(fingersJSON, func(kc int, raw json.RawMessage) error {
		var f Finger
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		fingers[kc] = f
		return nil
	}); err != nil {
		return fmt.Errorf("fingers: %w", err)
	}
	return nil
}

// unmarshalKeyed decodes a {"<keycode>": <obj>} map, skipping "_comment"-style
// string keys, and invokes fn for each numeric-keyed entry.
func unmarshalKeyed(data []byte, fn func(int, json.RawMessage) error) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		if k == "" || k[0] == '_' {
			continue
		}
		n, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		if err := fn(n, v); err != nil {
			return err
		}
	}
	return nil
}

// Char returns the display token for a keycode ("a", "Space", "å"). Unknown
// keycodes render as "0x<code>" so nothing is silently dropped from a report.
func Char(keycode int) string {
	if e, ok := layout[keycode]; ok && e.Base != "" {
		return e.Base
	}
	return fmt.Sprintf("kc%d", keycode)
}

// FingerOf returns the finger/hand for a keycode, and whether it is mapped.
func FingerOf(keycode int) (Finger, bool) {
	f, ok := fingers[keycode]
	return f, ok
}

// SameFinger reports whether two keycodes are struck by the same finger of the
// same hand — the basis of same-finger-bigram/skipgram detection. A keycode
// pressed twice in a row is same-finger by definition.
func SameFinger(kc1, kc2 int) bool {
	f1, ok1 := fingers[kc1]
	f2, ok2 := fingers[kc2]
	if !ok1 || !ok2 {
		return false
	}
	return f1 == f2
}

// nonText: keycodes that are mapped but are not text-producing (control/nav).
var nonText = map[int]bool{
	1: true, 14: true, 15: true, 28: true, 58: true, // Esc, Backspace, Tab, Enter, Caps
	103: true, 105: true, 106: true, 108: true, // arrows
}

// IsText reports whether a keycode produces text (letters, digits, punctuation,
// space) — i.e. it edits the buffer, as opposed to modifiers or control/nav
// keys. Used by the delete-and-replace mistype tracker.
func IsText(keycode int) bool {
	if _, ok := fingers[keycode]; !ok {
		return false // unmapped/function keys
	}
	return !IsModifier(keycode) && !nonText[keycode]
}

// IsModifier reports whether a keycode is a modifier key (so metrics can
// separate modifier load from typing load).
func IsModifier(keycode int) bool {
	switch keycode {
	case 29, 97, // ctrl
		42, 54, // shift
		56, 100, // alt / altgr
		125, 126: // super
		return true
	}
	return false
}
