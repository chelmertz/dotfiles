package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
// last_active, ball ("you", "claude" or empty), archived (0/1).
func list(s *Store, root string, w io.Writer, all bool) error {
	ps, open, err := load(s, root, all)
	if err != nil {
		return err
	}
	return writeList(w, ps, open)
}

func writeList(w io.Writer, ps []Project, open map[string]bool) error {
	for _, p := range ps {
		o, a := "0", "0"
		if open[tagFor(p.Path)] {
			o = "1"
		}
		if p.Archived {
			a = "1"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.Path, p.Name, p.Label, o, p.LastActive, p.Ball, a); err != nil {
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
	args := []string{"-dmenu", "-i", "-p", prompt, "-format", "i|f", "-matching", "fuzzy", "-markup-rows", "-show-icons"}
	if toggleKey != "" {
		args = append(args, "-kb-cancel", "Escape,Control+g,Control+bracketleft,"+toggleKey)
	}
	return args
}

// rofiInput renders rows for rofi: the text, then the dmenu row option
// "\0icon\x1f<path>" so every row has an icon cell (a transparent blank for
// closed projects keeps the column uniform). With no icon files, plain rows.
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
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// runRofi shows one dmenu and returns its raw output. cancelled is true for
// Esc or the toggle key (rofi exit 1 with empty stderr); a nonzero exit with
// stderr output is a fatal rofi startup failure (display, "already running",
// a bad theme), not a cancel. No timeout: rofi waits for the user.
func runRofi(prompt, toggleKey string, input []byte) (out string, cancelled bool, err error) {
	cmd := exec.Command("rofi", rofiArgs(prompt, toggleKey)...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := exitCode(err)
		// Fragile if rofi ever warns on a normal Esc; revisit then.
		if code == 1 && strings.TrimSpace(stderr.String()) == "" {
			return "", true, nil
		}
		detail := clip(stderr.String())
		if code == -1 && detail == "" {
			detail = err.Error() // never started: the start error is the trace
		}
		return "", false, &CmdError{Cmd: "rofi -dmenu", ExitCode: code, Stderr: detail}
	}
	return stdout.String(), false, nil
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
		ps = onlyArchived(ps)
	}
	rows := Rows(ps, open, !archived)
	if len(rows) == 0 || (archived && len(ps) == 0) {
		if archived {
			notifyInfo("no archived projects")
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
	prompt := "project"
	if archived {
		prompt = "archived"
	}
	out, cancelled, err := runRofi(prompt, toggleKey, rofiInput(rows, icons))
	if err != nil || cancelled {
		return err
	}
	idx, typed, ok := parseRofiOut(out)
	if !ok {
		return fmt.Errorf("rofi returned unexpected output %q", out)
	}
	if isArchivedTail(rows, idx) {
		return menuMode(s, root, toggleKey, iconDir, true)
	}
	if idx < 0 {
		// typed text that matched no row: offer to create it
		path := createPathFromTyped(typed)
		if path == "" {
			notifyInfo("no matching project; type <namespace>/<name> to create one")
			return nil
		}
		return verbMenu(s, root, toggleKey, Project{Path: path}, createRows(path))
	}
	path, ok := selectRow(rows, idx)
	if !ok {
		return nil
	}
	for _, p := range ps {
		if p.Path == path {
			return verbMenu(s, root, toggleKey, p, verbRows(p))
		}
	}
	return nil
}

func onlyArchived(ps []Project) []Project {
	var out []Project
	for _, p := range ps {
		if p.Archived {
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

// isArchivedTail reports whether idx is the "archived…" switch row.
func isArchivedTail(rows []Row, idx int) bool {
	return idx >= 0 && idx < len(rows) && rows[idx].Path == "" && rows[idx].Icon == "archived"
}

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
	verbRename
	verbAddLink
	verbContext
	verbCreate
)

type verbRow struct {
	text string
	verb verb
	arg  string
}

func verbRows(p Project) []verbRow {
	if p.Archived {
		return []verbRow{{"open (reopen)", verbOpen, ""}, {"context", verbContext, ""}}
	}
	return []verbRow{{"open", verbOpen, ""}, {"archive", verbArchive, ""}, {"rename", verbRename, ""}, {"add link", verbAddLink, ""}, {"context", verbContext, ""}}
}

func createRows(path string) []verbRow { return []verbRow{{"create " + path, verbCreate, path}} }

func reasonRows() []verbRow {
	return []verbRow{{"done", verbArchive, "done"}, {"scrapped", verbArchive, "scrapped"}, {"deprioritized", verbArchive, "deprioritized"}, {"solved elsewhere", verbArchive, "elsewhere"}}
}

func verbInput(vs []verbRow) []byte {
	var b bytes.Buffer
	for _, v := range vs {
		b.WriteString(v.text + "\n")
	}
	return b.Bytes()
}

// pickVerb shows verb rows and returns the chosen one; ok is false on cancel
// or a typed non-match.
func pickVerb(prompt, toggleKey string, vs []verbRow) (verbRow, bool, error) {
	out, cancelled, err := runRofi(prompt, toggleKey, verbInput(vs))
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
func verbMenu(s *Store, root, toggleKey string, p Project, vs []verbRow) error {
	v, ok, err := pickVerb(p.Path, toggleKey, vs)
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
		r, ok, err := pickVerb("reason", toggleKey, reasonRows())
		if err != nil || !ok {
			return err
		}
		cl, err := Archive(s, root, p.Path, r.arg, copyqCopy)
		if err != nil {
			return err
		}
		notifyInfo(cl.Text())
		return nil
	case verbRename:
		out, cancelled, err := runRofi("rename "+p.Name+" to", toggleKey, nil)
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
		out, cancelled, err := runRofi("url", toggleKey, nil)
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
