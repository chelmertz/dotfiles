package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Config is the per-user configuration. Every key is optional; the zero
// value plus Defaults() is a working setup.
type Config struct {
	// Terminal is one of three things: absent (per-OS default), false (no
	// terminal, run the session detached with --bg) or a command template
	// with {dir} and {script} placeholders. One key, so "custom command but no
	// terminal" cannot be written.
	Terminal TerminalSetting `json:"terminal"`
	// PostWithoutAsking lets the session write to GitHub without a human
	// approving each call. Enforced as an --allowedTools permission, not as
	// a sentence in the prompt.
	PostWithoutAsking bool `json:"post_without_asking"`
	// GitRepoRoots are the directories searched for a folder named after
	// the repository.
	GitRepoRoots []string `json:"git_repo_roots"`
	// ClonePath is where the "not cloned" message suggests cloning to, a
	// template with {owner} and {repo}. Empty means <first root>/{repo}.
	ClonePath string `json:"clone_path"`
	// ReviewPrompt replaces the built-in first-round prompt. Later rounds
	// reuse it behind the fixed re-review preface.
	ReviewPrompt string `json:"review_prompt"`

	// The unattended runner, `claude-review tick`. ReviewRepos lists
	// "owner/*" or "owner/repo"; empty means tick runs nothing and clicks
	// record no acceptance. A review-repos file beside config.json adds one
	// per line (# comments), for names that should not sit in a shared config.
	ReviewRepos []string `json:"review_repos"`
	// DailyCap is how many rounds one PR may start in 24 hours (default 6).
	DailyCap int `json:"daily_cap"`
	// MaxRunning is how many unattended rounds may run at once (default 2).
	MaxRunning int `json:"max_running"`
	// RoundWrapper prefixes each unattended round, e.g. ["capped"].
	RoundWrapper []string `json:"round_wrapper"`
	// EllyURL is elly's address (default http://localhost:9876).
	EllyURL string `json:"elly_url"`
	// GitHubUser is the login rounds post as; empty asks gh.
	GitHubUser string `json:"github_user"`
}

// TerminalSetting is the three-state value of Config.Terminal.
type TerminalSetting struct {
	// Disabled is true when the JSON value was false.
	Disabled bool
	// Command is the custom template; empty means the per-OS default.
	Command string
}

// UnmarshalJSON accepts false, true (same as absent) or a string.
func (t *TerminalSetting) UnmarshalJSON(b []byte) error {
	// b is one JSON value the outer decoder has already validated, so this
	// cannot fail; the switch below rejects the types we do not take.
	var v any
	_ = json.Unmarshal(b, &v)
	switch x := v.(type) {
	case nil:
		*t = TerminalSetting{}
	case bool:
		*t = TerminalSetting{Disabled: !x}
	case string:
		if strings.TrimSpace(x) == "" {
			return errors.New(`"terminal" must be false or a non-empty command`)
		}
		*t = TerminalSetting{Command: x}
	default:
		return fmt.Errorf(`"terminal" must be false or a command string, got %s`, string(b))
	}
	return nil
}

// ConfigPath is <os.UserConfigDir()>/claude-review/config.json.
func ConfigPath(userConfigDir string) string {
	return filepath.Join(userConfigDir, "claude-review", "config.json")
}

// LoadConfig reads the file at path. A missing file is not an error: it
// yields the defaults. A present but unparseable file is an error, since a
// silently ignored typo would run a review with the wrong posting policy.
func LoadConfig(path, home string) (Config, error) {
	cfg := Config{}
	b, err := os.ReadFile(path) //nolint:gosec // path is derived from the user's own config dir
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Config{}, fmt.Errorf("claude-review: reading %s: %w", path, err)
	default:
		// Unknown keys are errors: a typo such as "post_without_askin" would
		// otherwise silently run with the other posting policy.
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("claude-review: parsing %s: %w", path, err)
		}
	}
	local, err := readLines(filepath.Join(filepath.Dir(path), "review-repos"))
	if err != nil {
		return Config{}, err
	}
	cfg.ReviewRepos = append(cfg.ReviewRepos, local...)
	if cfg.DailyCap == 0 {
		cfg.DailyCap = 6
	}
	if cfg.MaxRunning == 0 {
		cfg.MaxRunning = 2
	}
	if cfg.EllyURL == "" {
		cfg.EllyURL = "http://localhost:9876"
	}
	if len(cfg.GitRepoRoots) == 0 {
		cfg.GitRepoRoots = []string{"~/code"}
	}
	for i, r := range cfg.GitRepoRoots {
		cfg.GitRepoRoots[i] = expandHome(r, home)
	}
	if cfg.ClonePath != "" {
		cfg.ClonePath = expandHome(cfg.ClonePath, home)
		if !findable(cfg.ClonePath, cfg.GitRepoRoots) {
			return Config{}, fmt.Errorf("claude-review: clone_path %q must end in {repo}, at most %d levels below one of %s, with no hidden folder: a clone anywhere else is never found",
				cfg.ClonePath, maxRepoDepth, strings.Join(cfg.GitRepoRoots, ", "))
		}
	}
	return cfg, nil
}

// findable reports whether FindRepo would see a clone at path: the same
// depth and hidden-folder rules as dirsNamed.
func findable(path string, roots []string) bool {
	if filepath.Base(path) != "{repo}" {
		return false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		segs := strings.Split(rel, string(filepath.Separator))
		hidden := false
		for _, s := range segs {
			hidden = hidden || strings.HasPrefix(s, ".")
		}
		if len(segs) <= maxRepoDepth && !hidden {
			return true
		}
	}
	return false
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// readLines reads a list file: one entry per line, blank lines and # comments
// skipped. A missing file is an empty list.
func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a file beside the user's own config
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claude-review: reading %s: %w", path, err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out, nil
}
