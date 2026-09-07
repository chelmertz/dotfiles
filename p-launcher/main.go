package main

import (
	"context"
	"errors"
	"fmt"
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
			report(fmt.Errorf("panic: %v\n%s", r, debug.Stack()))
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

	switch args[0] {
	case "list":
		return list(s, root, os.Stdout)
	case "open":
		if len(args) != 2 {
			return usage()
		}
		return Open(s, root, args[1])
	case "menu":
		return menu(s, root)
	default:
		return usage()
	}
}

func dataDir(home string) string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return x
	}
	return filepath.Join(home, ".local", "share")
}

// report writes the error to stderr (the journal, via systemd-cat) and raises
// a desktop notification. A notify-send failure is logged next to the
// original error, never instead of it.
func report(err error) {
	fmt.Fprintln(os.Stderr, "p-launcher:", err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, nerr := runCmd(ctx, "notify-send", "-u", "critical", "-a", "p-launcher", "p-launcher failed", err.Error()); nerr != nil {
		fmt.Fprintln(os.Stderr, "p-launcher: notify-send also failed:", nerr)
	}
}
