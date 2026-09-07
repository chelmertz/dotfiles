package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// load discovers projects, upserts them, and returns the display list plus
// the set of tags with open windows. all includes archived projects.
func load(s *Store, root string, all bool) ([]Project, map[string]bool, error) {
	found, err := Discover(root)
	if err != nil {
		return nil, nil, err
	}
	if err := s.UpsertProjects(found); err != nil {
		return nil, nil, err
	}
	ps, err := s.ListProjectsFiltered(all)
	if err != nil {
		return nil, nil, err
	}
	tree, err := getTree()
	if err != nil {
		return nil, nil, err
	}
	open, err := OpenTags(tree)
	if err != nil {
		return nil, nil, err
	}
	return ps, open, nil
}

// list prints TSV rows for other front ends: path, name, label, open,
// last_active, ball ("you", "claude" or empty), archived (0/1), review (0/1:
// a fresh verdict says a linked PR waits on the user), description.
func list(s *Store, root string, w io.Writer, all bool) error {
	ps, open, err := load(s, root, all)
	if err != nil {
		return err
	}
	return writeList(w, ps, open)
}

func writeList(w io.Writer, ps []Project, open map[string]bool) error {
	for _, p := range ps {
		o, a, r := "0", "0", "0"
		if open[tagFor(p.Path)] {
			o = "1"
		}
		if p.Archived {
			a = "1"
		}
		if p.Review {
			r = "1"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.Path, p.Name, p.Label, o, p.LastActive, p.Ball, a, r, p.Description); err != nil {
			return err
		}
	}
	return nil
}

// rofiArgs builds the dmenu invocation. The output format is "i|f": the
// selected row index (-1 for a typed non-match) and the typed filter text,
// so a non-match can become a create. toggleKey, when set, is added to
// rofi's cancel binding so the same hotkey that opened the menu closes it
// (rofi grabs the keyboard, so i3 never sees the second press).
func rofiArgs(prompt, toggleKey string) []string {
	// -sync: read all rows before painting, so the window never shows a frame
	// without rows while the pipe drains (it did, intermittently).
	// -sep \x1e: rows are separated by the record separator so a row may
	// contain a newline (the description subtitle).
	args := []string{"-dmenu", "-sync", "-i", "-p", prompt, "-format", "i|f", "-matching", "fuzzy", "-markup-rows", "-show-icons", "-sep", rowSep}
	if toggleKey != "" {
		args = append(args, "-kb-cancel", "Escape,Control+g,Control+bracketleft,"+toggleKey)
	}
	return args
}

// rofiInput renders rows for rofi: the text, then the dmenu row option
// "\0icon\x1f<path>" so every row has an icon cell (a transparent blank for
// closed projects keeps the column uniform). With no icon files, plain rows.
const rowSep = "\x1e"

func rofiInput(rows []Row, icons map[string]string) []byte {
	var b bytes.Buffer
	for _, r := range rows {
		b.WriteString(r.Text)
		if icons != nil {
			p, ok := icons[r.Icon]
			if !ok {
				p = icons["blank"]
			}
			b.WriteString("\x00icon\x1f")
			b.WriteString(p)
		}
		b.WriteString(rowSep)
	}
	return b.Bytes()
}

// runRofi shows one dmenu and returns its raw output. cancelled is true for
// Esc or the toggle key (rofi exit 1 with empty stderr); a nonzero exit with
// stderr output is a fatal rofi startup failure (display, "already running",
// a bad theme), not a cancel. No timeout: rofi waits for the user.
// A custom key chord (-kb-custom-N) makes rofi exit with code 9+N; the
// returned key is N (1-based) or 0 for a plain selection.
func runRofi(prompt, toggleKey string, input []byte, extra ...string) (out string, key int, cancelled bool, err error) {
	cmd := exec.Command("rofi", append(rofiArgs(prompt, toggleKey), extra...)...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := exitCode(err)
		if code >= 10 && code <= 28 {
			return stdout.String(), code - 9, false, nil
		}
		// Fragile if rofi ever warns on a normal Esc; revisit then.
		if code == 1 && strings.TrimSpace(stderr.String()) == "" {
			return "", 0, true, nil
		}
		detail := clip(stderr.String())
		if code == -1 && detail == "" {
			detail = err.Error() // never started: the start error is the trace
		}
		return "", 0, false, &CmdError{Cmd: "rofi -dmenu", ExitCode: code, Stderr: detail}
	}
	return stdout.String(), 0, false, nil
}

// Chords for the docked actions, so they work however long the list is.
const (
	chordArchived = 1
	chordReport   = 2
)

func mainMenuExtra() []string {
	return []string{"-kb-custom-1", "Alt+a", "-kb-custom-2", "Alt+r",
		"-mesg", `<span alpha="60%">Alt+a archived · Alt+r report · ns/name creates</span>`}
}

// rowHeightArgs makes rows two lines tall when any project carries a
// description: rofi renders a row's second line only with -eh 2, and the
// taller rows are not worth it when nothing would fill them.
func rowHeightArgs(ps []Project) []string {
	for _, p := range ps {
		if p.Description != "" {
			return []string{"-eh", "2"}
		}
	}
	return nil
}

// parseRofiOut splits rofi's "i|f" output: index (-1 for a typed non-match)
// and the typed text. Only the first separator splits, so typed text may
// contain "|".
func parseRofiOut(out string) (idx int, typed string, ok bool) {
	out = strings.TrimSuffix(out, "\n")
	i, rest, found := strings.Cut(out, "|")
	if !found {
		return 0, "", false
	}
	n, err := strconv.Atoi(strings.TrimSpace(i))
	if err != nil {
		return 0, "", false
	}
	return n, strings.TrimSpace(rest), true
}

// menu is the F5 entry point: the project list, then a verb submenu for the
// chosen row. Every rofi cancel is a quiet exit.
func menu(s *Store, root, toggleKey, iconDir string) error {
	return menuMode(s, root, toggleKey, iconDir, false)
}

// menuMode shows ongoing projects (archived == false) with an "archived…"
// tail row that switches to the archived list, or the archived list itself.
func menuMode(s *Store, root, toggleKey, iconDir string, archived bool) error {
	ps, open, err := load(s, root, archived)
	if err != nil {
		return err
	}
	if archived {
		ps = onlyParked(ps)
	}
	rows := Rows(ps, open, !archived)
	if len(rows) == 0 || (archived && len(ps) == 0) {
		if archived {
			notifyInfo("no archived or postponed projects")
			return nil
		}
		return errors.New("no projects under " + root)
	}
	// Icons are a nicety: failing to write them is logged, not fatal.
	icons, err := writeIcons(iconDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "p-launcher: icons unavailable:", err)
		icons = nil
	}
	prompt, extra := "project", mainMenuExtra()
	if archived {
		prompt, extra = "archived / postponed", nil
	}
	extra = append(extra, rowHeightArgs(ps)...)
	out, key, cancelled, err := runRofi(prompt, toggleKey, rofiInput(rows, icons), extra...)
	if err != nil || cancelled {
		return err
	}
	idx, typed, ok := parseRofiOut(out)
	if !ok {
		return fmt.Errorf("rofi returned unexpected output %q", out)
	}
	action := tailOf(rows, idx)
	switch key {
	case chordArchived:
		action = "archived"
	case chordReport:
		action = "report"
	}
	switch action {
	case "archived":
		return menuMode(s, root, toggleKey, iconDir, true)
	case "report":
		// render all three ranges and open the 30d one; output stays quiet
		return runReport(reportOpts{rng: "30d", theme: "dark", open: true}, s, filepath.Dir(iconDir), io.Discard)
	}
	if idx < 0 {
		// typed text that matched no row: offer to create it
		path := createPathFromTyped(typed)
		if path == "" {
			notifyInfo("no matching project; type <namespace>/<name> to create one")
			return nil
		}
		return verbMenu(s, root, toggleKey, Project{Path: path}, createRows(path), icons)
	}
	path, ok := selectRow(rows, idx)
	if !ok {
		return nil
	}
	for _, p := range ps {
		if p.Path == path {
			return verbMenu(s, root, toggleKey, p, verbRows(p), icons)
		}
	}
	return nil
}

// onlyParked keeps archived and postponed projects: the second list.
func onlyParked(ps []Project) []Project {
	var out []Project
	for _, p := range ps {
		if p.Archived || p.Snoozed {
			out = append(out, p)
		}
	}
	return out
}

// selectRow resolves a rofi row index to a project path, isolated from I/O
// for testing. ok is false for an out-of-range index or a non-project row.
func selectRow(rows []Row, idx int) (string, bool) {
	if idx < 0 || idx >= len(rows) || rows[idx].Path == "" {
		return "", false
	}
	return rows[idx].Path, true
}

// tailOf names the docked row's action at idx ("archived", "report"), or ""
// for a project row or an invalid index.
func tailOf(rows []Row, idx int) string {
	if idx < 0 || idx >= len(rows) {
		return ""
	}
	return rows[idx].Action
}

// isArchivedTail reports whether idx is the "archived…" switch row.
func isArchivedTail(rows []Row, idx int) bool { return tailOf(rows, idx) == "archived" }

// createPathFromTyped accepts exactly "<namespace>/<name>" with clean
// segments and no spaces; anything else is "".
func createPathFromTyped(typed string) string {
	t := strings.TrimSpace(typed)
	seg := strings.Split(t, "/")
	if len(seg) != 2 || !cleanSegment(seg[0]) || !cleanSegment(seg[1]) || strings.ContainsAny(t, " \t") {
		return ""
	}
	return t
}

// Verbs of the action submenu. open is first so Enter-Enter opens a project.
type verb int

const (
	verbOpen verb = iota
	verbArchive
	verbPostpone
	verbRename
	verbDescribe
	verbAddLink
	verbContext
	verbCreate
)

type verbRow struct {
	text string
	verb verb
	arg  string
	icon string // name in iconPaths
}

func verbRows(p Project) []verbRow {
	if p.Archived {
		return []verbRow{{"open (reopen)", verbOpen, "", "reopened"}, {"context", verbContext, "", "context"}}
	}
	return []verbRow{{"open", verbOpen, "", "open"}, {"archive", verbArchive, "", "archived"}, {"postpone", verbPostpone, "", "snoozed"}, {"rename", verbRename, "", "rename"}, {"describe", verbDescribe, "", "describe"}, {"add link", verbAddLink, "", "link"}, {"context", verbContext, "", "context"}}
}

// postponeRows are the snooze lengths; arg is the number of days.
func postponeRows() []verbRow {
	return []verbRow{{"1 day", verbPostpone, "1", "snoozed"}, {"3 days", verbPostpone, "3", "snoozed"}, {"10 days", verbPostpone, "10", "snoozed"}}
}

func createRows(path string) []verbRow {
	return []verbRow{{"create " + path, verbCreate, path, "create"}}
}

func reasonRows() []verbRow {
	return []verbRow{{"done", verbArchive, "done", "done"}, {"scrapped", verbArchive, "scrapped", "scrapped"}, {"deprioritized", verbArchive, "deprioritized", "deprioritized"}, {"solved elsewhere", verbArchive, "elsewhere", "elsewhere"}}
}

// verbInput renders verb rows for rofi with their icons (same row option as
// the project rows); without icon files, plain rows.
func verbInput(vs []verbRow, icons map[string]string) []byte {
	rows := make([]Row, 0, len(vs))
	for _, v := range vs {
		rows = append(rows, Row{Text: v.text, Icon: v.icon})
	}
	return rofiInput(rows, icons)
}

// pickVerb shows verb rows and returns the chosen one; ok is false on cancel
// or a typed non-match.
func pickVerb(prompt, toggleKey string, vs []verbRow, icons map[string]string) (verbRow, bool, error) {
	out, _, cancelled, err := runRofi(prompt, toggleKey, verbInput(vs, icons))
	if err != nil || cancelled {
		return verbRow{}, false, err
	}
	idx, _, ok := parseRofiOut(out)
	if !ok || idx < 0 || idx >= len(vs) {
		return verbRow{}, false, nil
	}
	return vs[idx], true, nil
}

// verbMenu runs the submenu for one project and executes the verb.
func verbMenu(s *Store, root, toggleKey string, p Project, vs []verbRow, icons map[string]string) error {
	v, ok, err := pickVerb(p.Path, toggleKey, vs, icons)
	if err != nil || !ok {
		return err
	}
	switch v.verb {
	case verbOpen:
		return Open(s, root, p.Path)
	case verbCreate:
		if _, err := Create(s, root, v.arg); err != nil {
			return err
		}
		return Open(s, root, v.arg)
	case verbArchive:
		r, ok, err := pickVerb("reason", toggleKey, reasonRows(), icons)
		if err != nil || !ok {
			return err
		}
		cl, err := Archive(s, root, p.Path, r.arg, copyqCopy)
		if err != nil {
			return err
		}
		notifyInfo(cl.Text())
		return nil
	case verbPostpone:
		d, ok, err := pickVerb("postpone "+p.Name+" for", toggleKey, postponeRows(), icons)
		if err != nil || !ok {
			return err
		}
		days, _ := strconv.Atoi(d.arg)
		until, err := Postpone(s, p.Path, days, time.Now())
		if err != nil {
			return err
		}
		notifyInfo("postponed " + p.Path + " until " + until.Format("Mon Jan 02"))
		return nil
	case verbDescribe:
		// the current sentence is pre-filled so it can be edited, not retyped
		out, _, cancelled, err := runRofi("describe "+p.Name, toggleKey, nil, "-filter", p.Description)
		if err != nil || cancelled {
			return err
		}
		_, typed, ok := parseRofiOut(out)
		if !ok {
			return nil
		}
		if err := s.SetDescription(p.Path, typed); err != nil {
			return err
		}
		if typed == "" {
			notifyInfo("cleared the description of " + p.Path)
		} else {
			notifyInfo(p.Path + ": " + typed)
		}
		return nil
	case verbRename:
		out, _, cancelled, err := runRofi("rename "+p.Name+" to", toggleKey, nil)
		if err != nil || cancelled {
			return err
		}
		_, typed, ok := parseRofiOut(out)
		if !ok || typed == "" {
			return nil
		}
		newPath, err := Rename(s, root, p.Path, typed)
		if err != nil {
			return err
		}
		msg := "renamed " + p.Path + " → " + newPath
		if tree, err := getTree(); err == nil {
			if wins, err := FindTagged(tree, tagFor(p.Path)); err == nil && len(wins) > 0 {
				msg += fmt.Sprintf("; %d open window(s) keep the old tag until relaunched", len(wins))
			}
		}
		notifyInfo(msg)
		return nil
	case verbAddLink:
		out, _, cancelled, err := runRofi("url", toggleKey, nil)
		if err != nil || cancelled {
			return err
		}
		_, typed, ok := parseRofiOut(out)
		if !ok || !strings.HasPrefix(typed, "https://") {
			notifyInfo("no link added: expected an https:// URL")
			return nil
		}
		if err := s.AddLink(p.Path, typed); err != nil {
			return err
		}
		notifyInfo("linked " + typed + " to " + p.Path)
		return nil
	case verbContext:
		text, err := contextText(s, p.Path)
		if err != nil {
			return err
		}
		notifyInfo(text)
		return nil
	}
	return nil
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
