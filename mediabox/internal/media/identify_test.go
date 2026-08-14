package media

import (
	"bytes"
	"strings"
	"testing"
)

// idOf runs the ladder over an in-memory file. Going through the unexported
// core rather than Identify is what lets a case claim a size it does not
// occupy, which is how the absurd-size rejections are tested without writing
// four gigabytes to disk.
func idOf(t *testing.T, name string, content []byte, size int64) (ID, error) {
	t.Helper()
	head := content
	if len(head) > headLen {
		head = head[:headLen]
	}
	if size == 0 {
		size = int64(len(content))
	}
	return identify(bytes.NewReader(content), head, size, name)
}

func TestProbesIdentifyBySignature(t *testing.T) {
	// The disc cases carry an explicit size: the fixtures are a few blocks
	// long, and a real disc image that small would rightly be refused.
	cases := []struct {
		name    string
		file    string
		content []byte
		size    int64
		system  string
		kind    Kind
		detail  string
	}{
		{"iNES", "game.nes", nesROM(), 0, "nes", KindROM, "iNES"},
		{"N64 big-endian", "game.z64", n64ROM(0x80371240), 0, "n64", KindROM, "big-endian"},
		{"N64 byteswapped", "game.v64", n64ROM(0x37804012), 0, "n64", KindROM, "byteswapped"},
		{"N64 little-endian", "game.n64", n64ROM(0x40123780), 0, "n64", KindROM, "little-endian"},
		{"Game Boy", "game.gb", gbROM(false), 0, "gb", KindROM, "Game Boy header"},
		{"Game Boy Color", "game.gbc", gbROM(true), 0, "gb", KindROM, "Color"},
		{"GBA", "game.gba", gbaROM(), 0, "gba", KindROM, "0x96"},
		{"Genesis", "game.md", genesisROM(), 0, "genesis", KindROM, "0x100"},
		{"Master System", "game.sms", smsROM(), 0, "genesis", KindROM, "TMR SEGA"},
		{"SNES", "game.sfc", snesROM(true), 0, "snes", KindROM, "checksum/complement verified"},
		{"Matroska", "film.mkv", mkvFile(), 0, "movies", KindVideo, "Matroska"},
		{"MP4", "film.mp4", mp4File(), 0, "movies", KindVideo, "ISO base media"},
		{"SubRip", "film.srt", srtFile(), 0, "movies", KindSubtitle, "SubRip"},
		{"PS1 disc", "game.bin", ps1ISO(), 650 * mb, "ps1", KindDisc, "SLUS"},
		{"PSP disc", "game.iso", pspISO(), 1500 * mb, "psp", KindDisc, "UMD_DATA.BIN"},
		{"PSP via BOOT2", "game.iso", psxCNFasPSP(), 1500 * mb, "psp", KindDisc, "BOOT2"},
		{"Dreamcast", "game.iso", dreamcastImage(), 1200 * mb, "dreamcast", KindDisc, "SEGAKATANA"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, err := idOf(t, c.file, c.content, c.size)
			if err != nil {
				t.Fatalf("identify: %v", err)
			}
			if id.System != c.system {
				t.Errorf("system = %q, want %q (detail: %s)", id.System, c.system, id.Detail)
			}
			if id.Kind != c.kind {
				t.Errorf("kind = %v, want %v", id.Kind, c.kind)
			}
			if id.Conf != ConfStrong {
				t.Errorf("confidence = %v, want strong", id.Conf)
			}
			if !strings.Contains(id.Detail, c.detail) {
				t.Errorf("detail = %q, want it to mention %q", id.Detail, c.detail)
			}
		})
	}
}

// The name is never evidence. A .mkv that is really a SNES cartridge is routed
// as a cartridge, and the same bytes under any name reach the same verdict.
func TestExtensionDoesNotDecide(t *testing.T) {
	for _, name := range []string{"film.mkv", "notes.txt", "game.iso", "noextension"} {
		id, err := idOf(t, name, snesROM(true), 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if id.System != "snes" {
			t.Errorf("%s: system = %q, want snes", name, id.System)
		}
	}
}

func TestDangerGateRefusesExecutables(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		content []byte
	}{
		{"ELF", "game.sfc", append([]byte("\x7fELF\x02\x01\x01"), make([]byte, 4096)...)},
		{"PE", "film.mkv", append([]byte("MZ\x90\x00"), make([]byte, 4096)...)},
		{"Mach-O", "game.nes", append([]byte{0xFE, 0xED, 0xFA, 0xCF}, make([]byte, 4096)...)},
		{"shebang", "game.gb", []byte("#!/bin/sh\nrm -rf /\n")},
		{"OLE/MSI", "game.iso", append([]byte{0xD0, 0xCF, 0x11, 0xE0}, make([]byte, 4096)...)},
		{"Java class", "game.n64", append([]byte{0xCA, 0xFE, 0xBA, 0xBE}, make([]byte, 4096)...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := idOf(t, c.file, c.content, 0)
			if err == nil {
				t.Fatal("accepted an executable")
			}
			if !Rejected(err) {
				t.Fatalf("want a rejection, got %v", err)
			}
			if !strings.Contains(err.Error(), "at the door") {
				t.Errorf("error = %q, want it to name the gate", err)
			}
		})
	}
}

// The double-extension trick: real video bytes, but the last extension says
// executable. The extension blocklist is what catches this, which is why it
// runs regardless of content.
func TestDangerGateRefusesExecutableExtensions(t *testing.T) {
	for _, name := range []string{"Arrival.mkv.exe", "setup.msi", "run.sh", "x.dll", "a.desktop"} {
		_, err := idOf(t, name, mkvFile(), 0)
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if !Rejected(err) {
			t.Fatalf("%s: want a rejection, got %v", name, err)
		}
	}
}

func TestUnrecognisedContentIsRefused(t *testing.T) {
	cases := []struct{ name, file string }{
		{"random bytes", "mystery.mkv"},
		{"plain text", "readme.txt"},
		{"no extension", "blob"},
	}
	junk := bytes.Repeat([]byte{0x41, 0x7A, 0x00, 0x9C}, 1024)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := idOf(t, c.file, junk, 0)
			if !Rejected(err) {
				t.Fatalf("want a rejection, got %v", err)
			}
		})
	}
}

// A SNES ROM whose checksum does not match its complement is not identified by
// content. It falls back to the extension, which is honest about being weaker.
func TestSNESChecksumIsActuallyChecked(t *testing.T) {
	id, err := idOf(t, "game.sfc", snesROM(false), 0)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if id.Conf != ConfWeak {
		t.Errorf("confidence = %v, want weak — a broken checksum is not evidence", id.Conf)
	}

	// Under a name with no fallback, the same bytes are refused outright.
	if _, err := idOf(t, "game.rom", snesROM(false), 0); !Rejected(err) {
		t.Errorf("want a rejection without a fallback extension, got %v", err)
	}
}

// The case that motivated the size ladder: a valid SNES header on a file far
// too large to be a cartridge means something is wrong with the file.
func TestAbsurdSizeIsRefused(t *testing.T) {
	_, err := idOf(t, "game.sfc", snesROM(true), 4<<30)
	if !Rejected(err) {
		t.Fatalf("want a rejection for a 4G SNES ROM, got %v", err)
	}
	if !strings.Contains(err.Error(), "snes") {
		t.Errorf("error = %q, want it to name the system", err)
	}

	if _, err := idOf(t, "game.nes", nesROM(), 1); !Rejected(err) {
		t.Errorf("want a rejection for a 1-byte NES ROM, got %v", err)
	}
}

func TestPlausibleSizePasses(t *testing.T) {
	if _, err := idOf(t, "game.sfc", snesROM(true), 4<<20); err != nil {
		t.Fatalf("4M SNES ROM should be fine: %v", err)
	}
}

func TestDiscBySizeSuggests(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{650 * mb, "ps1"},
		{1200 * mb, "dreamcast"},
		{1800 * mb, "psp"},
	}
	for _, c := range cases {
		got := DiscBySize(c.size)
		if got.System != c.want {
			t.Errorf("%s: system = %q, want %q", humanSize(c.size), got.System, c.want)
		}
		if got.Conf != ConfWeak {
			t.Errorf("%s: suggestion should stay weak so the user is still asked", humanSize(c.size))
		}
	}
}
