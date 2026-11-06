package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// runTick is `claude-review tick [--dry-run | --reset | --self-check]`, run
// by a timer.
func runTick(args []string, d Deps) int {
	fs := flag.NewFlagSet("claude-review tick", flag.ContinueOnError)
	fs.SetOutput(d.Stderr)
	dry := fs.Bool("dry-run", false, "print what tick would do, and do none of it")
	reset := fs.Bool("reset", false, "let rounds start again after the breaker tripped, and do nothing else")
	check := fs.Bool("self-check", false, "check Claude Code's background sessions in a scratch repo now, and do nothing else")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || btoi(*dry)+btoi(*reset)+btoi(*check) > 1 {
		return 2
	}
	cfg, err := LoadConfig(ConfigPath(d.UserConfigDir), d.Home)
	if err != nil {
		return fail(d, err)
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
	if *reset {
		return resetBreaker(d)
	}
	if *check {
		return selfCheckNow(d, cfg)
	}
	if len(cfg.ReviewRepos) == 0 {
		fmt.Fprintln(d.Stderr, "claude-review tick: review_repos is empty; nothing to run")
		return 0
	}

	user, err := githubUser(d, cfg)
	if err != nil {
		return fail(d, fmt.Errorf("claude-review tick: %w", err))
	}
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	defer func() { _ = l.Close() }()

	h := newTickHost(d, cfg, l, user)
	h.dry = *dry
	if err := h.tick(); err != nil {
		fmt.Fprintln(d.Stderr, "claude-review tick:", err)
		return 1
	}
	return 0
}

// githubUser is the login rounds post as: the config's, else gh's.
func githubUser(d Deps, cfg Config) (string, error) {
	if cfg.GitHubUser != "" {
		return cfg.GitHubUser, nil
	}
	out, err := d.Gh("api", "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("who am I on GitHub: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// newTickHost wires a tickHost to the machine; a click uses one to settle
// the PR's last round before it starts the next.
func newTickHost(d Deps, cfg Config, l *Ledger, user string) tickHost {
	return tickHost{
		cfg:         cfg,
		tcfg:        TickConfig{ReviewRepos: cfg.ReviewRepos, User: user, DailyCap: cfg.DailyCap, MaxRunning: cfg.MaxRunning, TotalDailyCap: cfg.TotalDailyCap, Wrapper: cfg.RoundWrapper},
		ledger:      l,
		elly:        Elly{Base: cfg.EllyURL, TokenFile: ellyTokenFile(d.RuntimeDir)},
		gh:          GitHub{Gh: d.Gh, User: user},
		running:     func(id string) bool { return RoundRunning(d.ClaudeDir, id) },
		blocked:     func(id string) bool { return JobState(d.ClaudeDir, id) == "blocked" },
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
		now:    time.Now,
		out:    d.Stderr,
		lock:   d.Lock,
		sess:   d.Sessions,
		exists: func(id string) bool { return d.SessionExists(d.ClaudeDir, id) },
		sessionIndex: func() (map[string]SessionHit, error) {
			return IndexSessions(d.ClaudeDir, isRoundSession)
		},
		trust: d.Trust,
		version: func() (string, error) {
			out, err := d.Capture("", viaInteractiveShell(claudeCmd+" --version"))
			if err != nil {
				return "", err
			}
			// The interactive shell's rc files may print first.
			if v := lastLineOr(out, ""); v != "" {
				return v, nil
			}
			return "", errors.New("no output")
		},
		selfCheck: func() error {
			return selfChecker{sess: d.Sessions, run: d.Output, trust: d.Trust, newID: randomUUID,
				dir: filepath.Join(filepath.Dir(LedgerPath(d.StateDir)), "self-check"), sleep: d.Sleep}.check()
		},
		notify: func(msg string) { _, _ = d.Output("", notifyArgv(d.GOOS, "claude-review", msg)) },
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// selfCheckNow is `tick --self-check`: the check tick runs on a new Claude
// Code version, run by hand. It records the version on a pass and trips the
// breaker on a failure, as tick would.
func selfCheckNow(d Deps, cfg Config) int {
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	defer func() { _ = l.Close() }()
	h := newTickHost(d, cfg, l, "")
	v, err := h.version()
	if err != nil {
		return fail(d, fmt.Errorf("claude-review tick: claude --version: %w", err))
	}
	if err := h.runSelfCheck(v); err != nil {
		fmt.Fprintln(d.Stderr, "claude-review tick:", err)
		return 1
	}
	fmt.Fprintln(d.Stderr, "claude-review tick: the self-check passed on", v)
	return 0
}

func resetBreaker(d Deps) int {
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	defer func() { _ = l.Close() }()
	if err := l.Reset(time.Now()); err != nil {
		return fail(d, err)
	}
	return 0
}

// ellyTokenFile is where elly writes the annotation write token, under
// runtimeDir, $XDG_RUNTIME_DIR (Deps.RuntimeDir).
func ellyTokenFile(runtimeDir string) string {
	return filepath.Join(runtimeDir, "elly", "annotation-token")
}

// recordAcceptance is the user's click, written down: tick only ever runs a
// PR the user opened, at the head and base they opened it at. Only for repos
// the runner covers, and never fatal to the click. It returns the head it
// recorded, "" when it recorded none, and closed when GitHub reports the PR
// merged or closed: then nothing is recorded and no round is owed.
func recordAcceptance(d Deps, cfg Config, pr PR) (head string, closed bool) {
	if !repoAllowed(pr.Owner, pr.Repo, cfg.ReviewRepos) {
		return "", false
	}
	p, err := GitHub{Gh: d.Gh}.pull(pr)
	if err == nil && pullState(p) != prOpen {
		return "", true
	}
	if err == nil {
		var l *Ledger
		if l, err = OpenLedger(LedgerPath(d.StateDir)); err == nil {
			err = l.Accept(pr, p.Head.SHA, p.Base.Ref, time.Now())
			_ = l.Close()
		}
	}
	if err != nil {
		fmt.Fprintf(d.Stderr, "claude-review: %s not recorded for unattended rounds: %v\n", pr.URL(), err)
		return "", false
	}
	if err := requestOwnReview(d, cfg, pr, p); err != nil {
		fmt.Fprintf(d.Stderr, "claude-review: could not add you as a reviewer of %s: %v\n", pr.URL(), err)
	}
	return p.Head.SHA, false
}

// requestOwnReview makes the user a reviewer of a PR they handed over. A PR
// asked about only in a DM involves the user in nothing elly searches for,
// so its rounds' notes had no row to land on until the round posted. Not
// on the user's own PR, nor when they are already asked or have reviewed:
// re-requesting would show the author a review still owed.
func requestOwnReview(d Deps, cfg Config, pr PR, p ghPull) error {
	me, err := githubUser(d, cfg)
	if err != nil {
		return err
	}
	if strings.EqualFold(p.User.Login, me) || slices.ContainsFunc(p.RequestedReviewers, func(r ghLogin) bool { return strings.EqualFold(r.Login, me) }) {
		return nil
	}
	reviews, err := ghList(d.Gh, GitHub{}.api(pr, "pulls/%d/reviews", pr.Number))
	if err != nil {
		return err
	}
	if slices.ContainsFunc(reviews, func(r ghItem) bool { return strings.EqualFold(r.User.Login, me) }) {
		return nil
	}
	_, err = d.Gh("api", "-X", "POST", GitHub{}.api(pr, "pulls/%d/requested_reviewers", pr.Number), "-f", "reviewers[]="+me)
	return err
}
