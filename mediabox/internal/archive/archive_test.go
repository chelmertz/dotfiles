package archive

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type member struct {
	name    string
	body    []byte
	mode    fs.FileMode
	setMode bool
}

// writeZip builds an archive honestly: sizes and checksums are whatever the
// contents really are.
func writeZip(t *testing.T, members []member) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, m := range members {
		fh := &zip.FileHeader{Name: m.name, Method: zip.Deflate}
		if m.setMode {
			fh.SetMode(m.mode)
		}
		f, err := w.CreateHeader(fh)
		if err != nil {
			t.Fatalf("create %q: %v", m.name, err)
		}
		if _, err := f.Write(m.body); err != nil {
			t.Fatalf("write %q: %v", m.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return writeTemp(t, "clean-*.zip", buf.Bytes())
}

// writeForgedZip builds an archive whose index lies about how much a member
// expands to. The stream really does inflate to len(body); only the declared
// size is false. CreateRaw is what makes this possible — it takes the sizes on
// trust instead of computing them, exactly as a hostile archive does.
func writeForgedZip(t *testing.T, name string, body []byte, declared uint64) string {
	t.Helper()
	var deflated bytes.Buffer
	fw, err := flate.NewWriter(&deflated, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
	fh.UncompressedSize64 = declared
	fh.CompressedSize64 = uint64(deflated.Len())
	fh.CRC32 = crc32.ChecksumIEEE(body)
	f, err := w.CreateRaw(fh)
	if err != nil {
		t.Fatalf("CreateRaw: %v", err)
	}
	if _, err := f.Write(deflated.Bytes()); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return writeTemp(t, "forged-*.zip", buf.Bytes())
}

func writeTemp(t *testing.T, pattern string, b []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), pattern)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func rom(n int) []byte { return bytes.Repeat([]byte{0xA5}, n) }

// Every one of these has been a real way out of an extractor at some point.
func TestInspectRefusesHostileNames(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  string
	}{
		{"parent traversal", "../evil.sfc", "climbs out"},
		{"nested traversal", "a/b/../../../evil.sfc", "climbs out"},
		{"absolute path", "/etc/cron.d/evil.sfc", "absolute path"},
		{"backslash separator", `..\..\evil.sfc`, "backslash"},
		{"drive letter", "C:evil.sfc", "drive letter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeZip(t, []member{{name: c.entry, body: rom(64)}})
			_, err := Inspect(p, DefaultLimits)
			if err == nil {
				t.Fatal("accepted a hostile entry name")
			}
			if !Rejected(err) {
				t.Fatalf("want a rejection, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestInspectRefusesSymlink(t *testing.T) {
	p := writeZip(t, []member{
		{name: "game.sfc", body: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777, setMode: true},
	})
	_, err := Inspect(p, DefaultLimits)
	if !Rejected(err) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want a symlink rejection, got %v", err)
	}
}

func TestInspectRefusesSetuid(t *testing.T) {
	p := writeZip(t, []member{
		{name: "game.sfc", body: rom(64), mode: fs.ModeSetuid | 0o755, setMode: true},
	})
	_, err := Inspect(p, DefaultLimits)
	if !Rejected(err) || !strings.Contains(err.Error(), "setuid") {
		t.Fatalf("want a setuid rejection, got %v", err)
	}
}

// A decompression bomb is caught from the index alone, so nothing is written.
func TestInspectRefusesDecompressionBomb(t *testing.T) {
	// A million to one, which is the order real bombs work at.
	p := writeForgedZip(t, "bomb.sfc", rom(1024), 1<<30)
	_, err := Inspect(p, DefaultLimits)
	if !Rejected(err) {
		t.Fatalf("want a rejection, got %v", err)
	}
	if !strings.Contains(err.Error(), "bomb") {
		t.Errorf("error = %q, want it to name the bomb", err)
	}
}

// The other side of that threshold, and the reason it cannot be tight: a
// cartridge ROM is mostly padding, so an ordinary game archive compresses
// around 1000:1. Refusing those would make the archive support useless.
func TestInspectAcceptsPaddedROM(t *testing.T) {
	// 4M of zeroes with a header in it, which is what a real ROM looks like
	// to a compressor.
	padded := make([]byte, 4<<20)
	copy(padded[0x7FC0:], "LEGIT PADDED GAME")

	p := writeZip(t, []member{{name: "game.sfc", body: padded}})
	in, err := Inspect(p, DefaultLimits)
	if err != nil {
		t.Fatalf("a legitimately padded ROM was refused: %v", err)
	}
	if len(in.Payload) != 1 {
		t.Fatalf("payload = %d, want 1", len(in.Payload))
	}

	dir, files, err := Extract(in, DefaultLimits)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer os.RemoveAll(dir)
	if st, err := os.Stat(files[0].Path); err != nil || st.Size() != int64(len(padded)) {
		t.Errorf("extracted size = %v (err %v), want %d", st.Size(), err, len(padded))
	}
}

// The absolute cap, not the ratio, is what actually bounds how much can land
// on disk. It applies even when the ratio is entirely ordinary.
func TestTotalCapAppliesRegardlessOfRatio(t *testing.T) {
	lim := DefaultLimits
	lim.MaxTotalBytes = 2 << 20
	p := writeZip(t, []member{
		{name: "a.sfc", body: rom(1 << 20)},
		{name: "b.sfc", body: rom(1 << 20)},
		{name: "c.sfc", body: rom(1 << 20)},
	})
	if _, err := Inspect(p, lim); !Rejected(err) {
		t.Fatalf("want a rejection over the total cap, got %v", err)
	}
}

func TestInspectRefusesTooManyEntries(t *testing.T) {
	var ms []member
	for i := range 40 {
		ms = append(ms, member{name: string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".sfc", body: rom(8)})
	}
	lim := DefaultLimits
	lim.MaxEntries = 10
	if _, err := Inspect(writeZip(t, ms), lim); !Rejected(err) {
		t.Fatalf("want a rejection over the entry cap, got %v", err)
	}
}

func TestInspectRefusesOversizedTotal(t *testing.T) {
	lim := DefaultLimits
	lim.MaxTotalBytes = 1024
	p := writeZip(t, []member{{name: "a.sfc", body: rom(4096)}})
	if _, err := Inspect(p, lim); !Rejected(err) {
		t.Fatalf("want a rejection over the total cap, got %v", err)
	}
}

// An index that understates a file's size gets no further than extraction.
// archive/zip happens to catch this first — its checksumReader refuses to
// return more bytes than the header declared — so the assertion here is the
// property that matters rather than the specific message: nothing is written
// and nothing is left behind.
func TestExtractRefusesLyingSize(t *testing.T) {
	p := writeForgedZip(t, "liar.sfc", rom(5000), 10)
	in, err := Inspect(p, DefaultLimits)
	if err != nil {
		t.Fatalf("inspect should pass — the lie is only visible on extraction: %v", err)
	}
	dir, files, err := Extract(in, DefaultLimits)
	if err == nil {
		os.RemoveAll(dir)
		t.Fatal("extracted an archive that lied about its size")
	}
	if dir != "" || files != nil {
		os.RemoveAll(dir)
		t.Errorf("failed extraction left %q behind", dir)
	}
}

// The size check in writeEntry is the backstop for when the underlying reader
// does not police this itself — 7-Zip's solid streams are read by a third-party
// library with no such guarantee. Driving writeEntry directly is the only way
// to exercise it, since archive/zip intercepts the equivalent case above.
func TestWriteEntryRefusesOverProducingReader(t *testing.T) {
	dir := t.TempDir()
	e := entry{
		name: "liar.sfc",
		size: 10,
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(rom(5000))), nil
		},
	}
	_, err := writeEntry(dir, e, Member{Name: "liar.sfc", Size: 10}, DefaultLimits, map[string]bool{})
	if !Rejected(err) || !strings.Contains(err.Error(), "lying") {
		t.Fatalf("want a lying-archive rejection, got %v", err)
	}
}

// The mirror case: a stream that stops short of what the index promised is
// just as much a mismatch, and just as refused.
func TestWriteEntryRefusesUnderProducingReader(t *testing.T) {
	dir := t.TempDir()
	e := entry{
		name: "short.sfc",
		size: 5000,
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(rom(10))), nil
		},
	}
	_, err := writeEntry(dir, e, Member{Name: "short.sfc", Size: 5000}, DefaultLimits, map[string]bool{})
	if !Rejected(err) {
		t.Fatalf("want a rejection, got %v", err)
	}
}

func TestClassificationDecidesDeletability(t *testing.T) {
	cases := []struct {
		name      string
		members   []member
		payload   int
		unknown   int
		deletable bool
	}{
		{
			name:      "payload only",
			members:   []member{{name: "game.sfc", body: rom(512)}},
			payload:   1,
			deletable: true,
		},
		{
			name: "companions do not block deletion",
			members: []member{
				{name: "game.sfc", body: rom(512)},
				{name: "readme.txt", body: []byte("cracked by someone")},
				{name: "cover.jpg", body: rom(32)},
				{name: "game.sfv", body: []byte("game.sfc 1a2b3c4d")},
			},
			payload:   1,
			deletable: true,
		},
		{
			name: "an unrecognised file keeps the archive",
			members: []member{
				{name: "game.sfc", body: rom(512)},
				{name: "installer.bin.part", body: rom(64)},
			},
			payload:   1,
			unknown:   1,
			deletable: false,
		},
		{
			name: "a nested archive keeps the archive",
			members: []member{
				{name: "game.sfc", body: rom(512)},
				{name: "extras.rar", body: rom(64)},
			},
			payload:   1,
			unknown:   1,
			deletable: false,
		},
		{
			name:      "nothing routable inside",
			members:   []member{{name: "readme.txt", body: []byte("hello")}},
			payload:   0,
			deletable: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := Inspect(writeZip(t, c.members), DefaultLimits)
			if err != nil {
				t.Fatalf("inspect: %v", err)
			}
			if len(in.Payload) != c.payload {
				t.Errorf("payload = %d, want %d", len(in.Payload), c.payload)
			}
			if len(in.Unknown) != c.unknown {
				t.Errorf("unknown = %d, want %d", len(in.Unknown), c.unknown)
			}
			if in.Deletable() != c.deletable {
				t.Errorf("deletable = %v, want %v", in.Deletable(), c.deletable)
			}
		})
	}
}

// Directory structure inside the archive is discarded on the way out, which is
// what makes traversal impossible rather than merely detected.
func TestExtractFlattensAndStaysInsideTempDir(t *testing.T) {
	p := writeZip(t, []member{
		{name: "Some Release/disc1/game.bin", body: rom(300)},
		{name: "Some Release/disc2/game.bin", body: rom(400)},
		{name: "Some Release/game.cue", body: []byte("FILE \"game.bin\" BINARY\nTRACK 01 MODE2/2352\n")},
	})
	in, err := Inspect(p, DefaultLimits)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	dir, files, err := Extract(in, DefaultLimits)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer os.RemoveAll(dir)

	if len(files) != 3 {
		t.Fatalf("extracted %d files, want 3", len(files))
	}
	seen := map[string]bool{}
	for _, f := range files {
		if filepath.Dir(f.Path) != dir {
			t.Errorf("%q escaped the temp directory %q", f.Path, dir)
		}
		seen[filepath.Base(f.Path)] = true
	}
	// The colliding names must both survive, under distinct names.
	if !seen["game.bin"] || !seen["game-2.bin"] {
		t.Errorf("collision not resolved, got %v", keys(seen))
	}

	// And the bytes must be intact.
	for _, f := range files {
		st, err := os.Stat(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(st.Size()) != f.Member.Size {
			t.Errorf("%s: wrote %d bytes, index declared %d", f.Path, st.Size(), f.Member.Size)
		}
	}
}

func TestExtractRefusesPayloadFreeArchive(t *testing.T) {
	in, err := Inspect(writeZip(t, []member{{name: "readme.txt", body: []byte("hi")}}), DefaultLimits)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if _, _, err := Extract(in, DefaultLimits); !Rejected(err) {
		t.Fatalf("want a rejection, got %v", err)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
