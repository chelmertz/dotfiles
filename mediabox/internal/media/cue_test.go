package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCue(t *testing.T, body string, tracks ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tr := range tracks {
		if err := os.WriteFile(filepath.Join(dir, tr), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "game.cue")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCueTracksResolvesBesideTheCue(t *testing.T) {
	body := `FILE "Game (Track 1).bin" BINARY
  TRACK 01 MODE2/2352
    INDEX 01 00:00:00
FILE "Game (Track 2).bin" BINARY
  TRACK 02 AUDIO
    INDEX 01 00:00:00
`
	p := writeCue(t, body, "Game (Track 1).bin", "Game (Track 2).bin")
	got, err := CueTracks(p)
	if err != nil {
		t.Fatalf("CueTracks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tracks, want 2: %v", len(got), got)
	}
	for _, g := range got {
		if filepath.Dir(g) != filepath.Dir(p) {
			t.Errorf("%q is not beside the cue sheet", g)
		}
	}
}

func TestCueTracksHandlesUnquotedNames(t *testing.T) {
	p := writeCue(t, "FILE game.bin BINARY\n  TRACK 01 MODE1/2352\n", "game.bin")
	got, err := CueTracks(p)
	if err != nil {
		t.Fatalf("CueTracks: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "game.bin" {
		t.Errorf("got %v, want one game.bin", got)
	}
}

// A cue sheet is just text, so its FILE lines are attacker-controlled in
// exactly the way an archive's entry names are.
func TestCueTracksRefusesEscapingPaths(t *testing.T) {
	for _, name := range []string{"../../etc/passwd", "/etc/passwd", "sub/dir/game.bin"} {
		p := writeCue(t, "FILE \""+name+"\" BINARY\n  TRACK 01 MODE1/2352\n")
		_, err := CueTracks(p)
		if err == nil {
			t.Errorf("%q was accepted", name)
			continue
		}
		if !Rejected(err) {
			t.Errorf("%q: want a rejection, got %v", name, err)
		}
	}
}

func TestCueTracksRefusesEmptyCue(t *testing.T) {
	p := writeCue(t, "REM nothing here\n")
	if _, err := CueTracks(p); !Rejected(err) {
		t.Fatalf("want a rejection, got %v", err)
	}
}

func TestIsCueSheet(t *testing.T) {
	yes := []byte("FILE \"game.bin\" BINARY\n  TRACK 01 MODE2/2352\n")
	if !IsCueSheet(yes) {
		t.Error("a real cue sheet was not recognised")
	}
	for _, no := range []string{"", "hello world", "FILE a report about nothing"} {
		if IsCueSheet([]byte(no)) {
			t.Errorf("%q was taken for a cue sheet", no)
		}
	}
}

func TestCueDetailMentionsSuggestion(t *testing.T) {
	id := DiscBySize(650 * mb)
	if !strings.Contains(id.Detail, "PS1") {
		t.Errorf("detail = %q, want it to explain the suggestion", id.Detail)
	}
}
