package media

import (
	"bytes"
	"encoding/binary"
	"io"
)

// A probe looks for evidence and says nothing when it finds none. Order
// matters only where two probes could both fire; discs go last because their
// checks are the most expensive.
type probe func(r io.ReaderAt, head []byte, size int64) (ID, bool)

var probes = []probe{
	probeNES,
	probeN64,
	probeGameBoy,
	probeGBA,
	probeGenesis,
	probeMasterSystem,
	probeSNES,
	probeVideo,
	probeSubtitle,
	probeDisc,
}

func probeNES(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	if !bytes.HasPrefix(head, []byte("NES\x1a")) {
		return ID{}, false
	}
	return ID{System: "nes", Kind: KindROM, Detail: "iNES header"}, true
}

func probeN64(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	if len(head) < 4 {
		return ID{}, false
	}
	// The same ROM ships in three byte orders. Naming which one is useful:
	// some cores only take the big-endian form.
	switch binary.BigEndian.Uint32(head[:4]) {
	case 0x80371240:
		return ID{System: "n64", Kind: KindROM, Detail: "N64 ROM, big-endian (z64)"}, true
	case 0x37804012:
		return ID{System: "n64", Kind: KindROM, Detail: "N64 ROM, byteswapped (v64)"}, true
	case 0x40123780:
		return ID{System: "n64", Kind: KindROM, Detail: "N64 ROM, little-endian (n64)"}, true
	}
	return ID{}, false
}

// gbLogo is the 48-byte Nintendo logo the boot ROM compares against. A Game Boy
// cartridge that does not carry it verbatim does not boot, so it is a reliable
// signature rather than a convention.
var gbLogo = []byte{
	0xCE, 0xED, 0x66, 0x66, 0xCC, 0x0D, 0x00, 0x0B, 0x03, 0x73, 0x00, 0x83,
	0x00, 0x0C, 0x00, 0x0D, 0x00, 0x08, 0x11, 0x1F, 0x88, 0x89, 0x00, 0x0E,
	0xDC, 0xCC, 0x6E, 0xE6, 0xDD, 0xDD, 0xD9, 0x99, 0xBB, 0xBB, 0x67, 0x63,
	0x6E, 0x0E, 0xEC, 0xCC, 0xDD, 0xDC, 0x99, 0x9F, 0xBB, 0xB9, 0x33, 0x3E,
}

func probeGameBoy(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	if len(head) < 0x150 || !bytes.Equal(head[0x104:0x134], gbLogo) {
		return ID{}, false
	}
	// 0x80 and 0xC0 mark colour support; both belong with the GBC cores.
	if c := head[0x143]; c == 0x80 || c == 0xC0 {
		return ID{System: "gb", Kind: KindROM, Detail: "Game Boy Color header (Nintendo logo verified)"}, true
	}
	return ID{System: "gb", Kind: KindROM, Detail: "Game Boy header (Nintendo logo verified)"}, true
}

// gbaLogoPrefix is the start of the GBA's own 156-byte logo. The prefix plus
// the fixed byte at 0xB2 is already far past coincidence.
var gbaLogoPrefix = []byte{
	0x24, 0xFF, 0xAE, 0x51, 0x69, 0x9A, 0xA2, 0x21,
	0x3D, 0x84, 0x82, 0x0A, 0x84, 0xE4, 0x09, 0xAD,
}

func probeGBA(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	if len(head) < 0xC0 {
		return ID{}, false
	}
	if !bytes.HasPrefix(head[0x04:], gbaLogoPrefix) || head[0xB2] != 0x96 {
		return ID{}, false
	}
	return ID{System: "gba", Kind: KindROM, Detail: "GBA header (Nintendo logo + fixed byte 0x96)"}, true
}

func probeGenesis(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	if len(head) < 0x110 {
		return ID{}, false
	}
	if !bytes.HasPrefix(head[0x100:], []byte("SEGA")) {
		return ID{}, false
	}
	return ID{System: "genesis", Kind: KindROM, Detail: "Mega Drive/Genesis header at 0x100"}, true
}

func probeMasterSystem(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	// The header floats between three slots depending on cartridge size.
	for _, off := range []int{0x1FF0, 0x3FF0, 0x7FF0} {
		if len(head) >= off+8 && bytes.Equal(head[off:off+8], []byte("TMR SEGA")) {
			return ID{System: "genesis", Kind: KindROM, Detail: "Master System/Game Gear header (TMR SEGA)"}, true
		}
	}
	return ID{}, false
}

// probeSNES is the one file(1) cannot do. A SNES cartridge has no magic number,
// but its internal header carries a checksum and that checksum's complement,
// and the two must XOR to 0xFFFF. That identity plus a printable title is
// evidence, and it names the memory mapping as a bonus.
func probeSNES(r io.ReaderAt, _ []byte, size int64) (ID, bool) {
	type layout struct {
		base int64
		name string
	}
	layouts := []layout{{0x7FC0, "LoROM"}, {0xFFC0, "HiROM"}, {0x40FFC0, "ExHiROM"}}

	// A 512-byte copier header shifts everything along. Its presence is what
	// makes the file size an odd multiple of 512 rather than a clean 1024.
	var offsets []int64
	if size%1024 == 512 {
		offsets = []int64{512}
	} else {
		offsets = []int64{0}
	}

	for _, shift := range offsets {
		for _, l := range layouts {
			buf := make([]byte, 32)
			if _, err := r.ReadAt(buf, l.base+shift); err != nil {
				continue
			}
			complement := binary.LittleEndian.Uint16(buf[0x1C:0x1E])
			checksum := binary.LittleEndian.Uint16(buf[0x1E:0x20])
			if checksum^complement != 0xFFFF {
				continue
			}
			// Guard against a run of zeroes satisfying the identity by accident.
			if checksum == 0 || checksum == 0xFFFF || !printableTitle(buf[:21]) {
				continue
			}
			d := "SNES " + l.name + " header, checksum/complement verified"
			if shift != 0 {
				d += " (512-byte copier header)"
			}
			return ID{System: "snes", Kind: KindROM, Detail: d}, true
		}
	}
	return ID{}, false
}

func printableTitle(b []byte) bool {
	printable := 0
	for _, c := range b {
		if c >= 0x20 && c <= 0x7E {
			printable++
		}
	}
	// Titles are space-padded ASCII; allow a few odd bytes for regional sets.
	return printable >= len(b)-4
}

// videoSigs identify a container. Everything here routes to movies, so the
// codec inside is irrelevant — only "is this really a video container".
func probeVideo(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	switch {
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return ID{System: "movies", Kind: KindVideo, Detail: "Matroska/WebM container"}, true
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		return ID{System: "movies", Kind: KindVideo, Detail: "ISO base media container (mp4/mov/m4v)"}, true
	case len(head) >= 12 && bytes.HasPrefix(head, []byte("RIFF")) && bytes.Equal(head[8:12], []byte("AVI ")):
		return ID{System: "movies", Kind: KindVideo, Detail: "AVI container"}, true
	case bytes.HasPrefix(head, []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11}):
		return ID{System: "movies", Kind: KindVideo, Detail: "ASF/WMV container"}, true
	case bytes.HasPrefix(head, []byte("FLV\x01")):
		return ID{System: "movies", Kind: KindVideo, Detail: "FLV container"}, true
	case bytes.HasPrefix(head, []byte{0x00, 0x00, 0x01, 0xBA}):
		return ID{System: "movies", Kind: KindVideo, Detail: "MPEG program stream"}, true
	case isMPEGTS(head):
		return ID{System: "movies", Kind: KindVideo, Detail: "MPEG transport stream"}, true
	}
	return ID{}, false
}

// isMPEGTS checks the 0x47 sync byte repeats on the 188-byte packet grid. One
// 0x47 proves nothing; four in a row on the grid is a stream.
func isMPEGTS(head []byte) bool {
	if len(head) < 188*3+1 {
		return false
	}
	for i := range 4 {
		if head[i*188] != 0x47 {
			return false
		}
	}
	return true
}

// probeSubtitle accepts the few text formats worth carrying next to a film.
// Text that is not one of these is left to be refused: deny by default means
// an unrecognised text file is not "probably fine".
func probeSubtitle(_ io.ReaderAt, head []byte, _ int64) (ID, bool) {
	t := head
	// Tolerate a UTF-8 BOM, which players and editors add freely.
	t = bytes.TrimPrefix(t, []byte{0xEF, 0xBB, 0xBF})
	switch {
	case bytes.HasPrefix(t, []byte("WEBVTT")):
		return ID{System: "movies", Kind: KindSubtitle, Detail: "WebVTT subtitles"}, true
	case bytes.Contains(head, []byte("[Script Info]")):
		return ID{System: "movies", Kind: KindSubtitle, Detail: "SubStation Alpha subtitles"}, true
	case looksLikeSRT(t):
		return ID{System: "movies", Kind: KindSubtitle, Detail: "SubRip subtitles"}, true
	}
	return ID{}, false
}

// looksLikeSRT keys off the timing arrow, which no other text format uses and
// which must appear in the first cue of a valid file.
func looksLikeSRT(t []byte) bool {
	limit := min(len(t), 4096)
	return bytes.Contains(t[:limit], []byte(" --> "))
}
