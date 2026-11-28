package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// StartRequest is what the wrapper sends the starter for one agent run.
type StartRequest struct {
	Run     string            `json:"run"`
	Org     string            `json:"org"`
	Command string            `json:"command"`
	Prompt  string            `json:"prompt"`
	Files   map[string]string `json:"files"` // relative path -> content, staged read-only
}

// StartResponse is the starter's answer once the agent unit has stopped.
type StartResponse struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	UnitResult string `json:"unit_result,omitempty"`
	Result     string `json:"result,omitempty"` // result.json as the agent wrote it, unvalidated
}

const (
	maxPrompt    = 64 << 10
	maxStaged    = 16 << 20
	maxFiles     = 2000
	maxResult    = 4 << 20
	maxRequest   = maxStaged + maxPrompt + 1<<20
	resultName   = "result.json"
	promptName   = "prompt.md"
	stagedSubdir = "in"
)

// stagedPathRe accepts relative paths of lowercase segments; no segment starts
// with a dot, so nothing staged can be a hidden or traversal component.
var stagedPathRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(/[a-z0-9][a-z0-9_.-]*)*\.(md|txt|json)$`)

// UnitRunner starts one agent unit for a run and returns systemd's verdict
// once it has stopped.
type UnitRunner interface {
	Run(run string) (result string, err error)
	Reset(run string)
}

// Starter runs as root. Everything it writes is under directories it creates
// itself, from names it has validated, so no path the user controls is ever
// followed.
type Starter struct {
	RunsDir   string // /var/lib/wiki/runs: <run>/in staged, <run>/prompt.md
	Registry  string // /run/wiki/runs: <run>.json for the proxy
	ResultDir string // /var/lib/private/wiki-agent: <run>/result.json from the agent
	UID       int    // the only uid allowed to ask
	Units     UnitRunner
}

func (s *Starter) validate(req *StartRequest) error {
	switch {
	case !runIDRe.MatchString(req.Run):
		return fmt.Errorf("bad run id %q", req.Run)
	case !Orgs[req.Org]:
		return fmt.Errorf("bad org %q", req.Org)
	case !commandRe.MatchString(req.Command):
		return fmt.Errorf("bad command %q", req.Command)
	case req.Prompt == "" || len(req.Prompt) > maxPrompt:
		return errors.New("prompt empty or too large")
	case len(req.Files) > maxFiles:
		return fmt.Errorf("%d files, at most %d", len(req.Files), maxFiles)
	}
	total := 0
	for p, c := range req.Files {
		if !stagedPathRe.MatchString(p) || strings.Contains(p, "..") {
			return fmt.Errorf("bad staged path %q", p)
		}
		total += len(c)
	}
	if total > maxStaged {
		return fmt.Errorf("staged %d bytes, at most %d", total, maxStaged)
	}
	return nil
}

// Start stages, registers, runs and collects one agent run. The run's
// directories and registration are removed whatever happens.
func (s *Starter) Start(req *StartRequest) StartResponse {
	if err := s.validate(req); err != nil {
		return StartResponse{Error: err.Error()}
	}
	runDir := filepath.Join(s.RunsDir, req.Run)
	// Mkdir, not MkdirAll: a run id is used once, and an existing directory
	// means a reused id or a leftover that must not be trusted.
	if err := os.Mkdir(runDir, 0o755); err != nil {
		return StartResponse{Error: fmt.Sprintf("run directory: %v", err)}
	}
	reg := filepath.Join(s.Registry, req.Run+".json")
	resDir := filepath.Join(s.ResultDir, req.Run)
	defer func() {
		os.Remove(reg)
		os.RemoveAll(runDir)
		os.RemoveAll(resDir)
		s.Units.Reset(req.Run)
	}()
	if err := writeNew(filepath.Join(runDir, promptName), req.Prompt); err != nil {
		return StartResponse{Error: err.Error()}
	}
	in := filepath.Join(runDir, stagedSubdir)
	if err := os.Mkdir(in, 0o755); err != nil {
		return StartResponse{Error: err.Error()}
	}
	for p, c := range req.Files {
		dst := filepath.Join(in, p)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return StartResponse{Error: err.Error()}
		}
		if err := writeNew(dst, c); err != nil {
			return StartResponse{Error: err.Error()}
		}
	}
	rb, _ := json.Marshal(Registration{Run: req.Run, Org: req.Org, Command: req.Command})
	if err := writeNew(reg, string(rb)); err != nil {
		return StartResponse{Error: fmt.Sprintf("registration: %v", err)}
	}
	unitResult, runErr := s.Units.Run(req.Run)
	resp := StartResponse{UnitResult: unitResult}
	res, err := readResult(filepath.Join(resDir, resultName))
	switch {
	case err != nil && runErr != nil:
		resp.Error = fmt.Sprintf("agent unit: %v; no result: %v", runErr, err)
	case err != nil:
		resp.Error = fmt.Sprintf("no result: %v", err)
	default:
		resp.OK = runErr == nil
		resp.Result = res
		if runErr != nil {
			resp.Error = fmt.Sprintf("agent unit: %v", runErr)
		}
	}
	return resp
}

// writeNew creates a file that must not exist yet, never following a link.
func writeNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readResult reads the agent's result without following a link and refuses
// anything but a bounded regular file.
func readResult(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > maxResult {
		return "", fmt.Errorf("%s is not a regular file under %d bytes", resultName, maxResult)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxResult+1))
	if err != nil || len(b) > maxResult {
		return "", fmt.Errorf("reading %s: %v", resultName, err)
	}
	return string(b), nil
}

// Serve answers one request per connection, one connection at a time, from
// the allowed uid only.
func (s *Starter) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		s.handle(c)
	}
}

func (s *Starter) handle(c net.Conn) {
	defer c.Close()
	reply := func(r StartResponse) { json.NewEncoder(c).Encode(r) }
	uid, err := peerUID(c)
	if err != nil || uid != s.UID {
		log.Printf("starter: refused peer uid=%d err=%v", uid, err)
		reply(StartResponse{Error: "not allowed"})
		return
	}
	var req StartRequest
	if err := json.NewDecoder(io.LimitReader(c, maxRequest)).Decode(&req); err != nil {
		reply(StartResponse{Error: "bad request"})
		return
	}
	resp := s.Start(&req)
	log.Printf("starter: run=%s org=%s command=%s unit=%s ok=%v err=%s", req.Run, req.Org, req.Command, resp.UnitResult, resp.OK, resp.Error)
	reply(resp)
}

func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if serr != nil {
		return -1, serr
	}
	return int(cred.Uid), nil
}

// systemdUnits starts wiki-agent@<run>.service, a Type=oneshot unit, so
// `systemctl start` returns only once the agent has exited.
type systemdUnits struct{ Systemctl string }

func (u systemdUnits) unit(run string) string { return "wiki-agent@" + run + ".service" }

func (u systemdUnits) Run(run string) (string, error) {
	err := exec.Command(u.Systemctl, "start", u.unit(run)).Run()
	out, serr := exec.Command(u.Systemctl, "show", "--property=Result", "--value", u.unit(run)).Output()
	res := strings.TrimSpace(string(out))
	if serr != nil {
		res = "unknown"
	}
	if err == nil && res != "success" {
		err = fmt.Errorf("unit result %s", res)
	}
	return res, err
}

func (u systemdUnits) Reset(run string) {
	exec.Command(u.Systemctl, "reset-failed", u.unit(run)).Run()
}
