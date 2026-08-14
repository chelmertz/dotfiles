package box

import (
	"slices"
	"strings"
	"testing"
)

func TestDestOnlyResolvesKnownSystems(t *testing.T) {
	for _, sys := range []string{"snes", "nes", "gb", "gba", "genesis", "n64", "ps1", "psp", "dreamcast", "movies"} {
		d, err := Dest(sys)
		if err != nil {
			t.Errorf("%s: %v", sys, err)
			continue
		}
		if !strings.HasPrefix(d, MediaRoot+"/") && !strings.HasPrefix(d, ROMRoot+"/") {
			t.Errorf("%s: destination %q is outside the allowed roots", sys, d)
		}
	}
}

// Anything unidentified has nowhere to go, which is what makes deny-by-default
// reach all the way to the filesystem rather than stopping at identification.
func TestDestRefusesUnknownSystems(t *testing.T) {
	for _, sys := range []string{"", "unknown", "../../etc", "/etc/cron.d", "snes/../.."} {
		if _, err := Dest(sys); err == nil {
			t.Errorf("%q resolved to a destination", sys)
		}
	}
}

// rsync lists only what it would still change. A file that already matches by
// checksum produces no line at all, and that silence is the proof of a good copy.
func TestParseItemizedFindsOnlyRealDifferences(t *testing.T) {
	// The leading character is what matters. "<", ">" and "c" all mean data
	// would move, so the copy is not identical. "." means no transfer — the
	// contents already match and only metadata differs, which is exactly the
	// case that must not hold a file back from deletion.
	out := strings.Join([]string{
		`>f+++++++++ New Game.sfc`,
		`.f..t...... Same Content Newer Mtime.sfc`,
		`cd+++++++++ somedir/`,
		`>f.st...... Changed.mkv`,
		`.f          Already Matching.sfc`,
		`cf+++++++++ Copied.bin`,
		``,
	}, "\n")

	got := parseItemized(out)
	want := []string{"New Game.sfc", "Changed.mkv", "Copied.bin"}

	if !slices.Equal(got, want) {
		t.Errorf("parseItemized = %v, want %v", got, want)
	}
	if slices.Contains(got, "Same Content Newer Mtime.sfc") {
		t.Error("a timestamp-only difference was treated as a content mismatch")
	}
	if slices.Contains(got, "somedir/") {
		t.Error("a directory was reported as a differing file")
	}
}

func TestParseItemizedEmptyMeansVerified(t *testing.T) {
	if got := parseItemized(""); len(got) != 0 {
		t.Errorf("no output should mean nothing differs, got %v", got)
	}
}

func TestShortStripsDataPrefix(t *testing.T) {
	if got := Short(ROMRoot + "/snes"); got != "games/roms/snes" {
		t.Errorf("Short = %q, want games/roms/snes", got)
	}
}
