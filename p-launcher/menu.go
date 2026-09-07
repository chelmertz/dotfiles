package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// load discovers projects, upserts them, and returns the display list plus
// the set of tags with open windows.
func load(s *Store, root string) ([]Project, map[string]bool, error) {
	found, err := Discover(root)
	if err != nil {
		return nil, nil, err
	}
	if err := s.UpsertProjects(found); err != nil {
		return nil, nil, err
	}
	ps, err := s.ListProjects()
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

// list prints TSV rows for other front ends: path, name, label, open, last_active.
func list(s *Store, root string, w io.Writer) error {
	ps, open, err := load(s, root)
	if err != nil {
		return err
	}
	return writeList(w, ps, open)
}

func writeList(w io.Writer, ps []Project, open map[string]bool) error {
	for _, p := range ps {
		o := "0"
		if open[tagFor(p.Path)] {
			o = "1"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Path, p.Name, p.Label, o, p.LastActive); err != nil {
			return err
		}
	}
	return nil
}

// menu shows rofi and opens the selection. Esc (rofi exit 1, empty stderr)
// is a quiet exit; a nonzero exit with stderr output is a fatal rofi
// startup failure (display, "already running", a bad theme), not a cancel.
func menu(s *Store, root string) error {
	ps, open, err := load(s, root)
	if err != nil {
		return err
	}
	rows := Rows(ps, open)
	if len(rows) == 0 {
		return errors.New("no projects under " + root)
	}
	var in bytes.Buffer
	for _, r := range rows {
		in.WriteString(r.Text + "\n")
	}
	// -format i prints the selected row index; -1 when the typed text matched
	// no row (the future create-project hook). No timeout: rofi waits for the user.
	cmd := exec.Command("rofi", "-dmenu", "-i", "-p", "project", "-format", "i", "-matching", "fuzzy")
	cmd.Stdin = &in
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := exitCode(err)
		if code == 1 && strings.TrimSpace(stderr.String()) == "" {
			return nil // cancelled with Esc
		}
		return &CmdError{Cmd: "rofi -dmenu", ExitCode: code, Stderr: clip(stderr.String())}
	}
	idx, ok := parseRofiIndex(stdout.String())
	if !ok {
		return fmt.Errorf("rofi returned unexpected output %q", stdout.String())
	}
	path, ok := selectRow(rows, idx)
	if !ok {
		if idx == -1 {
			// rofi gives only the index, not the typed text, so the
			// notification can't name what was typed.
			notifyInfo("no matching project; creating projects is not implemented yet")
		}
		return nil // typed non-match or divider: no-op for now
	}
	return Open(s, root, path)
}

// selectRow resolves a rofi row index to a project path, isolated from I/O
// for testing. ok is false for an out-of-range index or a divider row.
func selectRow(rows []Row, idx int) (string, bool) {
	if idx < 0 || idx >= len(rows) || rows[idx].Path == "" {
		return "", false
	}
	return rows[idx].Path, true
}

func parseRofiIndex(out string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(out))
	return n, err == nil
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
