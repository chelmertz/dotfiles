package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// version is set with -ldflags "-X main.version=..." by a release build.
var version = "dev"

// Deps is everything run needs from the outside world, so the whole flow is
// testable without a desktop, a terminal or Claude Code installed.
type Deps struct {
	GOOS          string
	UserConfigDir string
	Home          string
	ClaudeDir     string
	// StateDir is the XDG state home: $XDG_STATE_HOME, else ~/.local/state.
	StateDir string
	// RuntimeDir is $XDG_RUNTIME_DIR, where elly writes its token.
	RuntimeDir string
	// Environ is the process environment, read once by realDeps.
	Environ       []string
	IsTTY         bool
	Stdout        io.Writer
	Stderr        io.Writer
	FindRepo      func(roots []string, clonePath string, pr PR) (dir, remote string, err error)
	SessionExists func(claudeDir, id string) bool
	// SessionRunning reports the live process holding the session, if any.
	SessionRunning func(claudeDir, id string) Live
	// Lock takes the per-PR lock a click holds from planning to its end;
	// errLocked means another click holds it.
	Lock func(id string) (unlock func(), err error)
	// Sleep waits between polls.
	Sleep func(time.Duration)
	// Exec runs argv in dir with the terminal attached and waits.
	Exec func(dir string, argv []string) error
	// Output runs argv in dir, waits, and returns its combined output.
	Output func(dir string, argv []string) (string, error)
	// Capture runs argv in dir, waits, and returns its stdout alone; stderr
	// is attached to the error only, so a warning cannot pass for output.
	Capture func(dir string, argv []string) (string, error)
	// Spawn starts argv in dir and returns without waiting.
	Spawn func(dir string, argv []string) error
	// WriteScript writes an executable file named name and returns its path.
	WriteScript func(name, content string) (string, error)
	// Gh runs the GitHub CLI, for the subcommands.
	Gh Gh
	// Sessions keeps one background worker per PR (see Relaunch).
	Sessions Sessions
	// Trust marks a clone trusted for Claude Code before a background launch.
	Trust func(dir string) error
}

const usageText = `usage: claude-review [--dry-run] <pr>
       claude-review post <pr> comment|inline|reply|review ...
       claude-review threads <pr>
       claude-review show <pr> <path> | grep <pr> <text> | diff <pr>
       claude-review approve <pr> --at SHA
       claude-review slack [--dry-run] <pr>...
       claude-review tick [--dry-run | --reset]

  <pr>   a GitHub PR URL, claude-review://owner/repo/number or owner/repo/number

Starts, or resumes, the Claude Code review session for that PR in the local
clone. Config: ` + "`claude-review/config.json`" + ` under the user config directory.
` + "`claude-review post`" + ` is what the session uses to post; see its --help.
` + "`claude-review threads`" + ` prints what is already on the PR; ` + "`show`, `grep` and `diff`" + `
read the PR's code at its head without giving the session git.
` + "`claude-review approve <pr> --at SHA`" + ` approves SHA if it is still the head;
it is yours to run, and the session is denied it.
` + "`claude-review slack`" + ` prints a line per PR to paste to its reviewer, and
refuses a PR that is red, pending, conflicting, a draft or owes a reply.
` + "`claude-review tick`" + ` runs owed rounds on accepted PRs in review_repos, unattended,
and reports them to elly; a timer runs it.
`

// subcommands are the gh-backed commands beside the default launch.
var subcommands = map[string]func([]string, Gh, io.Writer, io.Writer) int{
	"threads": runThreads,
	"approve": runApprove,
}

// runSessionCommand runs the commands a review session uses: post, with
// its budget, and the git reads.
func runSessionCommand(args []string, d Deps) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	if args[0] == "post" {
		return runPost(args[1:], d.Gh, ledgerSpend(d), d.Environ, d.Stdout, d.Stderr), true
	}
	if sub, ok := readCommands[args[0]]; ok {
		return sub(args[1:], newPRReader(d), d.Stdout, d.Stderr), true
	}
	return 0, false
}

// run is main without os: returns the exit code. 0 ok, 1 failure, 2 usage.
func run(args []string, d Deps) int {
	if len(args) > 0 && args[0] == "tick" {
		return runTick(args[1:], d)
	}
	if code, ok := runSessionCommand(args, d); ok {
		return code
	}
	if len(args) > 0 && args[0] == "slack" {
		// Needs Claude as well as gh, so it takes the whole host.
		return runSlack(args[1:], d)
	}
	if len(args) > 0 {
		if sub, ok := subcommands[args[0]]; ok {
			return sub(args[1:], d.Gh, d.Stdout, d.Stderr)
		}
	}
	fs := flag.NewFlagSet("claude-review", flag.ContinueOnError)
	fs.SetOutput(d.Stderr)
	fs.Usage = func() { fmt.Fprint(d.Stderr, usageText) }
	dryRun := fs.Bool("dry-run", false, "print the command instead of running it")
	showVersion := fs.Bool("version", false, "print the version")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(d.Stdout, "claude-review", version)
		return 0
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	cfg, err := LoadConfig(ConfigPath(d.UserConfigDir), d.Home)
	if err != nil {
		return fail(d, err)
	}
	if pr, verb, q, ok := ParseVerb(fs.Arg(0)); ok {
		return runVerb(d, cfg, pr, verb, q)
	}
	pr, err := ParseTarget(fs.Arg(0))
	if err != nil {
		return fail(d, err)
	}
	dir, remote, err := d.FindRepo(cfg.GitRepoRoots, cfg.ClonePath, pr)
	if err != nil {
		return fail(d, err)
	}

	return launch(d, cfg, pr, dir, remote, *dryRun)
}

// launch plans one click on pr in dir and runs it, or prints it. A PR in
// review_repos runs in its background worker (backgroundClick); any other
// opens a window, under the user's own settings.
func launch(d Deps, cfg Config, pr PR, dir, remote string, dryRun bool) int {
	background := repoAllowed(pr.Owner, pr.Repo, cfg.ReviewRepos)
	if dryRun {
		plan, err := planClick(d, cfg, pr, remote)
		if background {
			plan, err = planBackgroundClick(d, cfg, pr, remote)
		}
		if err != nil {
			return fail(d, err)
		}
		for _, argv := range plan.commands() {
			fmt.Fprintln(d.Stdout, shellLine(dir, argv))
		}
		return 0
	}
	// One click at a time per PR, from reading the session's state to the
	// end of the click: two clicks planned together would both see no
	// session and both start one. Tick and the attach verb take it too.
	unlock, err := d.Lock(pr.SessionID())
	if errors.Is(err, errLocked) {
		return fail(d, fmt.Errorf("claude-review: another click is already opening %s", pr.Slug()))
	}
	if err != nil {
		return fail(d, fmt.Errorf("claude-review: locking %s: %w", pr.Slug(), err))
	}
	defer unlock()
	head, closed := recordAcceptance(d, cfg, pr)
	if closed {
		// Only a PR in review_repos is asked about. tick's sweep would stop
		// the round anyway; refusing here spares it a slot meanwhile.
		return fail(d, fmt.Errorf("claude-review: %s is merged/closed; no round", pr.URL()))
	}
	if background {
		return backgroundClick(d, cfg, pr, dir, remote, head)
	}
	plan, err := planClick(d, cfg, pr, remote)
	if err != nil {
		return fail(d, err)
	}
	if err := plan.execute(d, cfg, dir); err != nil {
		return fail(d, err)
	}
	return 0
}

// clickStarter starts a click's round like tick's, through the
// interactive shell (a URL handler's PATH rarely has claude).
func clickStarter(d Deps, cfg Config) roundStarter {
	return roundStarter{sess: d.Sessions, wrapper: cfg.RoundWrapper,
		exists: func(id string) bool { return d.SessionExists(d.ClaudeDir, id) },
		run: func(dir string, argv []string) error {
			if out, err := d.Output(dir, viaInteractiveShell(shellLine(dir, argv))); err != nil {
				return fmt.Errorf("%s: %s", argv[0], lastLineOr(out, err.Error()))
			}
			return nil
		}}
}

// planBackgroundClick is what a click on a PR in review_repos would run,
// for --dry-run: the round session's bootstrap when it has none, then its
// launch, or nothing when the session saved other options.
func planBackgroundClick(d Deps, cfg Config, pr PR, remote string) (click, error) {
	id := pr.RoundSessionID()
	existed := d.SessionExists(d.ClaudeDir, id)
	argv, err := d.Sessions.LaunchArgv(id, PolicyFlags(pr, remote), RoundArgs(cfg, pr, remote, existed), ResumeArgs(cfg, pr, remote, existed))
	if err != nil {
		return click{}, fmt.Errorf("claude-review: %s: nothing starts: %w", pr.Slug(), err)
	}
	return click{pr: pr, bootstrap: !existed, round: true, detached: true, argv: append(append([]string{}, cfg.RoundWrapper...), argv...)}, nil
}

// backgroundClick runs a click on a PR in review_repos in the PR's one
// background worker, under the pinned round policy, and notifies: nobody
// watches it, and watching is `claude attach`. Like tick it starts nothing
// while the breaker is tripped, settles the last round before stopping its
// idle worker, and records the round once the worker is listed, so tick
// settles it like its own. head is the PR head the click saw ("" when
// GitHub did not say; then nothing is recorded).
func backgroundClick(d Deps, cfg Config, pr PR, dir, remote, head string) int {
	l, err := OpenLedger(LedgerPath(d.StateDir))
	if err != nil {
		return fail(d, err)
	}
	defer func() { _ = l.Close() }()
	if reason, err := l.Tripped(); err != nil || reason != "" {
		return fail(d, fmt.Errorf("claude-review: %s: %w", pr.Slug(), errors.Join(err, breakerError(reason))))
	}
	// read before the round starts: it records a round and a running note
	before, err := l.Handover(pr)
	if err != nil {
		return fail(d, err)
	}
	if err := d.Trust(dir); err != nil {
		return fail(d, fmt.Errorf("claude-review: trusting %s for Claude Code: %w", dir, err))
	}
	settle := func() error {
		user, err := githubUser(d, cfg)
		if err != nil {
			return err
		}
		return newTickHost(d, cfg, l, user).settleUnread(pr)
	}
	// No out: a click reports through its notification, and the running
	// row's own failures through say.
	h := tickHost{ledger: l, elly: Elly{Base: cfg.EllyURL, TokenFile: ellyTokenFile(d.RuntimeDir)}}
	say := func(err error) {
		if err != nil {
			fmt.Fprintf(d.Stderr, "claude-review: %s: %v\n", pr.Slug(), err)
		}
	}
	at := time.Now()
	res, err := clickStarter(d, cfg).start(cfg, pr, dir, remote, false, settle)
	if err != nil && res.Kind != relaunchDuplicate {
		say(h.dropRunning(pr))
		return fail(d, fmt.Errorf("claude-review: %s: %w", pr.Slug(), err))
	}
	started, err := h.afterRelaunch(pr, res, err, func() error {
		if head == "" {
			return nil // nothing recorded, so nothing would settle a running row
		}
		if _, err := l.StartRound(pr, head, clickTrigger(at), at); err != nil {
			return err
		}
		say(h.markRunning(pr, "a click", at))
		return nil
	})
	if err != nil {
		say(h.dropRunning(pr))
	}
	return clickOutcome(d, pr, res, started, err, before)
}

// clickOutcome tells the user what a background click did, and on a start
// what came of the PR's earlier handovers.
func clickOutcome(d Deps, pr PR, res RelaunchResult, started bool, err error, before Handover) int {
	switch {
	case err != nil:
		return fail(d, fmt.Errorf("claude-review: %s: %w", pr.Slug(), err))
	case started:
		tell(d, "review of "+pr.URL()+" started"+before.words())
		return 0
	case res.Kind == relaunchBusy:
		tell(d, pr.URL()+" is mid-round; it continues in the background")
		return 0
	case res.Kind == relaunchWindow:
		return fail(d, fmt.Errorf("claude-review: %s: %s", pr.URL(), windowNote))
	}
	return fail(d, fmt.Errorf("claude-review: %s: the round was launched but %s", pr.URL(), res.Detail))
}

// click is what one click will do, decided before anything runs.
type click struct {
	pr        PR
	bootstrap bool     // no transcript yet: create the session headlessly first
	attach    bool     // a background worker is running it: attach, do not resume
	round     bool     // the PR's round session (review_repos), not its window session
	detached  bool     // terminal: false
	argv      []string // the review (or attach) command
}

// planClick inspects the session's state and picks the commands. A session
// that is open, and cannot be attached to from here, is an error: any
// command would start a second copy of it.
func planClick(d Deps, cfg Config, pr PR, remote string) (click, error) {
	id := pr.SessionID()
	live := d.SessionRunning(d.ClaudeDir, id)
	detached := cfg.Terminal.Disabled
	switch {
	case live.PID > 0 && !live.Background:
		// A terminal window, or another click's bootstrap mid-turn. Neither
		// takes an attach; the window is where the next round is asked for.
		return click{}, fmt.Errorf("claude-review: %s is already open (pid %d); ask for a new round in that window", pr.Slug(), live.PID)
	case live.PID > 0 && detached:
		// `--bg --resume` on a running session would start a copy.
		return click{}, fmt.Errorf("claude-review: %s is still running as %s; `claude attach %s` opens it", pr.Slug(), id, id)
	case live.PID > 0:
		return click{pr: pr, attach: true, argv: AttachArgs(pr)}, nil
	}
	existed := d.SessionExists(d.ClaudeDir, id)
	return click{pr: pr, bootstrap: !existed, detached: detached, argv: ReviewArgs(cfg, pr, remote, existed, detached)}, nil
}

// commands lists what execute will run, for --dry-run.
func (c click) commands() [][]string {
	var all [][]string
	switch {
	case c.bootstrap && c.round:
		all = append(all, RoundBootstrapArgs(c.pr))
	case c.bootstrap:
		all = append(all, BootstrapArgs(c.pr))
	}
	return append(all, c.argv)
}

// execute runs the click. Captured runs go through the interactive shell
// because a URL handler's PATH rarely has claude; from a tty it is harmless.
func (c click) execute(d Deps, cfg Config, dir string) error {
	pr, id := c.pr, c.pr.SessionID()
	switch {
	case c.attach:
		// A running background session refuses --resume (before 2.1.285),
		// takes the prompt (later) or is forked by it (2.1.314, when idle);
		// attaching is right on all of them and never forks. The user types
		// the re-review request in the attached view.
		fmt.Fprintf(d.Stderr, "claude-review: %s is still running; attaching instead of resuming\n", pr.Slug())
	case c.bootstrap && (c.detached || d.IsTTY):
		// First click: create the session headlessly under our id, then
		// resume it like every later click. A new window does this itself
		// (see openTerminal), so it opens before the bootstrap's turn.
		out, err := d.Output(dir, viaInteractiveShell(shellLine(dir, BootstrapArgs(pr))))
		if err != nil {
			// Claude's own last line (e.g. "Workspace not trusted. Run
			// `claude` in <dir> once…") is the useful part; the exit status
			// alone would send the user to a terminal to find it.
			return fmt.Errorf("claude-review: creating session for %s: %s", pr.Slug(), lastLineOr(out, err.Error()))
		}
	}

	switch {
	case c.detached:
		out, err := d.Output(dir, viaInteractiveShell(shellLine(dir, c.argv)))
		if err != nil {
			return fmt.Errorf("claude-review: starting background session: %s", lastLineOr(out, err.Error()))
		}
		fmt.Fprint(d.Stderr, out)
		fmt.Fprintf(d.Stderr, "claude-review: %s running in the background as %s; `claude attach %s` opens it\n", pr.Slug(), id, id)
		return nil
	case d.IsTTY:
		if err := d.Exec(dir, c.argv); err != nil {
			return fmt.Errorf("claude-review: claude exited: %w", err)
		}
		return nil
	default:
		steps := [][]string{c.argv}
		if c.bootstrap {
			// The bootstrap turn takes seconds; run it in the window rather
			// than before opening it, so the click shows something at once.
			steps = [][]string{bootstrapNotice, BootstrapArgs(pr), c.argv}
		}
		if err := openTerminal(d, cfg, dir, steps, pr); err != nil {
			return err
		}
		if !c.attach {
			awaitWindow(d, id, c.bootstrap)
		}
		return nil
	}
}

// awaitPolls and awaitPoll bound awaitWindow: two minutes covers a slow
// shell start and a bootstrap turn.
const (
	awaitPolls = 480
	awaitPoll  = 250 * time.Millisecond
)

// awaitWindow keeps the click, and so its lock, alive until the session the
// window starts is live; until then a second click would see none and open
// another window. With a bootstrap first, its `claude -p` holds the id for a
// turn of several seconds and exits, so the window's session is the next pid
// after the first one seen. Timing out is silent: the window is open, and
// the next click sees whatever it became.
func awaitWindow(d Deps, id string, bootstrap bool) {
	first := 0
	for range awaitPolls {
		if pid := d.SessionRunning(d.ClaudeDir, id).PID; pid > 0 {
			if !bootstrap || (first > 0 && pid != first) {
				return
			}
			first = pid
		}
		d.Sleep(awaitPoll)
	}
}

// bootstrapNotice is what a new window shows while the bootstrap turn runs.
var bootstrapNotice = []string{"echo", "claude-review: creating the review session..."}

// openTerminal writes the launcher script and starts a terminal running it.
func openTerminal(d Deps, cfg Config, dir string, steps [][]string, pr PR) error {
	script, err := d.WriteScript("claude-review-"+pr.SessionID()+".sh", launcherScript(dir, steps...))
	if err != nil {
		return fmt.Errorf("claude-review: writing launcher: %w", err)
	}
	tmpl := cfg.Terminal.Command
	if tmpl == "" {
		tmpl = defaultTerminalTemplate(d.GOOS)
	}
	termArgv, err := terminalArgv(tmpl, dir, script)
	if err != nil {
		return err
	}
	if err := d.Spawn(dir, termArgv); err != nil {
		return fmt.Errorf("claude-review: opening terminal with %q: %w", termArgv[0], err)
	}
	return nil
}

// lastLineOr is the last non-empty line of out, or fallback when out is blank.
func lastLineOr(out, fallback string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return fallback
}

// fail reports err where the user will see it: stderr always, and a desktop
// notification when there is no terminal to read stderr in.
func fail(d Deps, err error) int {
	fmt.Fprintln(d.Stderr, err)
	if d.IsTTY {
		return 1
	}
	var nf *notFoundError
	switch {
	case errors.As(err, &nf) && d.GOOS != darwin:
		notifyCopy(d, nf)
	case errors.As(err, &nf):
		// osascript notifications take no actions: the command is the body.
		_ = d.Spawn("", notifyArgv(d.GOOS, "claude-review", nf.notification(d.Home, false)))
	default:
		_ = d.Spawn("", notifyArgv(d.GOOS, "claude-review", strings.TrimPrefix(err.Error(), "claude-review: ")))
	}
	return 1
}

// notifyCopy shows the short "not cloned" notification and waits for it. Its
// one action is named "default", which dunst's do_action (middle click by
// default) invokes without a menu; it copies the clone command. Dismissed or
// expired, notify-send prints nothing and nothing is copied.
func notifyCopy(d Deps, nf *notFoundError) {
	out, _ := d.Capture("", []string{"notify-send", "--action=default=Copy clone command", "--", "claude-review", nf.notification(d.Home, true)})
	if strings.TrimSpace(out) != "default" {
		return
	}
	// xclip stays behind to serve the selection; with its output kept open
	// the capture would wait for it.
	copyCmd := []string{"sh", "-c", `printf %s "$1" | xclip -selection clipboard >/dev/null 2>&1`, "sh", nf.Command()}
	if _, err := d.Capture("", copyCmd); err != nil {
		_ = d.Spawn("", notifyArgv(d.GOOS, "claude-review", "could not copy (is xclip installed?); run:\n"+nf.Command()))
	}
}

// words is the handover history as the tail of one notification line.
func (h Handover) words() string {
	if h.Rounds == 0 {
		return " (first handover)"
	}
	s := fmt.Sprintf(": handed over %s, %d round", h.First.Local().Format("Jan 2"), h.Rounds)
	if h.Rounds != 1 {
		s += "s"
	}
	if h.LastNote != "" {
		s += ", last: " + capRunes(h.LastNote, 80)
	}
	return s
}
