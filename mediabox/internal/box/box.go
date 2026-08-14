// Package box routes identified files to the media box and moves them there.
//
// Every destination comes from a compiled-in table, so a path is never built
// from anything a file said about itself. Commands are run with an explicit
// argv and never through a shell.
package box

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The two roots on the box. Nothing is ever written outside them.
const (
	MediaRoot = "/data/media"
	ROMRoot   = "/data/games/roms"
)

// routes is the whole of the routing policy. A system that is not in here has
// nowhere to go, which is the intended answer for anything unrecognised.
var routes = map[string]string{
	"movies":    MediaRoot + "/movies",
	"snes":      ROMRoot + "/snes",
	"nes":       ROMRoot + "/nes",
	"gb":        ROMRoot + "/gb",
	"gba":       ROMRoot + "/gba",
	"genesis":   ROMRoot + "/genesis",
	"n64":       ROMRoot + "/n64",
	"dreamcast": ROMRoot + "/dreamcast",
	"ps1":       ROMRoot + "/ps1",
	"psp":       ROMRoot + "/psp",
}

// Dest returns where a system's files belong.
func Dest(system string) (string, error) {
	d, ok := routes[system]
	if !ok {
		return "", fmt.Errorf("no destination for %q", system)
	}
	// Belt and braces: the table is a literal, but a path that escaped its
	// root would be the one bug worth catching late.
	if !strings.HasPrefix(d, MediaRoot+"/") && !strings.HasPrefix(d, ROMRoot+"/") {
		return "", fmt.Errorf("destination %q is outside the allowed roots", d)
	}
	return d, nil
}

// Short renders a destination the way it is shown to the user.
func Short(dest string) string { return strings.TrimPrefix(dest, "/data/") }

// Transport moves files. It is an interface so the tests can run offline.
type Transport interface {
	// Send copies files into dest, creating dest if needed.
	Send(files []string, dest string) error
	// Verify re-checks the same files against dest by checksum and returns
	// those whose contents do not match.
	Verify(files []string, dest string) ([]string, error)
}

// Rsync moves files with rsync over ssh.
//
// The wire is left to openssh and rsync on purpose. They already read
// ~/.ssh/config, use the agent, and verify host keys against known_hosts;
// reimplementing that in-process would mean owning host-key verification,
// which is the last thing worth hand-rolling here. rsync also resumes an
// interrupted multi-gigabyte transfer, which matters for films.
//
// What Go takes over is argv construction. The old shell version built a
// remote command by string concatenation and passed bare paths, so a file
// named "foo:bar.mkv" was read by rsync as a host called "foo". Sources are
// resolved to absolute paths here, which removes that ambiguity entirely.
type Rsync struct {
	User    string
	Host    string
	Verbose bool
}

func (r Rsync) remote(dest string) string {
	return fmt.Sprintf("%s@%s:%s/", r.User, r.Host, dest)
}

// absAll resolves sources to absolute paths. Besides being tidy, this is what
// makes a colon in a file name harmless: rsync only reads "host:path" when the
// colon appears before the first slash.
func absAll(files []string) ([]string, error) {
	out := make([]string, 0, len(files))
	for _, f := range files {
		a, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (r Rsync) Send(files []string, dest string) error {
	abs, err := absAll(files)
	if err != nil {
		return err
	}
	args := []string{"-a", "--mkpath", "--human-readable"}
	if r.Verbose {
		args = append(args, "--info=progress2")
	}
	args = append(args, "--")
	args = append(args, abs...)
	args = append(args, r.remote(dest))

	cmd := exec.Command("rsync", args...)
	cmd.Stdout = os.Stderr // progress belongs on stderr; stdout stays pipeable
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Verify asks rsync what it would still need to send if it ran again with
// checksums enabled. Nothing itemised means the remote content matches, and it
// costs no second read of the files over the network.
func (r Rsync) Verify(files []string, dest string) ([]string, error) {
	abs, err := absAll(files)
	if err != nil {
		return nil, err
	}
	args := []string{"-a", "--checksum", "--dry-run", "--itemize-changes"}
	args = append(args, "--")
	args = append(args, abs...)
	args = append(args, r.remote(dest))

	cmd := exec.Command("rsync", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rsync verify: %w", err)
	}
	return parseItemized(string(out)), nil
}

// parseItemized reads rsync's --itemize-changes output. The flags are YXcstpo…
// where Y is the update type and X the file type; a regular file that would
// still be transferred shows Y in "<>c" and X == 'f'. Files that already match
// are not listed at all.
func parseItemized(out string) []string {
	var diff []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 12 {
			continue
		}
		flags, name := line[:11], strings.TrimSpace(line[11:])
		if name == "" {
			continue
		}
		if flags[1] != 'f' {
			continue // directories and links, not our payload
		}
		switch flags[0] {
		case '<', '>', 'c':
			diff = append(diff, name)
		}
	}
	return diff
}
