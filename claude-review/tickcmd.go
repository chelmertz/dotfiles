package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runTick is `claude-review tick [--dry-run]`, run by a timer.
func runTick(args []string, d Deps) int {
	fs := flag.NewFlagSet("claude-review tick", flag.ContinueOnError)
	fs.SetOutput(d.Stderr)
	dry := fs.Bool("dry-run", false, "print what tick would do, and do none of it")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	cfg, err := LoadConfig(ConfigPath(d.UserConfigDir), d.Home)
	if err != nil {
		return fail(d, err)
	}
	if len(cfg.ReviewRepos) == 0 {
		fmt.Fprintln(d.Stderr, "claude-review tick: review_repos is empty; nothing to run")
		return 0
	}
	// One tick at a time: a timer run and a manual one would both start
	// the same round.
	unlock, err := d.Lock("tick")
	if errors.Is(err, errLocked) {
		return 0
	}
	if err != nil {
		return fail(d, err)
	}
	defer unlock()

	user := cfg.GitHubUser
	if user == "" {
		out, err := d.Gh("api", "user", "--jq", ".login")
		if err != nil {
			return fail(d, fmt.Errorf("claude-review tick: who am I on GitHub: %w", err))
		}
		user = strings.TrimSpace(out)
	}
	l, err := OpenLedger(LedgerPath(d.Home))
	if err != nil {
		return fail(d, err)
	}
	defer func() { _ = l.Close() }()

	h := tickHost{
		cfg:    cfg,
		tcfg:   TickConfig{ReviewRepos: cfg.ReviewRepos, User: user, DailyCap: cfg.DailyCap, MaxRunning: cfg.MaxRunning, Wrapper: cfg.RoundWrapper},
		ledger: l,
		elly:   Elly{Base: cfg.EllyURL, TokenFile: ellyTokenFile()},
		gh:     GitHub{Gh: d.Gh, User: user},
		running: func(id string) bool {
			return d.SessionRunning(d.ClaudeDir, id).PID > 0
		},
		lastMessage: func(id string) (string, error) { return LastMessage(d.ClaudeDir, id) },
		findRepo: func(pr PR) (string, string, error) {
			return d.FindRepo(cfg.GitRepoRoots, cfg.ClonePath, pr)
		},
		start: func(dir string, argv []string) error {
			out, err := d.Output(dir, viaInteractiveShell(shellLine(dir, argv)))
			if err != nil {
				return fmt.Errorf("starting the round: %s", lastLineOr(out, err.Error()))
			}
			return nil
		},
		now: time.Now,
		out: d.Stderr,
		dry: *dry,
	}
	if err := h.tick(); err != nil {
		fmt.Fprintln(d.Stderr, "claude-review tick:", err)
		return 1
	}
	return 0
}

// ellyTokenFile is where elly writes the annotation write token.
func ellyTokenFile() string {
	return filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "elly", "annotation-token")
}

// recordAcceptance is the user's click, written down: tick only ever runs a
// PR the user opened, at the head and base they opened it at. Only for repos
// the runner covers, and never fatal to the click.
func recordAcceptance(d Deps, cfg Config, pr PR) {
	if !repoAllowed(pr.Owner, pr.Repo, cfg.ReviewRepos) {
		return
	}
	p, err := GitHub{Gh: d.Gh}.pull(pr)
	if err == nil {
		var l *Ledger
		if l, err = OpenLedger(LedgerPath(d.Home)); err == nil {
			err = l.Accept(pr, p.Head.SHA, p.Base.Ref, time.Now())
			_ = l.Close()
		}
	}
	if err != nil {
		fmt.Fprintf(d.Stderr, "claude-review: %s not recorded for unattended rounds: %v\n", pr.Slug(), err)
	}
}
