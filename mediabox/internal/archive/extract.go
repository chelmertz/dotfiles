package archive

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extracted is one file written out of an archive.
type Extracted struct {
	Path   string // where it landed, inside the temp directory
	Member Member
}

// Extract writes the payload members into a fresh private directory and
// returns that directory so the caller can remove it unconditionally.
//
// Only payload members are written. Names are reduced to their base name, so
// no entry can cause a write outside the directory even if a check above was
// wrong — there is no path to traverse. Each entry is copied through a limited
// reader and the bytes written must match what the index declared, so a header
// that lies about its size cannot run away with the disk.
func Extract(in *Inspection, lim Limits) (dir string, out []Extracted, err error) {
	if len(in.Payload) == 0 {
		return "", nil, reject("nothing routable inside")
	}

	o, err := openArchive(in.Path)
	if err != nil {
		return "", nil, err
	}
	defer o.close()

	dir, err = os.MkdirTemp("", "mediabox-")
	if err != nil {
		return "", nil, err
	}
	// Any failure past this point leaves nothing behind.
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
			dir = ""
			out = nil
		}
	}()

	want := make(map[string]Member, len(in.Payload))
	for _, m := range in.Payload {
		want[m.Name] = m
	}

	used := map[string]bool{}
	for _, e := range o.entries {
		m, ok := want[e.name]
		if !ok || e.dir {
			continue
		}
		var dest string
		dest, err = writeEntry(dir, e, m, lim, used)
		if err != nil {
			return "", nil, err
		}
		out = append(out, Extracted{Path: dest, Member: m})
	}

	if len(out) != len(in.Payload) {
		err = reject("archive index and contents disagree: expected %d payload files, wrote %d",
			len(in.Payload), len(out))
		return "", nil, err
	}
	return dir, out, nil
}

func writeEntry(dir string, e entry, m Member, lim Limits, used map[string]bool) (string, error) {
	name := uniqueName(filepath.Base(e.name), used)
	dest := filepath.Join(dir, name)

	rc, err := e.open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// One byte past the declared size is enough to catch a header that lies
	// low, and the per-entry cap catches one that lies high.
	ceiling := min(m.Size, lim.MaxEntryBytes)
	limited := &io.LimitedReader{R: rc, N: int64(ceiling) + 1}

	n, err := io.Copy(f, limited)
	if err != nil {
		return "", fmt.Errorf("extracting %q: %w", e.name, err)
	}
	if uint64(n) != m.Size {
		return "", reject("entry %q declared %s but produced %s — refusing a lying archive",
			e.name, human(m.Size), human(uint64(n)))
	}
	return dest, nil
}

// uniqueName keeps two members called "game.bin" in different directories from
// colliding once the paths are flattened away.
func uniqueName(base string, used map[string]bool) string {
	if base == "" || base == "." || base == ".." {
		base = "unnamed"
	}
	if !used[base] {
		used[base] = true
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if !used[cand] {
			used[cand] = true
			return cand
		}
	}
}
