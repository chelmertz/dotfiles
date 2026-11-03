package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// claudeJSONPath is Claude Code's global state file, which holds per-folder
// trust: configDir/.claude.json when $CLAUDE_CONFIG_DIR (configDir) is set,
// else ~/.claude.json.
func claudeJSONPath(home, configDir string) string {
	if configDir != "" {
		return filepath.Join(configDir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// trusted reads projects[dir].hasTrustDialogAccepted from the file at path.
func trusted(path, dir string) (bool, map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path) //nolint:gosec // Claude Code's own state file
	if err != nil {
		return false, nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return false, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var projects map[string]map[string]json.RawMessage
	if raw, ok := top["projects"]; ok {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return false, nil, fmt.Errorf("reading %s: projects: %w", path, err)
		}
	}
	return string(projects[dir]["hasTrustDialogAccepted"]) == "true", top, nil
}

// ensureTrusted trusts dir for Claude Code, which refuses `claude --bg` in a
// folder it has not trusted ("Workspace not trusted"). Rounds load none of
// the clone's settings or hooks, the reason the trust prompt exists, so the
// prompt has nothing left to guard for them. Only that one key is written,
// every other value is kept as raw JSON, through a temp file and a rename in
// the same directory, mode 0600; the file is read back to verify. Claude
// Code rewrites this file itself, so a write can lose a race with it: the
// read-back is what catches that, and a launch only follows a verified trust.
func ensureTrusted(path, dir string) error {
	ok, top, err := trusted(path, dir)
	if err != nil || ok {
		return err
	}
	var projects map[string]map[string]json.RawMessage
	if raw, has := top["projects"]; has {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return err
		}
	}
	if projects == nil {
		projects = map[string]map[string]json.RawMessage{}
	}
	if projects[dir] == nil {
		projects[dir] = map[string]json.RawMessage{}
	}
	projects[dir]["hasTrustDialogAccepted"] = json.RawMessage("true")
	if top["projects"], err = json.Marshal(projects); err != nil {
		return err
	}
	if err := writeJSONAtomic(path, top); err != nil {
		return err
	}
	ok, _, err = trusted(path, dir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("trusting %s in %s did not hold", dir, path)
	}
	return nil
}

// writeJSONAtomic writes v indented to path through a temp file and a
// rename in the same directory, mode 0600.
func writeJSONAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.claude-review-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(out.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
