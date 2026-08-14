package archive

import (
	"os"
	"path/filepath"
	"testing"
)

// sevenzip is a third-party reader, so the 7z path deserves a real archive
// rather than confidence by analogy with zip. testdata/rom.7z holds a single
// 512K SNES ROM and was produced with p7zip; the library is read-only, so the
// fixture is checked in rather than generated.
func TestSevenZipRoundTrip(t *testing.T) {
	p := filepath.Join("testdata", "rom.7z")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	in, err := Inspect(p, DefaultLimits)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if in.Format != "7z" {
		t.Errorf("format = %q, want 7z", in.Format)
	}
	if len(in.Payload) != 1 {
		t.Fatalf("payload = %d members, want 1: %+v", len(in.Payload), in.Members)
	}
	if !in.Deletable() {
		t.Error("an archive holding only a ROM should be deletable once verified")
	}

	dir, files, err := Extract(in, DefaultLimits)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer os.RemoveAll(dir)

	if len(files) != 1 {
		t.Fatalf("extracted %d files, want 1", len(files))
	}
	st, err := os.Stat(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	// The declared size and what actually landed must agree; this is the check
	// that has no equivalent guarantee inside the 7z reader.
	if uint64(st.Size()) != files[0].Member.Size {
		t.Errorf("wrote %d bytes, index declared %d", st.Size(), files[0].Member.Size)
	}
	if filepath.Dir(files[0].Path) != dir {
		t.Errorf("%q escaped the temp directory", files[0].Path)
	}
}
