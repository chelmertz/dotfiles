package main

import (
	"path/filepath"
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
