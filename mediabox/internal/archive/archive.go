// Package archive opens .zip and .7z files without trusting a byte of them.
//
// The order is deliberate: everything that can be decided from the index alone
// is decided before a single byte is written to disk. Extraction then writes
// bare file names into a fresh private directory, which makes path traversal
// impossible by construction rather than by checking for it.
package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
)

// Class decides what a member means for the bundle as a whole.
type Class int

const (
	// ClassPayload is something worth routing to the box.
	ClassPayload Class = iota
	// ClassCompanion is a harmless sidecar. It is not sent, and it never
	// stands in the way of deleting the archive afterwards.
	ClassCompanion
	// ClassUnknown is content we could not account for. It is not sent, and
	// its presence means the archive is never offered for deletion: something
	// in there was not understood, so it is not ours to throw away.
	ClassUnknown
)

func (c Class) String() string {
	switch c {
	case ClassPayload:
		return "payload"
	case ClassCompanion:
		return "companion"
	default:
		return "unknown"
	}
}

// Rejection marks an archive as refused rather than broken.
type Rejection struct{ Reason string }

func (r *Rejection) Error() string { return r.Reason }

// Rejected reports whether err is a refusal.
func Rejected(err error) bool {
	var r *Rejection
	return errors.As(err, &r)
}

func reject(format string, args ...any) error {
	return &Rejection{Reason: fmt.Sprintf(format, args...)}
}

// Member is one entry in the index.
type Member struct {
	Name       string
	Size       uint64 // declared uncompressed size
	Compressed uint64 // 0 when the format does not report it per entry
	Class      Class
}

// Inspection is what the index says, after every check has passed.
type Inspection struct {
	Path      string
	Format    string
	Members   []Member
	Payload   []Member
	Companion []Member
	Unknown   []Member
}

// Deletable reports whether the archive may be offered for deletion once its
// payload is verified on the box. Anything unaccounted for keeps it.
func (in *Inspection) Deletable() bool {
	return len(in.Unknown) == 0 && len(in.Payload) > 0
}

// Limits bound what an archive is allowed to claim about itself.
type Limits struct {
	MaxEntries    int
	MaxTotalBytes uint64
	MaxEntryBytes uint64
	MaxRatio      float64
	RatioFloor    uint64 // ratios below this size are noise, not bombs
}

// DefaultLimits are sized for game and film archives with room to spare.
//
// MaxTotalBytes is the check that actually bounds the damage: extraction
// enforces each entry's declared size exactly, so nothing can write more than
// the index adds up to. MaxRatio is a cheap early warning on top of that, and
// it has to be generous — cartridge ROMs are padded with large runs of zeroes
// and legitimately compress around 1000:1, while real decompression bombs run
// to a million to one and beyond. Setting it tight enough to be "strict" only
// refuses ordinary games.
var DefaultLimits = Limits{
	MaxEntries:    2048,
	MaxTotalBytes: 16 << 30,
	MaxEntryBytes: 8 << 30,
	MaxRatio:      10000,
	RatioFloor:    1 << 20,
}

// entry is the format-independent view of a member.
type entry struct {
	name string
	size uint64
	comp uint64
	mode fs.FileMode
	dir  bool
	open func() (io.ReadCloser, error)
}

type opened struct {
	format  string
	entries []entry
	close   func() error
}

// IsArchive reports whether this path is one we handle, by extension.
func IsArchive(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".zip", ".7z":
		return true
	}
	return false
}

func openArchive(p string) (*opened, error) {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".zip":
		return openZip(p)
	case ".7z":
		return open7z(p)
	}
	return nil, reject("not an archive we handle")
}

func openZip(p string) (*opened, error) {
	rc, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	o := &opened{format: "zip", close: rc.Close}
	for _, f := range rc.File {
		o.entries = append(o.entries, entry{
			name: f.Name,
			size: f.UncompressedSize64,
			comp: f.CompressedSize64,
			mode: f.Mode(),
			dir:  f.FileInfo().IsDir(),
			open: func() (io.ReadCloser, error) { return f.Open() },
		})
	}
	return o, nil
}

func open7z(p string) (*opened, error) {
	rc, err := sevenzip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	o := &opened{format: "7z", close: rc.Close}
	for _, f := range rc.File {
		o.entries = append(o.entries, entry{
			name: f.Name,
			size: f.UncompressedSize,
			// 7-Zip packs files into shared solid streams, so there is no
			// honest per-entry compressed size. The whole-archive ratio is
			// checked instead.
			comp: 0,
			mode: f.Mode(),
			dir:  f.FileInfo().IsDir(),
			open: func() (io.ReadCloser, error) { return f.Open() },
		})
	}
	return o, nil
}

// Inspect reads only the index and applies every check that can be made
// without decompressing anything.
func Inspect(p string, lim Limits) (*Inspection, error) {
	o, err := openArchive(p)
	if err != nil {
		return nil, err
	}
	defer o.close()

	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}

	if len(o.entries) > lim.MaxEntries {
		return nil, reject("%d entries, over the %d cap", len(o.entries), lim.MaxEntries)
	}

	in := &Inspection{Path: p, Format: o.format}
	var total uint64

	for _, e := range o.entries {
		if e.dir {
			continue
		}
		if err := checkName(e.name); err != nil {
			return nil, err
		}
		if err := checkMode(e.name, e.mode); err != nil {
			return nil, err
		}
		if e.size > lim.MaxEntryBytes {
			return nil, reject("entry %q declares %s, over the per-file cap", e.name, human(e.size))
		}
		// A per-entry ratio only means something where the format reports an
		// honest compressed size.
		if e.comp > 0 && e.size > lim.RatioFloor {
			if r := float64(e.size) / float64(e.comp); r > lim.MaxRatio {
				return nil, reject("entry %q expands %.0fx (%s from %s) — refusing as a decompression bomb",
					e.name, r, human(e.size), human(e.comp))
			}
		}
		total += e.size
		if total > lim.MaxTotalBytes {
			return nil, reject("declares more than %s uncompressed, over the cap", human(lim.MaxTotalBytes))
		}

		m := Member{Name: e.name, Size: e.size, Compressed: e.comp, Class: classify(e.name)}
		in.Members = append(in.Members, m)
		switch m.Class {
		case ClassPayload:
			in.Payload = append(in.Payload, m)
		case ClassCompanion:
			in.Companion = append(in.Companion, m)
		default:
			in.Unknown = append(in.Unknown, m)
		}
	}

	// Solid formats get the ratio check at whole-archive level, which is where
	// a 7z bomb would show up anyway.
	if archSize := uint64(st.Size()); archSize > 0 && total > lim.RatioFloor {
		if r := float64(total) / float64(archSize); r > lim.MaxRatio {
			return nil, reject("expands %.0fx overall (%s from %s) — refusing as a decompression bomb",
				r, human(total), human(archSize))
		}
	}

	return in, nil
}

// checkName refuses anything that is trying to escape, in any of the spellings
// that have historically worked.
func checkName(name string) error {
	if name == "" {
		return reject("entry with an empty name")
	}
	if strings.ContainsRune(name, 0) {
		return reject("entry name contains a NUL byte")
	}
	if strings.Contains(name, `\`) {
		return reject("entry %q uses backslash separators", name)
	}
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return reject("entry %q is an absolute path", name)
	}
	if len(name) >= 2 && name[1] == ':' {
		return reject("entry %q carries a drive letter", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return reject("entry %q climbs out of the archive", name)
		}
	}
	return nil
}

// checkMode refuses entries that are not plain files. A symlink in an archive
// is a way to make extraction write somewhere it was never pointed at.
func checkMode(name string, mode fs.FileMode) error {
	if mode&fs.ModeSymlink != 0 {
		return reject("entry %q is a symlink", name)
	}
	if mode&(fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeCharDevice) != 0 {
		return reject("entry %q is a device or pipe, not a file", name)
	}
	if mode&fs.ModeSetuid != 0 || mode&fs.ModeSetgid != 0 {
		return reject("entry %q is setuid/setgid", name)
	}
	return nil
}

// payloadExts is what might be worth sending. It is deliberately name-based and
// deliberately provisional: whatever comes out is identified again by content
// before it goes anywhere.
var payloadExts = map[string]bool{
	"mkv": true, "mp4": true, "mov": true, "avi": true, "webm": true,
	"m4v": true, "wmv": true, "flv": true, "mpg": true, "mpeg": true,
	"m2ts": true, "ts": true,
	"srt": true, "sub": true, "ass": true, "ssa": true, "vtt": true,
	"sfc": true, "smc": true, "nes": true, "fds": true, "gb": true,
	"gbc": true, "gba": true, "md": true, "gen": true, "smd": true,
	"sms": true, "gg": true, "n64": true, "z64": true, "v64": true,
	"gdi": true, "cdi": true, "cso": true,
	"bin": true, "cue": true, "iso": true, "chd": true, "img": true, "pbp": true,
}

// companionExts are the sidecars that habitually travel with a release. They
// are not sent, and they do not block deleting the archive afterwards.
var companionExts = map[string]bool{
	"txt": true, "nfo": true, "diz": true, "md5": true, "sfv": true,
	"sha1": true, "crc": true, "jpg": true, "jpeg": true, "png": true,
	"log": true, "url": true,
}

// nestedExts are archives inside archives. We do not open them, and their
// presence keeps the outer archive from being deleted.
var nestedExts = map[string]bool{
	"zip": true, "7z": true, "rar": true, "gz": true, "bz2": true,
	"xz": true, "tar": true, "tgz": true, "z": true, "lzh": true, "arj": true,
}

func classify(name string) Class {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	// A nested archive is checked before payload: ".md" is a companion here,
	// but "md" is also a Mega Drive ROM, so order decides. Nested wins because
	// unopened content must never be treated as accounted for.
	if nestedExts[ext] {
		return ClassUnknown
	}
	if payloadExts[ext] {
		return ClassPayload
	}
	if companionExts[ext] {
		return ClassCompanion
	}
	return ClassUnknown
}

func human(n uint64) string {
	const u = 1 << 10
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0fM", float64(n)/(1<<20))
	case n >= u:
		return fmt.Sprintf("%.0fK", float64(n)/u)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
