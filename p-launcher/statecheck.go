package main

// The launcher's check on the files it reads. Everything the session brief
// shows comes from HANDOFF.md, and the parser that reads it is position-
// sensitive: it fails silently, so a file that looks right to a human can
// brief as blank (GOTCHAS.md has both traps and what they cost). This turns
// that class of failure into something the F5 row and the report say out loud
// on every look, instead of something noticed by reading a rendered brief.
//
// Every check runs the real parser and compares what it got against what the
// file plainly offers a human. A check that reimplemented the parsing would
// pass while the parser kept dropping the slot, which is the whole bug.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// handoffCeiling is the line count the project-state skill sets. Its
// counterpart is the prose "ceiling ~120 lines" in
// claude/skills/project-state/SKILL.md, pinned by
// TestHandoffCeilingMatchesProjectStateSkill against that literal.
const handoffCeiling = 120

// stateProblem is one thing wrong with a project's state files. Kind drives
// the row label and the report's grouping; Detail is the sentence that says
// what to do about it.
type stateProblem struct {
	Kind   string // missing | oversized | dropped
	Detail string
}

// short is the row label: the third column of an F5 row, beside phrases like
// "reviewer waiting for your reply". Kept to a few words on purpose - the
// detail belongs in the report, where there is room to read it.
func (p stateProblem) short() string {
	switch p.Kind {
	case "missing":
		return "no handoff"
	case "oversized":
		return "handoff too long"
	case "dropped":
		return "brief drops a slot"
	}
	return ""
}

// lineCount counts lines the way a person reading `wc -l` would, ignoring a
// trailing newline so a file and the same file without its last newline are
// not one line apart.
func lineCount(text string) int {
	t := strings.TrimRight(text, "\n")
	if t == "" {
		return 0
	}
	return strings.Count(t, "\n") + 1
}

// checkState reports what is wrong with <root>/<path>'s state files. Cheap
// enough for the menu to call on every project on every open: one stat, one
// read, and the parsing the brief already does.
func checkState(root, path string) []stateProblem {
	b, err := os.ReadFile(filepath.Join(root, path, "HANDOFF.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return []stateProblem{{Kind: "missing", Detail: "no HANDOFF.md: nothing tells the next session what is true"}}
	}
	if err != nil {
		return []stateProblem{{Kind: "missing", Detail: "HANDOFF.md unreadable: " + err.Error()}}
	}
	text := string(b)
	// Severity order: the row shows only the first, and a slot the brief
	// silently drops misinforms every session that reads it, where an
	// oversized file only asks for tidying.
	out := droppedSlots(text)
	if n := lineCount(text); n > handoffCeiling {
		out = append(out, stateProblem{Kind: "oversized",
			Detail: "HANDOFF.md is " + strconv.Itoa(n) + " lines against a ceiling of " +
				strconv.Itoa(handoffCeiling) + ": move a section out, do not trim wording"})
	}
	return out
}

// reFieldLine matches the start of another header field, so a wrapped
// sentence is told apart from the next field beginning.
var reFieldLine = regexp.MustCompile(`^[A-Z][A-Za-z ]{0,24}:`)

// bulletish reports that a line is a list item to a human eye, whatever
// indentation or marker it uses. mdItems only counts "- " and "* " at column
// zero, so anything else here is an item the brief will not count.
func bulletish(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")
}

// droppedSlots reports every slot the file fills for a human but the parser
// reads as empty. Each check pairs a real parser call with a plain-eye scan
// of the same text; they disagreeing is the defect.
func droppedSlots(text string) []stateProblem {
	var out []stateProblem

	// reLastAction is single-line. A wrapped sentence loses everything after
	// the first line, with no warning - the trap that ate half of two live
	// handoffs' last action.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "Last action:") || i+1 >= len(lines) {
			continue
		}
		nxt := lines[i+1]
		if strings.TrimSpace(nxt) == "" || strings.HasPrefix(nxt, "#") || reFieldLine.MatchString(nxt) || bulletish(nxt) {
			break
		}
		out = append(out, stateProblem{Kind: "dropped",
			Detail: "`Last action:` wraps onto the next line and the brief prints only the first: put the sentence on one line"})
		break
	}

	next := mdSection(text, "Next")
	// reProgress wants the digits immediately after the colon. Prose in
	// between drops the count silently, which is how one project briefed with
	// no progress at all for days.
	if reProgress.FindStringSubmatch(next) == nil {
		for _, line := range strings.Split(next, "\n") {
			if strings.HasPrefix(line, "Progress:") {
				out = append(out, stateProblem{Kind: "dropped",
					Detail: "`Progress:` has no N/M the brief can read: put the digits first, prose after"})
				break
			}
		}
	}
	// firstUncheckedItem only sees "- [ ] " at column zero.
	if firstUncheckedItem(next) == "" {
		for _, line := range strings.Split(next, "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "- [ ] ") || strings.HasPrefix(t, "* [ ] ") {
				out = append(out, stateProblem{Kind: "dropped",
					Detail: "the next step is indented or uses `*`, so the brief shows none: start it with `- [ ] ` at column zero"})
				break
			}
		}
	}
	// The two counted sections. An indented item reads fine and counts zero.
	for _, name := range []string{"Open decisions", "Unverified"} {
		body := mdSection(text, name)
		if body == "" || mdItems(body) > 0 {
			continue
		}
		for _, line := range strings.Split(body, "\n") {
			if bulletish(line) {
				out = append(out, stateProblem{Kind: "dropped",
					Detail: "`## " + name + "` has items the brief counts as none: unindent them to column zero"})
				break
			}
		}
	}
	return out
}

// StateFileRow is one problem in the report's friction section: one line of
// work, so a project with two problems gets two rows.
type StateFileRow struct {
	Path, Kind, Detail string
}

// scanState checks every project on disk and returns the problems sorted by
// path, how many projects have at least one, and how many were scanned. The
// denominator matters: "4 of 15" is a backlog, "4 of 4" is a broken
// convention.
func scanState(root string) (rows []StateFileRow, affected, scanned int) {
	found, err := Discover(root)
	if err != nil {
		return nil, 0, 0
	}
	for _, f := range found {
		scanned++
		ps := checkState(root, f.Path)
		if len(ps) > 0 {
			affected++
		}
		for _, p := range ps {
			rows = append(rows, StateFileRow{Path: f.Path, Kind: p.Kind, Detail: p.Detail})
		}
	}
	return rows, affected, scanned
}

// stateKindRank orders the report's table the way the row orders its label:
// a slot the brief drops is misinformation, a missing handoff is an absence,
// an oversized one is only untidy.
var stateKindRank = map[string]int{"dropped": 0, "missing": 1, "oversized": 2}

// capStateRows keeps the n worst rows and counts the rest, so the fixed-height
// friction panel cannot push the sections below it off the page. The same
// shape as the live section's LiveHidden.
func capStateRows(rows []StateFileRow, n int) ([]StateFileRow, int) {
	out := append([]StateFileRow(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		return stateKindRank[out[i].Kind] < stateKindRank[out[j].Kind]
	})
	if len(out) <= n {
		return out, 0
	}
	return out[:n], len(out) - n
}
