// mediabox-add copies files to the right place on the media box, deciding what
// each one is from its contents rather than from its name.
//
//	mediabox-add *.sfc              # -> /data/games/roms/snes
//	mediabox-add ~/Downloads/*      # routes what it recognises, refuses the rest
//	mediabox-add -n game.zip        # dry run
//
// Nothing is sent unless something positively identified it, archives are
// checked before a byte of them is written to disk, and local originals are
// only ever deleted after the copy has been verified and you have said yes.
//
// Reports on stderr, so stdout stays free for piping.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chelmertz/dotfiles/mediabox/internal/archive"
	"github.com/chelmertz/dotfiles/mediabox/internal/box"
	"github.com/chelmertz/dotfiles/mediabox/internal/media"
)

// item is one file that will actually be copied.
type item struct {
	src   string // local path, possibly inside a temp directory
	label string // what the user sees
	id    media.ID
	dest  string
	owner *bundle
}

// bundle is everything that came from one command-line argument. Deletion is
// decided per bundle, so an archive and the files that came out of it are
// accepted or kept as a unit.
type bundle struct {
	origin  string
	items   []*item
	locals  []string // local originals that deletion would remove
	tempDir string   // extracted files, removed either way
	hold    string   // non-empty means never offer this for deletion
	sent    bool
}

func (b *bundle) deletable() bool { return b.hold == "" && b.sent && len(b.items) > 0 }

func main() {
	os.Exit(run())
}

type options struct {
	host   string
	user   string
	dryRun bool
	files  []string
}

func run() int {
	opt, err := parseArgs(os.Args[1:])
	if err != nil {
		if errors.Is(err, errUsage) {
			usage()
			return 0
		}
		errf("%s%v%s", cErr, err, cOff)
		usage()
		return 2
	}

	bundles := make([]*bundle, 0, len(opt.files))
	for _, f := range opt.files {
		if b := build(f); b != nil {
			bundles = append(bundles, b)
		}
	}
	// Extracted files are temporary whatever happens next.
	defer func() {
		for _, b := range bundles {
			if b.tempDir != "" {
				os.RemoveAll(b.tempDir)
			}
		}
	}()

	var all []*item
	for _, b := range bundles {
		all = append(all, b.items...)
	}
	if len(all) == 0 {
		errf("")
		errf("nothing to send")
		return 0
	}

	// Everything that needs asking is asked before any transfer starts.
	all = resolve(all, opt.dryRun)
	if len(all) == 0 {
		errf("")
		errf("nothing to send")
		return 0
	}

	if opt.dryRun {
		for _, it := range all {
			errf("%swould%s  %s %s->%s %s", cDim, cOff, it.label, cDim, cOff, box.Short(it.dest))
		}
		return 0
	}

	tr := box.Rsync{User: opt.user, Host: opt.host, Verbose: true}
	if !transfer(tr, all) {
		return 1
	}

	offerDeletion(bundles)

	sent := 0
	for _, it := range all {
		if it.owner.sent {
			sent++
		}
	}
	errf("")
	errf("%s%d sent%s%s -> %s@%s%s", cOk, sent, cOff, cDim, opt.user, opt.host, cOff)
	return 0
}

var errUsage = errors.New("usage")

func parseArgs(args []string) (options, error) {
	opt := options{
		host: envOr("MEDIABOX_HOST", "mediabox.local"),
		user: envOr("MEDIABOX_USER", "ch"),
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-n", "--dry-run":
			opt.dryRun = true
		case "-h", "--help":
			return opt, errUsage
		case "--host", "--user":
			if i+1 >= len(args) {
				return opt, fmt.Errorf("%s needs a value", a)
			}
			i++
			if a == "--host" {
				opt.host = args[i]
			} else {
				opt.user = args[i]
			}
		case "--":
			opt.files = append(opt.files, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				return opt, fmt.Errorf("unknown option: %s", a)
			}
			opt.files = append(opt.files, a)
		}
	}
	if len(opt.files) == 0 {
		return opt, errUsage
	}
	return opt, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: mediabox-add [-n|--dry-run] [--host HOST] [--user USER] FILE...

  -n, --dry-run    show what would happen, copy nothing
      --host HOST  default: mediabox.local  (or $MEDIABOX_HOST)
      --user USER  default: ch              (or $MEDIABOX_USER)

Identifies files by content, not by extension. Anything unrecognised is
refused rather than guessed at, so globbing a whole directory is safe.
.zip and .7z archives are checked, then unpacked locally and their contents
identified. After a verified copy you are asked once whether to delete the
local originals.
`)
}

// build turns one command-line argument into a bundle.
func build(path string) *bundle {
	b := &bundle{origin: path}
	name := filepath.Base(path)

	st, err := os.Stat(path)
	switch {
	case err != nil:
		errf("%smiss  %s %s %s(no such file)%s", cErr, cOff, name, cDim, cOff)
		return nil
	case st.IsDir():
		errf("%sskip  %s %s %s(directory)%s", cSkip, cOff, name, cDim, cOff)
		return nil
	}

	switch {
	case archive.IsArchive(path):
		buildArchive(b, path, name)
	case isCue(path):
		buildCue(b, path, name)
	default:
		buildFile(b, path, name)
	}
	if len(b.items) == 0 {
		return nil
	}
	return b
}

func buildFile(b *bundle, path, name string) {
	id, err := media.Identify(path)
	if err != nil {
		report(name, err)
		return
	}
	b.items = append(b.items, &item{src: path, label: name, id: id, owner: b})
	b.locals = []string{path}
	errf("%sfound %s %s %s(%s)%s", cOk, cOff, name, cDim, id.Detail, cOff)
}

func isCue(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".cue") {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	return media.IsCueSheet(head[:n])
}

// buildCue keeps a cue sheet and the tracks it names together, so one game is
// one decision rather than one decision per file.
func buildCue(b *bundle, path, name string) {
	tracks, err := media.CueTracks(path)
	if err != nil {
		report(name, err)
		return
	}
	var id media.ID
	var found bool
	for _, t := range tracks {
		if _, err := os.Stat(t); err != nil {
			errf("%sskip  %s %s %s(references missing track %s)%s",
				cWarn, cOff, name, cDim, filepath.Base(t), cOff)
			return
		}
		if !found {
			if tid, err := media.Identify(t); err == nil {
				id, found = tid, true
			}
		}
	}
	if !found {
		// The tracks are raw sectors with no readable filesystem. Fall back to
		// the total size and let the user confirm.
		var total int64
		for _, t := range tracks {
			if st, err := os.Stat(t); err == nil {
				total += st.Size()
			}
		}
		id = media.DiscBySize(total)
	}

	b.locals = append([]string{path}, tracks...)
	for _, p := range append([]string{path}, tracks...) {
		b.items = append(b.items, &item{src: p, label: filepath.Base(p), id: id, owner: b})
	}
	errf("%sfound %s %s %s(cue sheet + %d track(s): %s)%s",
		cOk, cOff, name, cDim, len(tracks), id.Detail, cOff)
}

// buildArchive is the careful path: inspect the index, refuse anything odd,
// unpack only the payload, then identify what actually came out.
func buildArchive(b *bundle, path, name string) {
	in, err := archive.Inspect(path, archive.DefaultLimits)
	if err != nil {
		report(name, err)
		return
	}
	if len(in.Payload) == 0 {
		errf("%sskip  %s %s %s(no game or media files inside — not unpacked, not touched)%s",
			cWarn, cOff, name, cDim, cOff)
		return
	}

	dir, files, err := archive.Extract(in, archive.DefaultLimits)
	if err != nil {
		report(name, err)
		return
	}
	b.tempDir = dir
	b.locals = []string{path}

	if n := len(in.Unknown); n > 0 {
		b.hold = fmt.Sprintf("holds %d unrecognised file(s), e.g. %s", n, in.Unknown[0].Name)
	}

	for _, ex := range files {
		// The name inside the archive was only ever a hint. What came out gets
		// the full ladder, danger gate included.
		id, err := media.Identify(ex.Path)
		if err != nil {
			report(name+" → "+ex.Member.Name, err)
			if b.hold == "" {
				b.hold = fmt.Sprintf("%s was not identified", ex.Member.Name)
			}
			continue
		}
		b.items = append(b.items, &item{
			src: ex.Path, label: ex.Member.Name, id: id, owner: b,
		})
	}
	if len(b.items) > 0 {
		errf("%sfound %s %s %s(%s, %d file(s) unpacked)%s",
			cOk, cOff, name, cDim, in.Format, len(b.items), cOff)
	}
}

func report(name string, err error) {
	if media.Rejected(err) || archive.Rejected(err) {
		errf("%srefuse%s %s %s(%v)%s", cWarn, cOff, name, cDim, err, cOff)
		return
	}
	errf("%serror %s %s %s(%v)%s", cErr, cOff, name, cDim, err, cOff)
}

// resolve settles every uncertain verdict and assigns destinations, before any
// transfer begins.
func resolve(all []*item, dryRun bool) []*item {
	// One answer per bundle: a disc's cue and bins are the same game.
	answered := map[*bundle]string{}
	out := make([]*item, 0, len(all))

	for _, it := range all {
		if it.id.Kind == media.KindDisc && it.id.Conf < media.ConfStrong {
			if prev, ok := answered[it.owner]; ok {
				it.id.System = prev
			} else if dryRun {
				errf("%sask   %s %s %s(would ask which system; %s)%s",
					cWarn, cOff, it.label, cDim, it.id.Detail, cOff)
			} else {
				choice := askSystem(it.label, it.id)
				if choice == "" {
					errf("%sskip  %s %s %s(skipped at prompt)%s", cSkip, cOff, it.label, cDim, cOff)
					it.owner.hold = "skipped at the prompt"
					continue
				}
				answered[it.owner] = choice
				it.id.System = choice
			}
		}
		dest, err := box.Dest(it.id.System)
		if err != nil {
			report(it.label, err)
			it.owner.hold = "no destination"
			continue
		}
		it.dest = dest
		out = append(out, it)
	}
	return out
}

// askSystem asks which console a disc image belongs to, offering the size-based
// suggestion as the default. Reads the terminal directly so stdout stays clean.
func askSystem(label string, id media.ID) string {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		errf("%sskip  %s %s %s(needs a system, no terminal to ask)%s", cWarn, cOff, label, cDim, cOff)
		return ""
	}
	defer tty.Close()

	sc := bufio.NewScanner(tty)
	for {
		fmt.Fprintf(tty, "\n%s%s%s %s(%s)%s\n", cWarn, label, cOff, cDim, id.Detail, cOff)
		for i, s := range media.DiscSystems {
			marker := " "
			if s == id.System {
				marker = "*"
			}
			fmt.Fprintf(tty, "  %d) %s %s\n", i+1, s, marker)
		}
		fmt.Fprintf(tty, "  s) skip\n")
		fmt.Fprintf(tty, "> [%s] ", id.System)

		if !sc.Scan() {
			return ""
		}
		ans := strings.TrimSpace(sc.Text())
		switch {
		case ans == "":
			return id.System
		case ans == "s" || ans == "S" || ans == "skip":
			return ""
		}
		if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(media.DiscSystems) {
			return media.DiscSystems[n-1]
		}
	}
}

// transfer sends everything, one rsync per destination, then verifies by
// checksum before anything is called done.
func transfer(tr box.Transport, all []*item) bool {
	byDest := map[string][]*item{}
	var order []string
	for _, it := range all {
		if _, seen := byDest[it.dest]; !seen {
			order = append(order, it.dest)
		}
		byDest[it.dest] = append(byDest[it.dest], it)
	}

	for _, dest := range order {
		items := byDest[dest]
		srcs := make([]string, len(items))
		for i, it := range items {
			srcs[i] = it.src
		}

		if err := tr.Send(srcs, dest); err != nil {
			errf("%sfailed%s sending to %s %s(%v)%s", cErr, cOff, box.Short(dest), cDim, err, cOff)
			return false
		}

		diff, err := tr.Verify(srcs, dest)
		if err != nil {
			errf("%sfailed%s verifying %s %s(%v)%s", cErr, cOff, box.Short(dest), cDim, err, cOff)
			return false
		}
		bad := map[string]bool{}
		for _, d := range diff {
			bad[filepath.Base(d)] = true
		}

		for _, it := range items {
			if bad[filepath.Base(it.src)] {
				errf("%sBAD   %s %s %s(copy on the box does not match — keeping local)%s",
					cErr, cOff, it.label, cDim, cOff)
				it.owner.hold = "verification failed"
				continue
			}
			errf("%ssent  %s %s %s->%s %s %s(%s, verified)%s",
				cOk, cOff, it.label, cDim, cOff, box.Short(dest),
				cDim, media.HumanSize(it.id.Size), cOff)
			it.owner.sent = true
		}
	}
	return true
}

// offerDeletion asks once, for everything, at the end. One answer covers the
// whole set: minimum fuss, and safe by default since the answer is no.
func offerDeletion(bundles []*bundle) {
	var ready []*bundle
	var held []*bundle
	for _, b := range bundles {
		switch {
		case b.deletable():
			ready = append(ready, b)
		case len(b.locals) > 0 && b.hold != "":
			held = append(held, b)
		}
	}

	for _, b := range held {
		errf("%skept  %s %s %s(%s)%s", cSkip, cOff, filepath.Base(b.origin), cDim, b.hold, cOff)
	}
	if len(ready) == 0 {
		return
	}

	var locals []string
	temps := 0
	for _, b := range ready {
		locals = append(locals, b.locals...)
		if b.tempDir != "" {
			temps += len(b.items)
		}
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		errf("%skept  %s %d local file(s) %s(no terminal to ask)%s", cSkip, cOff, len(locals), cDim, cOff)
		return
	}
	defer tty.Close()

	fmt.Fprintf(tty, "\n%sdelete %d local file(s)?%s", cWarn, len(locals), cOff)
	if temps > 0 {
		fmt.Fprintf(tty, " %s(+%d extracted temp file(s), removed either way)%s", cDim, temps, cOff)
	}
	fmt.Fprintln(tty)
	for _, b := range ready {
		note := ""
		if b.tempDir != "" {
			note = cDim + "  archive, fully consumed" + cOff
		}
		for _, l := range b.locals {
			fmt.Fprintf(tty, "  %s%s\n", shortenHome(l), note)
			note = ""
		}
	}
	fmt.Fprintf(tty, "[y/N] ")

	sc := bufio.NewScanner(tty)
	if !sc.Scan() {
		return
	}
	if ans := strings.ToLower(strings.TrimSpace(sc.Text())); ans != "y" && ans != "yes" {
		errf("%skept  %s %d local file(s)%s", cSkip, cOff, len(locals), cOff)
		return
	}

	removed := 0
	for _, l := range locals {
		if err := os.Remove(l); err != nil {
			errf("%serror %s could not delete %s %s(%v)%s", cErr, cOff, shortenHome(l), cDim, err, cOff)
			continue
		}
		removed++
	}
	errf("%sdeleted%s %d local file(s)", cOk, cOff, removed)
}

func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// --- output ---------------------------------------------------------------

var cOk, cSkip, cWarn, cErr, cDim, cOff string

func init() {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("NOCOLOR") != "" || !isTTY(os.Stderr) {
		return
	}
	cOk, cSkip, cWarn = "\033[32m", "\033[90m", "\033[33m"
	cErr, cDim, cOff = "\033[31m", "\033[2m", "\033[0m"
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func errf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
