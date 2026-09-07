package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func listPaths(t *testing.T, s *Store, all bool) map[string]bool {
	t.Helper()
	ps, err := s.ListProjectsFiltered(all)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, p := range ps {
		out[p.Path] = p.Archived
	}
	return out
}

func TestListProjectsArchived(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a", "m/b")); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectEvent("m/a", "archived", "done"); err != nil {
		t.Fatal(err)
	}
	if got := listPaths(t, s, false); len(got) != 1 || got["m/a"] {
		t.Fatalf("default list must hide archived: %v", got)
	}
	if got := listPaths(t, s, true); len(got) != 2 || !got["m/a"] || got["m/b"] {
		t.Fatalf("all: %v", got)
	}
	// ListProjects() keeps its old meaning: everything, for hooks and the report
	ps, err := s.ListProjects()
	if err != nil || len(ps) != 2 {
		t.Fatalf("%v %v", ps, err)
	}
	if err := s.ProjectEvent("m/a", "reopened", ""); err != nil {
		t.Fatal(err)
	}
	if got := listPaths(t, s, false); len(got) != 2 || got["m/a"] {
		t.Fatalf("reopened is back: %v", got)
	}
	if err := s.ProjectEvent("m/a", "bogus", ""); err == nil {
		t.Fatal("bogus kind accepted")
	}
	if err := s.ProjectEvent("m/nope", "archived", "done"); err == nil {
		t.Fatal("unknown project accepted")
	}
}

func TestCreate(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	mk(t, root, "m")
	dir, err := Create(s, root, "m/new")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "m", "new") {
		t.Fatalf("dir %s", dir)
	}
	b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || string(b) != "# new\n" {
		t.Fatalf("CLAUDE.md %q %v", b, err)
	}
	if got := listPaths(t, s, false); !reflect.DeepEqual(got, map[string]bool{"m/new": false}) {
		t.Fatalf("%v", got)
	}
	var kind string
	if err := s.db.QueryRow(`select kind from project_event`).Scan(&kind); err != nil || kind != "created" {
		t.Fatalf("%q %v", kind, err)
	}
	if _, err := Create(s, root, "m/new"); err == nil {
		t.Fatal("existing dir accepted")
	}
	if _, err := Create(s, root, "nope/x"); err == nil {
		t.Fatal("unknown namespace accepted")
	}
	if _, err := Create(s, root, "m/../x"); err == nil {
		t.Fatal("dot segment accepted")
	}
	if _, err := Create(s, root, "m"); err == nil {
		t.Fatal("no name accepted")
	}
}

func TestRename(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	mk(t, root, "m/a")
	mk(t, root, "m/taken")
	if err := s.UpsertProjects(found("m/a", "m/taken")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSessionEvent(SessionEvent{SessionID: "s1", Path: "m/a", Cwd: "/x", Kind: "prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectEvent("m/a", "created", ""); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "x/y", "a b", "..", "taken"} {
		if _, err := Rename(s, root, "m/a", bad); err == nil {
			t.Fatalf("rename to %q accepted", bad)
		}
	}
	if _, err := Rename(s, root, "m/nope", "b"); err == nil {
		t.Fatal("unknown project accepted")
	}
	newPath, err := Rename(s, root, "m/a", "b")
	if err != nil || newPath != "m/b" {
		t.Fatalf("%q %v", newPath, err)
	}
	if !isDir(filepath.Join(root, "m", "b")) || isDir(filepath.Join(root, "m", "a")) {
		t.Fatal("directory not moved")
	}
	got := listPaths(t, s, true)
	if _, old := got["m/a"]; old || len(got) != 2 {
		t.Fatalf("%v", got)
	}
	ps, _ := s.ListProjects()
	for _, p := range ps {
		if p.Path == "m/b" && p.Name != "b" {
			t.Fatalf("name not updated: %+v", p)
		}
	}
	// history follows the row: events and lifecycle stay attached
	var n int
	if err := s.db.QueryRow(`select count(*) from session_event e join project p on p.id = e.project_id where p.path = 'm/b'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events lost: %d %v", n, err)
	}
	if err := s.db.QueryRow(`select count(*) from project_event pe join project p on p.id = pe.project_id where p.path = 'm/b'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("project events lost: %d %v", n, err)
	}
}

func TestPostpone(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a", "m/b")); err != nil {
		t.Fatal(err)
	}
	now := ts("2026-09-08T09:00:00Z")
	if _, err := Postpone(s, "m/a", 0, now); err == nil {
		t.Fatal("zero days accepted")
	}
	until, err := Postpone(s, "m/a", 3, now)
	if err != nil || !until.Equal(now.AddDate(0, 0, 3)) {
		t.Fatalf("%v %v", until, err)
	}
	ps, _ := s.listProjectsAt(false, now.Add(time.Hour))
	if len(ps) != 1 || ps[0].Path != "m/b" {
		t.Fatalf("postponed project still listed: %+v", ps)
	}
	ps, _ = s.listProjectsAt(true, now.Add(time.Hour))
	if len(ps) != 2 || !(ps[0].Snoozed || ps[1].Snoozed) {
		t.Fatalf("all must include it as snoozed: %+v", ps)
	}
	// Claude waiting on the user overrides the snooze
	if err := s.setStateAt("s1", "m/a", "you", "stop", now); err != nil {
		t.Fatal(err)
	}
	ps, _ = s.listProjectsAt(false, now.Add(time.Hour))
	if len(ps) != 2 || ps[0].Path != "m/a" || !ps[0].Snoozed {
		t.Fatalf("needs-you must surface a postponed project first: %+v", ps)
	}
	if err := s.ClearSession("s1"); err != nil {
		t.Fatal(err)
	}
	// the wake time passes: back by itself
	ps, _ = s.listProjectsAt(false, until.Add(time.Minute))
	if len(ps) != 2 || ps[0].Snoozed || ps[1].Snoozed {
		t.Fatalf("expired snooze must lift: %+v", ps)
	}
	// opening a postponed project wakes it explicitly
	if _, err := Postpone(s, "m/a", 10, now); err != nil {
		t.Fatal(err)
	}
	woke, err := reopenIfArchived(s, "m/a")
	if err != nil || !woke {
		t.Fatalf("%v %v", woke, err)
	}
	var kind string
	if err := s.db.QueryRow(`select kind from project_event order by id desc limit 1`).Scan(&kind); err != nil || kind != "woken" {
		t.Fatalf("%q %v", kind, err)
	}
	if ps, _ = s.listProjectsAt(false, now.Add(time.Hour)); len(ps) != 2 {
		t.Fatalf("woken project hidden: %+v", ps)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH (the Nix build sandbox); dirty-clone detection is skipped")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestArchive(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	dir := mk(t, root, "m/a")
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"Bash(go test *)", "Bash(ls *)", "Bash(go test *)"} {
		if err := s.RecordPermission("s1", "m/a", "Bash", r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddLink("m/a", "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionState("s1", "m/a", "claude", ""); err != nil {
		t.Fatal(err)
	}
	gitInit(t, filepath.Join(dir, "repo"))
	if err := os.WriteFile(filepath.Join(dir, "repo", "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, filepath.Join(dir, "clean"))
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil { // not a repo
		t.Fatal(err)
	}
	var copied string
	clip := func(text string) error { copied = text; return nil }
	cl, err := Archive(s, root, "m/a", "done", clip)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cl.Rules, []RuleCount{{"Bash(go test *)", 2}, {"Bash(ls *)", 1}}) {
		t.Fatalf("rules %+v", cl.Rules)
	}
	if !reflect.DeepEqual(cl.OpenLinks, []string{"https://github.com/o/r/pull/1"}) || cl.LiveSessions != 1 {
		t.Fatalf("%+v", cl)
	}
	if !reflect.DeepEqual(cl.DirtyClones, []string{"repo"}) {
		t.Fatalf("dirty %v", cl.DirtyClones)
	}
	if !strings.Contains(copied, "Bash(go test *)") || !strings.Contains(copied, "Bash(ls *)") {
		t.Fatalf("clipboard %q", copied)
	}
	text := cl.Text()
	for _, want := range []string{"archived m/a (done)", "Bash(go test *)", "pull/1", "1 live session", "repo"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
	var kind, detail string
	if err := s.db.QueryRow(`select kind, detail from project_event order by id desc limit 1`).Scan(&kind, &detail); err != nil || kind != "archived" || detail != "done" {
		t.Fatalf("%s %s %v", kind, detail, err)
	}
	if got := listPaths(t, s, false); len(got) != 0 {
		t.Fatalf("still listed: %v", got)
	}
	if _, err := Archive(s, root, "m/a", "meh", clip); err == nil {
		t.Fatal("bad reason accepted")
	}
	// reopen on open
	re, err := reopenIfArchived(s, "m/a")
	if err != nil || !re {
		t.Fatalf("%v %v", re, err)
	}
	if got := listPaths(t, s, false); len(got) != 1 {
		t.Fatalf("not reopened: %v", got)
	}
	if re, _ := reopenIfArchived(s, "m/a"); re {
		t.Fatal("reopened twice")
	}
}
