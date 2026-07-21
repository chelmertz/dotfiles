// Command keylog is a local keyboard-usage profiler. See DESIGN.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chelmertz/dotfiles/keylogger/internal/capture"
	"github.com/chelmertz/dotfiles/keylogger/internal/daemon"
	"github.com/chelmertz/dotfiles/keylogger/internal/feeder"
	"github.com/chelmertz/dotfiles/keylogger/internal/i3cfg"
	"github.com/chelmertz/dotfiles/keylogger/internal/keys"
	"github.com/chelmertz/dotfiles/keylogger/internal/metrics"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
	"github.com/chelmertz/dotfiles/keylogger/internal/report"
	"github.com/chelmertz/dotfiles/keylogger/internal/rules"
	"github.com/chelmertz/dotfiles/keylogger/internal/seed"
	"github.com/chelmertz/dotfiles/keylogger/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = cmdSeed(os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	case "start":
		err = cmdStart(os.Args[2:])
	case "stop":
		err = cmdStop()
	case "status":
		err = cmdStatus()
	case "ctx":
		err = cmdCtx(os.Args[2:])
	case "tail":
		err = cmdTail(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "keylog:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `keylog — keyboard-usage profiler

  keylog seed                 fabricate a demo session (no hardware needed)
  keylog report [flags]       render the latest session's report
      --session N             report session N (default: latest)
      --all                   aggregate across every session (lifetime view)
      --html PATH             write a self-contained HTML report
      --i3config PATH|demo    parse i3 bindings ("demo" uses sample data)

  keylog start|stop|status|ctx   live capture (requires input group)
  keylog tail                    print live decoded events (verify capture)
`)
}

func dbPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "share", "keylog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "keylog.db"), nil
}

func openStore() (*store.Store, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	return store.Open(path)
}

func cmdSeed(_ []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()

	now := time.Now().Unix()
	host, _ := os.Hostname()
	id, err := s.BeginSession(now-3600, host, "seed")
	if err != nil {
		return err
	}
	if err := s.Flush(id, seed.Counts()); err != nil {
		return err
	}
	if err := s.EndSession(id, now); err != nil {
		return err
	}
	fmt.Printf("seeded session #%d — run: keylog report --i3config demo\n", id)
	return nil
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	sessionID := fs.Int64("session", 0, "session id (0 = latest)")
	all := fs.Bool("all", false, "aggregate across every session (lifetime view)")
	htmlPath := fs.String("html", "", "write HTML report to this path")
	i3conf := fs.String("i3config", "", "i3 config path, or \"demo\"")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()

	var sess model.Session
	var counts model.Counts
	if *all {
		n, earliest, latest, err := s.SessionStats()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no sessions yet — run `keylog seed` or `keylog start`")
		}
		host, _ := os.Hostname()
		sess = model.Session{ID: 0, StartedAt: earliest, EndedAt: latest, Host: host, Note: fmt.Sprintf("%d sessions", n)}
		if counts, err = s.LoadAllCounts(); err != nil {
			return err
		}
	} else {
		id := *sessionID
		if id == 0 {
			if id, err = s.LatestSessionID(); err != nil {
				return err
			}
		}
		if id == 0 {
			return fmt.Errorf("no sessions yet — run `keylog seed` or `keylog start`")
		}
		if sess, err = s.LoadSession(id); err != nil {
			return err
		}
		if counts, err = s.LoadCounts(id); err != nil {
			return err
		}
	}

	cfg := metrics.Config{I3Bindings: loadBindings(*i3conf)}
	m := metrics.Compute(counts, cfg)
	findings := rules.Evaluate(m, rules.DefaultThresholds())

	if *htmlPath != "" {
		f, err := os.Create(*htmlPath)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := report.HTML(f, sess, m, findings); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", *htmlPath)
		return nil
	}
	report.Terminal(os.Stdout, sess, m, findings)
	return nil
}

func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "keylog.sock")
}

func pidPath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(db), "keylog.pid"), nil
}

func writePid(pid int, sessionID int64) error {
	p, err := pidPath()
	if err != nil {
		return err
	}
	return os.WriteFile(p, fmt.Appendf(nil, "%d %d", pid, sessionID), 0o644)
}

func readPid() (pid int, sessionID int64, err error) {
	p, err := pidPath()
	if err != nil {
		return 0, 0, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, 0, err
	}
	_, err = fmt.Sscanf(strings.TrimSpace(string(b)), "%d %d", &pid, &sessionID)
	return pid, sessionID, err
}

func removePid() {
	if p, err := pidPath(); err == nil {
		_ = os.Remove(p)
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	note := fs.String("note", "", "session note")
	dur := fs.Duration("duration", time.Hour, "auto-stop after this long (0 = never)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if pid, _, err := readPid(); err == nil && processAlive(pid) {
		return fmt.Errorf("already capturing (pid %d) — run `keylog stop` first", pid)
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	host, _ := os.Hostname()
	sid, err := st.BeginSession(time.Now().Unix(), host, *note)
	if err != nil {
		return err
	}

	src, err := capture.OpenAll()
	if err != nil {
		_ = st.EndSession(sid, time.Now().Unix())
		return err
	}
	defer src.Close()

	sock, err := feeder.Listen(socketPath())
	if err != nil {
		_ = st.EndSession(sid, time.Now().Unix())
		return err
	}
	defer sock.Close()

	ctx := make(chan feeder.ContextMsg, 32)
	go feeder.StartI3(ctx)
	go func() {
		for m := range sock.Messages() {
			ctx <- m
		}
	}()

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; close(stop) }()

	if err := writePid(os.Getpid(), sid); err != nil {
		return err
	}
	defer removePid()

	fmt.Printf("keylog: capturing session #%d — auto-stop in %s (Ctrl-C or `keylog stop` to end)\n", sid, *dur)
	runErr := daemon.Run(st, sid, src, ctx, stop, daemon.Options{FlushInterval: 5 * time.Second, MaxDuration: *dur})
	_ = st.EndSession(sid, time.Now().Unix())
	fmt.Printf("keylog: session #%d ended\n", sid)
	return runErr
}

func cmdStop() error {
	pid, _, err := readPid()
	if err != nil || !processAlive(pid) {
		return fmt.Errorf("no running session")
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

func cmdStatus() error {
	pid, sid, err := readPid()
	if err != nil || !processAlive(pid) {
		fmt.Println("keylog: not running")
		return nil
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	sess, err := st.LoadSession(sid)
	if err != nil {
		return err
	}
	elapsed := time.Since(time.Unix(sess.StartedAt, 0)).Round(time.Second)
	fmt.Printf("keylog: capturing session #%d — %s elapsed (pid %d)\n", sid, elapsed, pid)
	return nil
}

// cmdTail prints live decoded events so you can confirm evdev capture and
// modifier tracking work before trusting a real session. No storage, no session.
func cmdTail(args []string) error {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	secs := fs.Int("seconds", 0, "auto-stop after N seconds (0 = until Ctrl-C)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	src, err := capture.OpenAll()
	if err != nil {
		return err
	}
	defer src.Close()

	// tail is a throwaway debug view: exit hard on signal or timeout rather than
	// relying on the stream to unwind (a blocked evdev read may not wake on Close).
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; src.Close(); os.Exit(0) }()
	if *secs > 0 {
		go func() { time.Sleep(time.Duration(*secs) * time.Second); src.Close(); os.Exit(0) }()
	}

	fmt.Fprintf(os.Stderr, "keylog: tailing decoded keydowns — type test keys; stops in %ds (or Ctrl-C)\n", *secs)
	fmt.Printf("%-22s %-5s %-12s %s\n", "DEVICE", "KC", "CHAR", "MODS")
	for ev := range src.Events() {
		fmt.Printf("%-22s %-5d %-12s %s\n", trunc(ev.Device, 22), ev.Keycode, keys.Char(ev.Keycode), fmtMods(ev.Modmask))
	}
	return nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func fmtMods(mask int) string {
	var m []string
	if mask&model.ModSuper != 0 {
		m = append(m, "Super")
	}
	if mask&model.ModCtrl != 0 {
		m = append(m, "Ctrl")
	}
	if mask&model.ModAlt != 0 {
		m = append(m, "Alt")
	}
	if mask&model.ModShift != 0 {
		m = append(m, "Shift")
	}
	if len(m) == 0 {
		return "-"
	}
	return strings.Join(m, "+")
}

func cmdCtx(args []string) error {
	fs := flag.NewFlagSet("ctx", flag.ContinueOnError)
	filetype := fs.String("filetype", "", "current filetype")
	buffer := fs.String("buffer", "", "current buffer path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// no-op if no daemon is listening
	return feeder.Send(socketPath(), feeder.ContextMsg{Source: "nvim", Filetype: *filetype, Buffer: *buffer})
}

// loadBindings resolves the --i3config flag: "demo" -> sample data, a path ->
// parse it, empty -> try the default location, tolerating absence.
func loadBindings(flagVal string) []metrics.Binding {
	switch {
	case flagVal == "demo":
		return seed.I3Bindings()
	case flagVal != "":
		bs, err := i3cfg.Parse(flagVal)
		if err != nil {
			fmt.Fprintln(os.Stderr, "keylog: i3 config:", err)
			return nil
		}
		return bs
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		bs, err := i3cfg.Parse(filepath.Join(home, ".config", "i3", "config"))
		if err != nil {
			return nil // no config, fine
		}
		return bs
	}
}
