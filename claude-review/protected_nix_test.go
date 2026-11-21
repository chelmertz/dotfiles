package main

import (
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// elly's unit in nix/home.nix names the database and leaves the token at
// its default, $XDG_RUNTIME_DIR/elly/annotation-token; both must sit under a
// protected path, so moving either in the unit fails here rather than
// leaving a round able to read it. Stands in for a runtime file elly would
// write: the unit is the only place either path is chosen. Skipped where the
// nix file is not beside this module, as in the nix build's sandbox.
func TestEllysUnitPathsAreProtected(t *testing.T) {
	b, err := os.ReadFile("../nix/home.nix")
	if err != nil {
		t.Skip("nix/home.nix not beside this module")
	}
	exec := regexp.MustCompile(`\$\{pkgs\.elly\}/bin/elly[\s\S]*?''\}([^"]*)";`).FindSubmatch(b)
	if exec == nil {
		t.Fatal("no elly ExecStart in nix/home.nix")
	}
	args := strings.Fields(string(exec[1]))
	db := flagValue(args, "-db")
	if db == "" {
		t.Fatal("elly's unit names no -db")
	}
	token := flagValue(args, "-annotation-token-file")
	if token == "" {
		token = "/run/user/1000/elly/annotation-token" // the default, under XDG_RUNTIME_DIR
	}
	for _, p := range []string{db, db + "-wal", token, token + ".tmp"} {
		p = strings.Replace(p, "%h/", "~/", 1)
		if !protected(p) {
			t.Errorf("%s is under no protected path", p)
		}
	}
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// protected says whether a Read rule in protectedPaths covers p. "//x" is
// the absolute path /x, a trailing "/**" a whole tree, "**/" any directory.
func protected(p string) bool {
	return slices.ContainsFunc(protectedPaths, func(rule string) bool {
		rule = strings.Replace(rule, "//", "/", 1)
		if dir, ok := strings.CutSuffix(rule, "/**"); ok {
			for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
				if m, _ := path.Match(dir, d); m {
					return true
				}
			}
			return false
		}
		if name, ok := strings.CutPrefix(rule, "**/"); ok {
			m, _ := path.Match(name, path.Base(p))
			return m
		}
		m, _ := path.Match(rule, p)
		return m
	})
}

func TestProtectedMatchesTheRuleShapes(t *testing.T) {
	for p, want := range map[string]bool{
		"~/.local/share/elly/elly.db":     true,
		"/run/user/1000/elly/x.tmp":       true,
		"/srv/elly.db-wal":                true,
		"~/.local/share/other/elly":       false,
		"/run/user/1000/annotation-token": false,
		"~/code/x/.config/elly-not/thing": false,
	} {
		if got := protected(p); got != want {
			t.Errorf("protected(%q) = %v", p, got)
		}
	}
}
