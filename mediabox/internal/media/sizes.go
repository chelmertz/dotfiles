package media

import "fmt"

const (
	kb = 1 << 10
	mb = 1 << 20
	gb = 1 << 30
)

// sizeWindow is what a system's files plausibly weigh. It is used two ways:
// to refuse an absurdity (a 4 GB file claiming to be a SNES cartridge), and to
// suggest a system for a disc image that carries no readable filesystem.
type sizeWindow struct {
	min, max int64
}

var sizeWindows = map[string]sizeWindow{
	"nes":       {8 * kb, 4 * mb},
	"snes":      {32 * kb, 8 * mb},
	"gb":        {32 * kb, 8 * mb},
	"gba":       {128 * kb, 64 * mb},
	"n64":       {1 * mb, 96 * mb},
	"genesis":   {8 * kb, 16 * mb},
	"ps1":       {1 * mb, 900 * mb},
	"dreamcast": {1 * mb, 2 * gb},
	"psp":       {1 * mb, 2 * gb},
	"movies":    {1, 200 * gb},
}

// checkSize refuses a verdict whose size makes no sense. A positive content
// match is strong evidence, but a SNES header on a 4 GB file means something is
// wrong with the file, not that we found a very large cartridge.
func checkSize(system string, size int64) error {
	w, ok := sizeWindows[system]
	if !ok {
		return nil
	}
	if size < w.min {
		return reject("identified as %s but only %s — too small to be real (expected at least %s)",
			system, humanSize(size), humanSize(w.min))
	}
	if size > w.max {
		return reject("identified as %s but %s — far past the %s a %s file can be",
			system, humanSize(size), humanSize(w.max), system)
	}
	return nil
}

// fallbackExts are the formats with no header worth checking, where the
// extension plus a plausible size is genuinely the best available evidence.
// Everything with a real signature is deliberately absent: if a .mkv fails the
// container probe it is not a .mkv, and saying so is the point.
var fallbackExts = map[string]struct {
	system string
	kind   Kind
	why    string
}{
	"sfc": {"snes", KindROM, "raw SNES cartridge dump (no verifiable header)"},
	"smc": {"snes", KindROM, "raw SNES cartridge dump (no verifiable header)"},
	"gg":  {"genesis", KindROM, "raw Game Gear dump (no header)"},
	"sms": {"genesis", KindROM, "raw Master System dump (no header)"},
	"gdi": {"dreamcast", KindDisc, "Dreamcast GD-ROM track index"},
	"cdi": {"dreamcast", KindDisc, "DiscJuggler image"},
	"cso": {"psp", KindDisc, "compressed PSP image"},
	"chd": {"", KindDisc, "MAME compressed hunks"},
}

func fallback(ext string, size int64) (ID, bool) {
	e, ok := fallbackExts[ext]
	if !ok {
		return ID{}, false
	}
	// A .chd can hold any disc; size is all we have to go on.
	if e.system == "" {
		sys, why := discBySize(size)
		return ID{System: sys, Kind: e.kind, Conf: ConfWeak, Detail: e.why + ", " + why}, true
	}
	return ID{System: e.system, Kind: e.kind, Conf: ConfWeak, Detail: e.why}, true
}

// discBySize suggests a console from how much a disc holds: a CD tops out
// around 700 MB, a Dreamcast GD-ROM holds about 1.2 GB, a PSP UMD about 1.8 GB.
// This only ever produces a suggestion — the user is still asked.
func discBySize(size int64) (string, string) {
	switch {
	case size <= 800*mb:
		return "ps1", fmt.Sprintf("%s fits a CD, suggesting PS1", humanSize(size))
	case size <= 1400*mb:
		return "dreamcast", fmt.Sprintf("%s is GD-ROM sized, suggesting Dreamcast", humanSize(size))
	default:
		return "psp", fmt.Sprintf("%s is UMD sized, suggesting PSP", humanSize(size))
	}
}

// DiscSystems are the consoles a disc image could belong to on this box, in the
// order the prompt offers them.
var DiscSystems = []string{"ps1", "psp", "dreamcast"}

// DiscBySize is the verdict for a disc image with no readable filesystem —
// raw cue/bin tracks, mostly. It is always weak, so the user is still asked;
// the size only decides which option is offered as the default.
func DiscBySize(size int64) ID {
	sys, why := discBySize(size)
	return ID{System: sys, Kind: KindDisc, Conf: ConfWeak, Detail: why, Size: size}
}

func humanSize(n int64) string {
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1fG", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%.0fM", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.0fK", float64(n)/kb)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// HumanSize renders a byte count for display.
func HumanSize(n int64) string { return humanSize(n) }
