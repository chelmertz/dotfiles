// Command wiki maintains the agent-written wiki (wiki-design.md in the
// personal obsidian project). The proxy subcommand is the only path from an
// ingest agent's jail to Anthropic: it holds the seat token the agent never
// sees, forwards only message requests, and charges every token to the run's
// org and command.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strconv"
	"time"
)

func main() {
	log.SetFlags(0) // journald stamps lines
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: wiki proxy|starter [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "proxy":
		if err := runProxy(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "starter":
		if err := runStarter(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "wiki: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

// upstream is fixed: the proxy never forwards anywhere else, whatever Host
// the agent names.
const upstream = "https://api.anthropic.com"

func runProxy(args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	registry := fs.String("registry", "/run/wiki/runs", "directory of run registrations written by the starter")
	state := fs.String("state", "/var/lib/wiki-proxy", "directory for budget.json and usage/<run>.json")
	token := fs.String("token", "", "seat token file (default $CREDENTIALS_DIRECTORY/seat-token)")
	day := fs.Int64("day-tokens", 2_000_000, "budgeted tokens (input + cache writes + output) per local day")
	run := fs.Int64("run-tokens", 1_000_000, "budgeted tokens per run")
	connect := fs.String("connect", "160.79.104.10:443", "address dialled for api.anthropic.com; the unit's firewall allows only Anthropic's API range and no DNS")
	listen := fs.String("listen", "", "unix socket path to listen on instead of a systemd-passed socket (tests, manual runs)")
	fs.Parse(args)
	if *token == "" {
		cd := os.Getenv("CREDENTIALS_DIRECTORY")
		if cd == "" {
			return errors.New("proxy: no --token and no CREDENTIALS_DIRECTORY")
		}
		*token = cd + "/seat-token"
	}
	if _, err := readToken(*token); err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	l, err := listener(*listen)
	if err != nil {
		return err
	}
	up, _ := url.Parse(upstream)
	p := &Proxy{
		Upstream:  up,
		Token:     func() (string, error) { return readToken(*token) },
		Registry:  *registry,
		Accounts:  &Accounts{Dir: *state, DayLimit: *day, RunLimit: *run, Now: time.Now},
		PeerGroup: peerCgroup,
		Transport: pinnedTransport(*connect),
	}
	srv := &http.Server{Handler: p, ConnContext: p.ConnContext, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("proxy: serving %s", l.Addr())
	return srv.Serve(l)
}

// listener takes the socket systemd passed (LISTEN_FDS, fd 3), or listens on
// path when one is given.
func listener(path string) (net.Listener, error) {
	if path != "" {
		os.Remove(path)
		return net.Listen("unix", path)
	}
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) || os.Getenv("LISTEN_FDS") != "1" {
		return nil, errors.New("proxy: no socket passed by systemd and no --listen")
	}
	f := os.NewFile(3, "wiki-proxy.socket")
	defer f.Close()
	return net.FileListener(f)
}

// pinnedTransport dials one fixed address whatever the URL names, so the
// proxy needs no DNS and its unit can deny every other destination. TLS still
// verifies the certificate for api.anthropic.com, the URL's host.
func pinnedTransport(addr string) http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	d := &net.Dialer{Timeout: 10 * time.Second}
	t.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	}
	return t
}

func runStarter(args []string) error {
	fs := flag.NewFlagSet("starter", flag.ExitOnError)
	runs := fs.String("runs", "/var/lib/wiki/runs", "root-owned directory for staged run inputs")
	registry := fs.String("registry", "/run/wiki/runs", "where the proxy reads run registrations")
	results := fs.String("results", "/var/lib/private/wiki-agent", "the agent units' state directories")
	userName := fs.String("user", "", "the only user allowed to start runs")
	systemctl := fs.String("systemctl", "systemctl", "systemctl binary")
	listen := fs.String("listen", "", "unix socket path instead of a systemd-passed socket")
	fs.Parse(args)
	if os.Geteuid() != 0 {
		return errors.New("starter: must run as root")
	}
	u, err := user.Lookup(*userName)
	if err != nil {
		return fmt.Errorf("starter: --user: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid == 0 {
		return fmt.Errorf("starter: user %q has uid %q", *userName, u.Uid)
	}
	l, err := listener(*listen)
	if err != nil {
		return err
	}
	s := &Starter{RunsDir: *runs, Registry: *registry, ResultDir: *results, UID: uid, Units: systemdUnits{*systemctl}}
	return s.Serve(l)
}
