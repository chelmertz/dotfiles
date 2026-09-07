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
type hintError struct{ msg, hint string }

func (e *hintError) Error() string { return e.msg + ". " + e.hint }

func usage() error {
	return errors.New("usage: p-launcher list | open <namespace/name> | menu")
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
	switch cmd {
	case "list", "menu":
		if len(args) != 1 {
			return usage()
		}
	case "open":
		if len(args) != 2 {
			return usage()
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

	s, err := OpenStore(dbPath)
	if err != nil {
		return &hintError{msg: err.Error(), hint: "move " + dbPath + " aside and rerun; only selection history is lost"}
	}
	defer s.Close()

	switch cmd {
	case "list":
		return list(s, root, os.Stdout)
	case "open":
		return Open(s, root, args[1])
	case "menu":
		return menu(s, root)
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

// notify raises a desktop notification with body. dunst renders the body as
// Pango markup, so it is HTML-escaped first. A notify-send failure is
// logged next to the original error, never instead of it.
func notify(body string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, nerr := runCmd(ctx, "notify-send", "-u", "critical", "-a", "p-launcher", "p-launcher failed", html.EscapeString(body)); nerr != nil {
		fmt.Fprintln(os.Stderr, "p-launcher: notify-send also failed:", nerr)
	}
}
