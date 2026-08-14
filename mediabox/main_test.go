package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/chelmertz/dotfiles/mediabox/internal/media"
)

// fakeTransport stands in for rsync so the whole send-and-verify path can be
// exercised with no media box present.
type fakeTransport struct {
	sent     map[string][]string
	mismatch map[string]bool // base names the box reports as different
	sendErr  error
}

func newFake() *fakeTransport {
	return &fakeTransport{sent: map[string][]string{}, mismatch: map[string]bool{}}
}

func (f *fakeTransport) Send(files []string, dest string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent[dest] = append(f.sent[dest], files...)
	return nil
}

func (f *fakeTransport) Verify(files []string, _ string) ([]string, error) {
	var diff []string
	for _, p := range files {
		if f.mismatch[filepath.Base(p)] {
			diff = append(diff, p)
		}
	}
	return diff, nil
}

func mkItem(b *bundle, src, system string) *item {
	it := &item{
		src:   src,
		label: filepath.Base(src),
		id:    media.ID{System: system, Kind: media.KindROM, Conf: media.ConfStrong, Size: 1024},
		dest:  "/data/games/roms/" + system,
		owner: b,
	}
	b.items = append(b.items, it)
	return it
}

func TestTransferGroupsByDestination(t *testing.T) {
	b := &bundle{origin: "batch"}
	items := []*item{
		mkItem(b, "/tmp/a.sfc", "snes"),
		mkItem(b, "/tmp/b.sfc", "snes"),
		mkItem(b, "/tmp/c.nes", "nes"),
	}
	f := newFake()
	if !transfer(f, items) {
		t.Fatal("transfer reported failure")
	}
	if got := len(f.sent["/data/games/roms/snes"]); got != 2 {
		t.Errorf("snes batch had %d files, want 2 — destinations should be batched", got)
	}
	if got := len(f.sent["/data/games/roms/nes"]); got != 1 {
		t.Errorf("nes batch had %d files, want 1", got)
	}
}

// The point of verifying: a copy that does not match must not let the local
// original be deleted.
func TestVerificationFailureHoldsTheBundle(t *testing.T) {
	b := &bundle{origin: "/home/ch/Downloads/game.sfc", locals: []string{"/home/ch/Downloads/game.sfc"}}
	items := []*item{mkItem(b, "/tmp/game.sfc", "snes")}

	f := newFake()
	f.mismatch["game.sfc"] = true

	if !transfer(f, items) {
		t.Fatal("transfer reported failure; a mismatch is a held bundle, not a fatal error")
	}
	if b.sent {
		t.Error("bundle marked sent despite a checksum mismatch")
	}
	if b.deletable() {
		t.Error("a bundle whose copy did not verify must never be offered for deletion")
	}
	if b.hold == "" {
		t.Error("bundle should record why it was held")
	}
}

func TestVerifiedBundleBecomesDeletable(t *testing.T) {
	b := &bundle{origin: "/home/ch/Downloads/game.sfc", locals: []string{"/home/ch/Downloads/game.sfc"}}
	items := []*item{mkItem(b, "/tmp/game.sfc", "snes")}

	if !transfer(newFake(), items) {
		t.Fatal("transfer reported failure")
	}
	if !b.sent {
		t.Error("bundle should be marked sent")
	}
	if !b.deletable() {
		t.Error("a verified bundle with no holds should be deletable")
	}
}

// An archive holding something we could not account for keeps its original,
// even when everything we did recognise copied across perfectly.
func TestUnaccountedContentBlocksDeletion(t *testing.T) {
	b := &bundle{
		origin: "/home/ch/Downloads/game.zip",
		locals: []string{"/home/ch/Downloads/game.zip"},
		hold:   "holds 1 unrecognised file(s), e.g. installer.bin.part",
	}
	mkItem(b, "/tmp/x/game.sfc", "snes")

	if !transfer(newFake(), b.items) {
		t.Fatal("transfer reported failure")
	}
	if b.deletable() {
		t.Error("an archive with unaccounted content must not be offered for deletion")
	}
}

func TestSendFailureIsFatal(t *testing.T) {
	b := &bundle{origin: "batch"}
	items := []*item{mkItem(b, "/tmp/a.sfc", "snes")}
	f := newFake()
	f.sendErr = errors.New("connection refused")

	if transfer(f, items) {
		t.Error("a failed send should stop the run")
	}
}

func TestParseArgs(t *testing.T) {
	t.Setenv("MEDIABOX_HOST", "")
	t.Setenv("MEDIABOX_USER", "")

	opt, err := parseArgs([]string{"-n", "--host", "box.lan", "--user", "someone", "a.sfc", "b.mkv"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !opt.dryRun {
		t.Error("dry run not set")
	}
	if opt.host != "box.lan" || opt.user != "someone" {
		t.Errorf("host/user = %q/%q", opt.host, opt.user)
	}
	if len(opt.files) != 2 {
		t.Errorf("files = %v, want 2", opt.files)
	}
}

// A file named "-weird.sfc" is a file, not a flag, once it is past "--".
func TestParseArgsDoubleDashEndsOptions(t *testing.T) {
	opt, err := parseArgs([]string{"--", "-weird.sfc", "--host"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(opt.files) != 2 || opt.files[0] != "-weird.sfc" {
		t.Errorf("files = %v, want the literal names", opt.files)
	}
}

func TestParseArgsRejectsUnknownOption(t *testing.T) {
	if _, err := parseArgs([]string{"--delete-everything", "a.sfc"}); err == nil {
		t.Error("unknown option accepted")
	}
}

func TestParseArgsNoFilesIsUsage(t *testing.T) {
	if _, err := parseArgs(nil); !errors.Is(err, errUsage) {
		t.Errorf("want errUsage, got %v", err)
	}
}
