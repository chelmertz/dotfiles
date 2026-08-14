// Package media decides what a file actually is, from its bytes.
//
// The rule everywhere here is deny by default: a file leaves the laptop only
// when something positively recognised it. An extension is a claim made by
// whoever named the file, so it never decides anything on its own.
package media

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Kind is the broad category, which decides the shape of the routing.
type Kind int

const (
	KindUnknown Kind = iota
	KindROM
	KindDisc
	KindVideo
	KindSubtitle
)

// Confidence records how a verdict was reached, which decides whether the user
// gets asked. Only disc images are ever ambiguous enough to ask about.
type Confidence int

const (
	// ConfNone means nothing recognised it.
	ConfNone Confidence = iota
	// ConfWeak means no content signature fired and the verdict rests on the
	// extension plus a plausible size. Worth asking about.
	ConfWeak
	// ConfStrong means a content signature matched.
	ConfStrong
)

// ID is a verdict about one file.
type ID struct {
	System string // routing key: snes, nes, gb, gba, n64, genesis, sms, ps1, psp, dreamcast, movies
	Kind   Kind
	Conf   Confidence
	Detail string // the evidence, in words, shown to the user
	Size   int64
}

// Rejection explains why a file is not going anywhere. It is a distinct type so
// callers can report a refusal differently from an I/O failure.
type Rejection struct {
	Reason string
}

func (r *Rejection) Error() string { return r.Reason }

// Rejected reports whether err is a refusal rather than an operational error.
func Rejected(err error) bool {
	var r *Rejection
	return errors.As(err, &r)
}

func reject(format string, args ...any) error {
	return &Rejection{Reason: fmt.Sprintf(format, args...)}
}

// headLen is how much of the file the cheap probes get to look at. Every
// cartridge signature lives far below this; the disc probes seek past it.
const headLen = 64 << 10

// Identify runs the full ladder over one file: danger gate, then content
// probes, then extension and size as a fallback.
func Identify(path string) (ID, error) {
	st, err := os.Stat(path)
	if err != nil {
		return ID{}, err
	}
	if st.IsDir() {
		return ID{}, reject("is a directory")
	}
	if !st.Mode().IsRegular() {
		return ID{}, reject("not a regular file (%s)", st.Mode().Type())
	}
	if st.Size() == 0 {
		return ID{}, reject("empty file")
	}

	f, err := os.Open(path)
	if err != nil {
		return ID{}, err
	}
	defer f.Close()

	head := make([]byte, headLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return ID{}, err
	}
	head = head[:n]

	return identify(f, head, st.Size(), filepath.Base(path))
}

// identify is the testable core: everything it needs is already in hand.
func identify(r io.ReaderAt, head []byte, size int64, name string) (ID, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))

	// The door. Nothing executable gets past here, whatever it is called.
	if err := dangerGate(ext, head); err != nil {
		return ID{}, err
	}

	// Content probes. A match here is the strongest thing we can say.
	for _, p := range probes {
		id, ok := p(r, head, size)
		if !ok {
			continue
		}
		id.Size = size
		if err := checkSize(id.System, size); err != nil {
			return ID{}, err
		}
		id.Conf = ConfStrong
		return id, nil
	}

	// Nothing signed itself. Fall back to the extension, but only where the
	// format genuinely has no header to check, and only at a plausible size.
	id, ok := fallback(ext, size)
	if !ok {
		if ext == "" {
			return ID{}, reject("unrecognised content, no extension to fall back on")
		}
		return ID{}, reject("unrecognised content (.%s is not evidence on its own)", ext)
	}
	id.Size = size
	if err := checkSize(id.System, size); err != nil {
		return ID{}, err
	}
	return id, nil
}
