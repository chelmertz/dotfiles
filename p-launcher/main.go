package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// hintError carries a self-healing instruction alongside the failure.
type hintError struct {
	msg, hint string
	cause     error
}

func (e *hintError) Error() string { return e.msg + ". " + e.hint }
func (e *hintError) Unwrap() error { return e.cause }

func usage() error {
	return errors.New("usage: p-launcher list [--all] | open <ns/name> | create <ns/name> | archive <ns/name> [--reason done|scrapped|deprioritized|elsewhere] | link add <ns/name> <url> | links refresh | menu [--toggle-key KEY] | hook | desktop lock|unlock | report [--demo] [--range 7d|30d|90d] [--theme dark|light] [--out DIR] [--open]")
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("panic: %v", r)
			fmt.Fprintf(os.Stderr, "p-launcher: %s\n%s", msg, debug.Stack())
			notify(msg)
			os.Exit(2)
		}
	}()
	if err := run(os.Args[1:]); err != nil {
		report(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	cmd := args[0]
	// Validate the subcommand and its arity before touching the store, so a
	// typo'd subcommand never opens (and possibly migrates) the DB.
	toggleKey := ""
	var ro reportOpts
	var archiveReason string
	listAll := false
	switch cmd {
	case "list":
		listAll = len(args) == 2 && args[1] == "--all"
		if len(args) > 2 || (len(args) == 2 && !listAll) {
			return usage()
		}
	case "create":
		if len(args) != 2 {
			return usage()
		}
	case "archive":
		switch {
		case len(args) == 2:
			archiveReason = "done"
		case len(args) == 4 && args[2] == "--reason" && args[3] != "":
			archiveReason = args[3]
		default:
			return usage()
		}
	case "link":
		if len(args) != 4 || args[1] != "add" {
			return usage()
		}
	case "links":
		if len(args) != 2 || args[1] != "refresh" {
			return usage()
		}
	case "desktop":
		if len(args) != 2 || (args[1] != "lock" && args[1] != "unlock") {
			return usage()
		}
	case "menu":
		switch {
		case len(args) == 1:
		case len(args) == 3 && args[1] == "--toggle-key" && args[2] != "":
			toggleKey = args[2]
		default:
			return usage()
		}
	case "open":
		if len(args) != 2 {
			return usage()
		}
	case "hook":
		if len(args) != 1 {
			return usage()
		}
	case "report":
		var err error
		if ro, err = parseReportFlags(args[1:], os.Stderr); err != nil {
			return err
		}
	default:
		return usage()
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, "p")
	dbPath := filepath.Join(dataDir(home), "p-launcher", "p.db")
	if cmd == "report" && ro.demo {
		return runReport(ro, nil, filepath.Dir(dbPath), os.Stdout) // no DB needed
	}

	s, err := OpenStore(dbPath)
	if err != nil {
		return &hintError{msg: err.Error(), hint: "move " + dbPath + " aside and rerun; only selection history is lost", cause: err}
	}
	defer s.Close()

	switch cmd {
	case "list":
		return list(s, root, os.Stdout, listAll)
	case "create":
		if _, err := Create(s, root, args[1]); err != nil {
			return err
		}
		return Open(s, root, args[1])
	case "archive":
		cl, err := Archive(s, root, args[1], archiveReason, copyqCopy)
		if err != nil {
			return err
		}
		fmt.Print(cl.Text())
		notifyInfo(cl.Text())
		return nil
	case "link":
		return s.AddLink(args[2], args[3])
	case "links":
		// Remote failures are counted, not fatal: the timer retries.
		res, err := refreshLinks(s, realLinkDeps())
		if err != nil {
			return err
		}
		fmt.Println(res)
		return nil
	case "desktop":
		// Screen lock/unlock from the i3 xss-lock wrapper; the report's away
		// detection reads these (session_id "desktop", no project).
		return s.RecordSessionEvent(SessionEvent{SessionID: desktopSession, Kind: args[1]})
	case "open":
		return Open(s, root, args[1])
	case "menu":
		return menu(s, root, toggleKey, filepath.Join(filepath.Dir(dbPath), "icons"))
	case "report":
		return runReport(ro, s, filepath.Dir(dbPath), os.Stdout)
	case "hook":
		// Claude Code runs this on every hook event with JSON on stdin. It
		// must never slow or fail a session: log to stderr and exit 0. Nothing
		// goes to stdout, which Claude Code would inject into the session.
		if err := hook(s, root, os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "p-launcher hook:", err)
		}
		return nil
	}
	panic("unreachable: cmd validated above")
}

func dataDir(home string) string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return x
	}
	return filepath.Join(home, ".local", "share")
}

// report writes the error to stderr (the journal, via systemd-cat) and
// raises a desktop notification.
func report(err error) {
	fmt.Fprintln(os.Stderr, "p-launcher:", err)
	notify(err.Error())
}

// notify raises a critical desktop notification for a failure, with body.
// dunst renders the body as Pango markup, so it is HTML-escaped first. A
// notify-send failure is logged next to the original error, never instead
// of it.
func notify(body string) {
	notifySend("critical", "p-launcher failed", body)
}

// notifyInfo raises a low-urgency, non-error desktop notification, e.g. a
// typed menu entry that matched no project.
func notifyInfo(body string) {
	notifySend("low", "p-launcher", body)
}

func notifySend(urgency, title, body string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, nerr := runCmd(ctx, "notify-send", "-u", urgency, "-a", "p-launcher", title, html.EscapeString(body)); nerr != nil {
		fmt.Fprintln(os.Stderr, "p-launcher: notify-send also failed:", nerr)
	}
}
