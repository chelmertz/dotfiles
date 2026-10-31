package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfig_MissingFileIsDefaults(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"), "/srv/x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Terminal != (TerminalSetting{}) || cfg.PostWithoutAsking || cfg.ReviewPrompt != "" {
		t.Errorf("unexpected non-default: %+v", cfg)
	}
	if len(cfg.GitRepoRoots) != 1 || cfg.GitRepoRoots[0] != "/srv/x/code" {
		t.Errorf("roots: %v", cfg.GitRepoRoots)
	}
}

func TestLoadConfig_UnreadableFileIsAnError(t *testing.T) {
	dir := t.TempDir() // a directory, not a file
	if _, err := LoadConfig(dir, "/h"); err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("want read error naming the path, got %v", err)
	}
}

func TestLoadConfig_BadJSONIsAnError(t *testing.T) {
	p := writeConfig(t, `{"post_without_asking": tru`)
	if _, err := LoadConfig(p, "/h"); err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("want parse error naming the path, got %v", err)
	}
}

func TestLoadConfig_ReadsEveryKey(t *testing.T) {
	p := writeConfig(t, `{
	  "terminal": "kitty --directory {dir} {script}",
	  "post_without_asking": true,
	  "git_repo_roots": ["~/work", "/srv/git", "~"],
	  "review_prompt": "be nice"
	}`)
	cfg, err := LoadConfig(p, "/srv/x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Terminal.Disabled || cfg.Terminal.Command != "kitty --directory {dir} {script}" {
		t.Errorf("terminal: %+v", cfg.Terminal)
	}
	if !cfg.PostWithoutAsking || cfg.ReviewPrompt != "be nice" {
		t.Errorf("%+v", cfg)
	}
	want := []string{"/srv/x/work", "/srv/git", "/srv/x"}
	if strings.Join(cfg.GitRepoRoots, ",") != strings.Join(want, ",") {
		t.Errorf("roots: %v", cfg.GitRepoRoots)
	}
}

func TestTerminalSetting_ThreeStates(t *testing.T) {
	cases := map[string]TerminalSetting{
		`{}`:                           {},
		`{"terminal": null}`:           {},
		`{"terminal": true}`:           {},
		detachedCfg:                    {Disabled: true},
		`{"terminal": "foo {script}"}`: {Command: "foo {script}"},
	}
	for in, want := range cases {
		cfg, err := LoadConfig(writeConfig(t, in), "/h")
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if cfg.Terminal != want {
			t.Errorf("%s: got %+v want %+v", in, cfg.Terminal, want)
		}
	}
}

func TestTerminalSetting_RejectsNonsense(t *testing.T) {
	for _, in := range []string{`{"terminal": 3}`, `{"terminal": ""}`, `{"terminal": "  "}`, `{"terminal": ["a"]}`, `{"terminal": {"a":1}}`} {
		if _, err := LoadConfig(writeConfig(t, in), "/h"); err == nil {
			t.Errorf("%s: expected an error", in)
		}
	}
}

func TestConfigPath(t *testing.T) {
	if got := ConfigPath("/srv/x/.config"); got != "/srv/x/.config/claude-review/config.json" {
		t.Errorf("got %q", got)
	}
}

func TestLoadConfig_UnknownKeyIsAnError(t *testing.T) {
	p := writeConfig(t, `{"post_without_askin": true}`)
	_, err := LoadConfig(p, "/h")
	if err == nil || !strings.Contains(err.Error(), "post_without_askin") {
		t.Fatalf("want an error naming the unknown key, got %v", err)
	}
}

func TestLoadConfig_ClonePathExpandsHome(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `{"clone_path": "~/code/github/{owner}/{repo}"}`), "/srv/x")
	if err != nil || cfg.ClonePath != "/srv/x/code/github/{owner}/{repo}" {
		t.Errorf("got %q, %v", cfg.ClonePath, err)
	}
}

// A clone at clone_path must be found by the next click, so the path has to
// end in {repo} and sit within maxRepoDepth of a root.
func TestLoadConfig_ClonePathMustBeFindable(t *testing.T) {
	for _, p := range []string{
		"~/code/{owner}",
		"~/code/{repo}/src",
		"~/elsewhere/{repo}",
		"~/code/a/b/c/{repo}",
		"~/code/.hidden/{repo}",
	} {
		_, err := LoadConfig(writeConfig(t, `{"clone_path": "`+p+`"}`), "/srv/x")
		if err == nil || !strings.Contains(err.Error(), "clone_path") {
			t.Errorf("%s: want a clone_path error, got %v", p, err)
		}
	}
}

func TestLoadConfigReadsReviewReposFromALocalFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"review_repos":["a/*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	local := "# work repos, kept out of the dotfiles\nb/one\n\n  c/*  \n"
	if err := os.WriteFile(filepath.Join(dir, "review-repos"), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath, "/h")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.ReviewRepos, ",") != "a/*,b/one,c/*" {
		t.Errorf("review_repos %q", cfg.ReviewRepos)
	}
}
