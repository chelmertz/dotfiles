package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// cut splits "m/dependabot" into ("m", "dependabot", true). Only tests use
// it; production code goes through UpsertProjects' Found values instead.
func cut(path string) (ns, name string, ok bool) {
	return strings.Cut(path, "/")
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "sub", "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	var fk int
	if err := s.db.QueryRow(`pragma foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("pragma foreign_keys = %d, want 1 (DSN not pinned)", fk)
	}
	return s
}

func found(paths ...string) []Found {
	var out []Found
	for _, p := range paths {
		ns, name, _ := cut(p)
		out = append(out, Found{Namespace: ns, Name: name, Path: p})
	}
	return out
}

func TestUpsertAndListOrdering(t *testing.T) {
	s := openTestStore(t)
	// personal before m on disk to prove sorting is by namespace.sort_order
	if err := s.UpsertProjects(found("personal/health", "m/reputation", "m/dependabot", "m/nginx-ingress")); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProjects(found("personal/health", "m/reputation", "m/dependabot", "m/nginx-ingress")); err != nil {
		t.Fatal("second upsert must be a no-op:", err)
	}
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if err := s.recordAt("m/reputation", "launch", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.recordAt("m/nginx-ingress", "focus", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.recordAt("m/reputation", "focus", t0.Add(2*time.Hour)); err != nil { // newest → first
		t.Fatal(err)
	}

	ps, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.Path)
	}
	want := []string{"m/reputation", "m/nginx-ingress", "m/dependabot", "personal/health"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if ps[0].Label != "matchi" || ps[3].Label != "personal" {
		t.Fatalf("labels: %q %q", ps[0].Label, ps[3].Label)
	}
	if ps[0].LastActive != "2026-09-01T12:00:00Z" || ps[2].LastActive != "" {
		t.Fatalf("last active: %q %q", ps[0].LastActive, ps[2].LastActive)
	}
}

func TestUpsertUnknownNamespaceAppendsSortOrder(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("oss/thing", "m/x")); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].Path != "m/x" || ps[1].Path != "oss/thing" || ps[1].Label != "oss" {
		t.Fatalf("got %+v", ps)
	}
}

func TestRecordActivityRejectsUnknownPathAndKind(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/x")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity("m/nope", "launch"); err == nil {
		t.Fatal("want error for unknown path")
	}
	if err := s.RecordActivity("m/x", "poke"); err == nil {
		t.Fatal("want error for unknown kind")
	}
	if err := s.RecordActivity("m/x", "launch"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStateAggregate(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a", "m/b", "m/c", "personal/d")); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	// m/a: newest activity, one session working
	if err := s.recordAt("m/a", "launch", t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.setStateAt("s1", "m/a", "claude", "", t0); err != nil {
		t.Fatal(err)
	}
	// m/b: older activity, two sessions, one needs you → project needs you and sorts first
	if err := s.recordAt("m/b", "launch", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.setStateAt("s2", "m/b", "claude", "", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.setStateAt("s3", "m/b", "you", "", t0); err != nil {
		t.Fatal(err)
	}
	// m/c: stale needs-you (older than 24h) → ignored
	if err := s.setStateAt("s4", "m/c", "you", "", t0.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// personal/d: needs you, but a different namespace → still after all of m
	if err := s.setStateAt("s5", "personal/d", "you", "", t0); err != nil {
		t.Fatal(err)
	}
	ps, err := s.listProjectsAt(true, t0.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.Path+":"+p.Ball)
	}
	want := []string{"m/b:you", "m/a:claude", "m/c:", "personal/d:you"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	// transitions overwrite, SessionEnd clears
	if err := s.setStateAt("s3", "m/b", "claude", "", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearSession("s2"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearSession("never-seen"); err != nil {
		t.Fatal(err)
	}
	ps, err = s.listProjectsAt(true, t0.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].Path != "m/a" || ps[1].Path != "m/b" || ps[1].Ball != "claude" {
		t.Fatalf("after transition: %+v", ps[:2])
	}
}

func TestSessionStateUnknownProject(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetSessionState("s1", "m/nope", "claude", ""); err == nil {
		t.Fatal("unknown project accepted")
	}
}

func TestRecordSessionEvent(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSessionEvent(SessionEvent{SessionID: "s1", Path: "m/a", Cwd: "/home/x/p/m/a/sub", Kind: "prompt"}); err != nil {
		t.Fatal(err)
	}
	// outside ~/p: no project, still recorded
	if err := s.RecordSessionEvent(SessionEvent{SessionID: "s2", Cwd: "/home/x/code", Kind: "notification", Detail: "permission_prompt"}); err != nil {
		t.Fatal(err)
	}
	var n, nulls int
	if err := s.db.QueryRow(`select count(*), sum(project_id is null) from session_event`).Scan(&n, &nulls); err != nil {
		t.Fatal(err)
	}
	if n != 2 || nulls != 1 {
		t.Fatalf("rows=%d nulls=%d", n, nulls)
	}
	var detail string
	if err := s.db.QueryRow(`select detail from session_event where session_id='s2'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != "permission_prompt" {
		t.Fatalf("detail %q", detail)
	}
	// unknown project path is an error, not a silent NULL
	if err := s.RecordSessionEvent(SessionEvent{SessionID: "s3", Path: "m/nope", Cwd: "/x", Kind: "stop"}); err == nil {
		t.Fatal("unknown project accepted")
	}
}

func TestLinkKindsOwnerAndNamespaces(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLinkKind("m/a", "https://github.com/o/r/issues/3", "github_issue"); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := s.db.QueryRow(`select kind from link`).Scan(&kind); err != nil || kind != "github_issue" {
		t.Fatalf("%q %v", kind, err)
	}
	if p, ok := s.LinkOwner("https://github.com/o/r/issues/3"); !ok || p != "m/a" {
		t.Fatalf("%q %v", p, ok)
	}
	if _, ok := s.LinkOwner("https://github.com/o/r/issues/4"); ok {
		t.Fatal("unknown url owned")
	}
	if s.NamespaceFor("matchiapp") != "" {
		t.Fatal("unmapped owner must be empty")
	}
	if err := s.SetNamespaceFor("matchiapp", "m"); err != nil {
		t.Fatal(err)
	}
	if s.NamespaceFor("matchiapp") != "m" {
		t.Fatal("mapping not stored")
	}
	ns, err := s.Namespaces()
	if err != nil || len(ns) != 2 || ns[0] != "m" || ns[1] != "personal" {
		t.Fatalf("%v %v", ns, err)
	}
}

func TestAdoptEvents(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	for _, e := range []SessionEvent{
		{SessionID: "old", Cwd: "/home/x/code/repo", Kind: "prompt"},
		{SessionID: "old", Cwd: "/home/x/code/repo/sub", Kind: "stop"},
		{SessionID: "other", Cwd: "/home/x/code/repo-two", Kind: "prompt"}, // prefix trap
		{SessionID: "mine", Path: "m/a", Cwd: "/home/x/p/m/a", Kind: "prompt"},
	} {
		if err := s.RecordSessionEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.AdoptEvents("m/a", "/home/x/code/repo/")
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	var orphans, mine int
	if err := s.db.QueryRow(`select sum(project_id is null), sum(project_id is not null) from session_event`).Scan(&orphans, &mine); err != nil || orphans != 1 || mine != 3 {
		t.Fatalf("orphans=%d mine=%d %v", orphans, mine, err)
	}
	if _, err := s.AdoptEvents("m/nope", "/x"); err == nil {
		t.Fatal("unknown project accepted")
	}
}

func TestSetDescription(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDescription("m/nope", "x"); err == nil {
		t.Fatal("unknown project accepted")
	}
	if err := s.SetDescription("m/a", "  one sentence  "); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.ListProjects()
	if ps[0].Description != "one sentence" {
		t.Fatalf("%q", ps[0].Description)
	}
	if err := s.SetDescription("m/a", ""); err != nil {
		t.Fatal(err)
	}
	if ps, _ = s.ListProjects(); ps[0].Description != "" {
		t.Fatalf("not cleared: %q", ps[0].Description)
	}
}

func TestSessionBallReason(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if st, why, err := s.SessionBall("nope"); err != nil || st != "" || why != "" {
		t.Fatalf("%q %q %v", st, why, err)
	}
	if err := s.SetSessionState("s1", "m/a", "you", "permission_prompt"); err != nil {
		t.Fatal(err)
	}
	st, why, err := s.SessionBall("s1")
	if err != nil || st != "you" || why != "permission_prompt" {
		t.Fatalf("%q %q %v", st, why, err)
	}
	if err := s.SetSessionState("s1", "m/a", "claude", ""); err != nil {
		t.Fatal(err)
	}
	if st, why, _ := s.SessionBall("s1"); st != "claude" || why != "" {
		t.Fatalf("%q %q", st, why)
	}
}

func TestReapDead(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	for sid, pid := range map[string]int{"dead": 41, "alive": 42, "legacy": 0} {
		if err := s.SetSessionState(sid, "m/a", "you", "stop"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSessionPID(sid, pid); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ReapDead(func(pid int) bool { return pid == 42 })
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	var left int
	if err := s.db.QueryRow(`select count(*) from session_state`).Scan(&left); err != nil || left != 2 {
		t.Fatalf("left=%d %v: the pid-less row must survive, the dead one must go", left, err)
	}
	if st, _, err := s.SessionBall("dead"); err == nil && st != "" {
		t.Fatalf("dead session still has state %q", st)
	}
	var kind, detail string
	if err := s.db.QueryRow(`select kind, detail from session_event where session_id = 'dead'`).Scan(&kind, &detail); err != nil || kind != "session_end" || detail != "reaped" {
		t.Fatalf("%s %s %v", kind, detail, err)
	}
	// a second pass is a no-op
	if n, err := s.ReapDead(func(int) bool { return false }); err != nil || n != 1 {
		t.Fatalf("second pass reaped %d (only 'alive' carried a pid) %v", n, err)
	}
}
