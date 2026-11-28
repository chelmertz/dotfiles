package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Registration is what the starter writes before it starts an agent unit:
// which org and command the run's tokens belong to. One file per run in the
// registry directory, named <run>.json, written by root.
type Registration struct {
	Run     string `json:"run"`
	Org     string `json:"org"`
	Command string `json:"command"`
}

var (
	runIDRe   = regexp.MustCompile(`^[0-9a-z]{1,64}$`)
	commandRe = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
	// The cgroup systemd gives an instance of the wiki-agent@.service template.
	// Run ids are [0-9a-z] so the instance name needs no escaping.
	agentCgroupRe = regexp.MustCompile(`^/system\.slice/system-wiki\\x2dagent\.slice/wiki-agent@([0-9a-z]{1,64})\.service$`)
)

// Orgs are the values a run may be registered under: one per employer, plus personal.
var Orgs = map[string]bool{"personal": true, "eversports": true}

// runFromCgroup returns the run id of an agent unit's cgroup path, or "".
func runFromCgroup(cg string) string {
	m := agentCgroupRe.FindStringSubmatch(cg)
	if m == nil {
		return ""
	}
	return m[1]
}

// lookupRun reads and validates the registration for a run id.
func lookupRun(dir, run string) (Registration, error) {
	var rr Registration
	if !runIDRe.MatchString(run) {
		return rr, fmt.Errorf("bad run id %q", run)
	}
	b, err := os.ReadFile(filepath.Join(dir, run+".json"))
	if err != nil {
		return rr, err
	}
	if err := json.Unmarshal(b, &rr); err != nil {
		return rr, err
	}
	if rr.Run != run || !Orgs[rr.Org] || !commandRe.MatchString(rr.Command) {
		return rr, fmt.Errorf("registration for %s is malformed", run)
	}
	return rr, nil
}

// soPeerPidfd is SO_PEERPIDFD from asm-generic/socket.h (Linux 6.5+); the Go
// standard library has no name for it.
const soPeerPidfd = 77

// peerCgroup returns the cgroup v2 path of the process at the other end of a
// unix socket. It takes a pidfd for the peer, reads the pid and cgroup, then
// checks the pidfd still refers to a live process, so a pid reused between
// the two reads cannot be mistaken for the peer.
func peerCgroup(c net.Conn) (string, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return "", errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return "", err
	}
	var pidfd int
	var serr error
	if err := raw.Control(func(fd uintptr) {
		pidfd, serr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, soPeerPidfd)
	}); err != nil {
		return "", err
	}
	if serr != nil {
		return "", fmt.Errorf("SO_PEERPIDFD: %w", serr)
	}
	defer syscall.Close(pidfd)
	pid, err := pidOfPidfd(pidfd)
	if err != nil {
		return "", err
	}
	cg, err := readCgroup(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", err
	}
	// pidfd_send_signal(pidfd, 0, NULL, 0): ESRCH once the process is gone.
	if _, _, e := syscall.Syscall6(424, uintptr(pidfd), 0, 0, 0, 0, 0); e != 0 {
		return "", fmt.Errorf("peer exited: %w", e)
	}
	return cg, nil
}

func pidOfPidfd(pidfd int) (int, error) {
	f, err := os.Open(fmt.Sprintf("/proc/self/fdinfo/%d", pidfd))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return parsePidfdInfo(f)
}

func parsePidfdInfo(f interface{ Read([]byte) (int, error) }) (int, error) {
	s := bufio.NewScanner(f)
	for s.Scan() {
		if v, ok := strings.CutPrefix(s.Text(), "Pid:"); ok {
			pid, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || pid <= 0 {
				return 0, fmt.Errorf("pidfd refers to no live process (%q)", v)
			}
			return pid, nil
		}
	}
	return 0, errors.New("no Pid line in pidfd fdinfo")
}

// readCgroup returns the unified-hierarchy path from a /proc/<pid>/cgroup file.
func readCgroup(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			return p, nil
		}
	}
	return "", errors.New("no cgroup v2 entry")
}
