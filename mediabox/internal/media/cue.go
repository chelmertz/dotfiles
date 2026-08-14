package media

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// A cue sheet is a text index naming the real data files next to it. Treating
// the cue and its bin as one unit is what stops the old script asking which
// console a game is for twice — once for the .cue and once for the .bin.

// IsCueSheet reports whether these bytes are a cue sheet.
func IsCueSheet(head []byte) bool {
	limit := min(len(head), 8192)
	upper := bytes.ToUpper(head[:limit])
	return bytes.Contains(upper, []byte("FILE ")) && bytes.Contains(upper, []byte("TRACK "))
}

// CueTracks returns the files a cue sheet references, resolved next to the cue
// itself. Names are taken as bare file names: a cue that tries to reach outside
// its own directory is refused rather than followed.
func CueTracks(cuePath string) ([]string, error) {
	f, err := os.Open(cuePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dir := filepath.Dir(cuePath)
	var out []string
	seen := map[string]bool{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*kb), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(strings.ToUpper(line), "FILE ") {
			continue
		}
		name := cueFileName(line)
		if name == "" {
			continue
		}
		// The cue lives beside its tracks. Anything with a path separator is
		// trying to leave that directory, so drop it.
		if name != filepath.Base(name) {
			return nil, reject("cue sheet references a path outside its directory: %q", name)
		}
		p := filepath.Join(dir, name)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, reject("cue sheet references no track files")
	}
	return out, nil
}

// cueFileName pulls the name out of a FILE line, which quotes it when it
// contains spaces and leaves it bare otherwise.
func cueFileName(line string) string {
	rest := strings.TrimSpace(line[len("FILE "):])
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : 1+end]
		}
		return ""
	}
	// Unquoted: the name runs to the whitespace before the trailing type word.
	if i := strings.LastIndexByte(rest, ' '); i > 0 {
		return strings.TrimSpace(rest[:i])
	}
	return rest
}
