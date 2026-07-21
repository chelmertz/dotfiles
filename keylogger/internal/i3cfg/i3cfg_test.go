package i3cfg

import (
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

func TestParseBindings(t *testing.T) {
	cfg := `
# comment
set $mod Mod4
bindsym $mod+Return exec alacritty
bindsym $mod+Shift+q kill
bindsym $mod+j focus left
bindsym --release $mod+p exec foo
bindsym $mod+F13 nop
`
	bs := parse(strings.NewReader(cfg))
	if len(bs) != 4 {
		t.Fatalf("got %d bindings, want 4 (F13 unknown skipped): %+v", len(bs), bs)
	}
	// find Return
	var ret *struct {
		kc, mod int
	}
	_ = ret
	got := map[string]int{}
	for _, b := range bs {
		got[b.Combo] = b.Keycode
	}
	if got["Mod+Return"] != 28 {
		t.Errorf("Mod+Return keycode = %d, want 28", got["Mod+Return"])
	}
	// shift modifier parsed
	for _, b := range bs {
		if b.Combo == "Mod+Shift+q" {
			if b.Modmask&model.ModShift == 0 || b.Modmask&model.ModSuper == 0 {
				t.Errorf("Mod+Shift+q modmask = %d, want Super|Shift", b.Modmask)
			}
			if b.Keycode != 16 { // q
				t.Errorf("q keycode = %d, want 16", b.Keycode)
			}
		}
	}
}
