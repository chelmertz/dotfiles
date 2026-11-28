package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeUnits plays the agent: while "running" it records what was staged and
// registered, then writes result.json the way the agent unit would.
type fakeUnits struct {
	s          *Starter
	write      func(resDir string) error
	err        error
	sawPrompt  string
	sawFile    string
	sawReg     Registration
	resetCalls int
}

func (f *fakeUnits) Run(run string) (string, error) {
	b, _ := os.ReadFile(filepath.Join(f.s.RunsDir, run, promptName))
	f.sawPrompt = string(b)
	b, _ = os.ReadFile(filepath.Join(f.s.RunsDir, run, stagedSubdir, "pages/eversports/queue.md"))
	f.sawFile = string(b)
	f.sawReg, _ = lookupRun(f.s.Registry, run)
	resDir := filepath.Join(f.s.ResultDir, run)
	os.MkdirAll(resDir, 0o755)
	if f.write != nil {
		if err := f.write(resDir); err != nil {
			return "", err
		}
	}
	if f.err != nil {
		return "exit-code", f.err
	}
	return "success", nil
}

func (f *fakeUnits) Reset(string) { f.resetCalls++ }

func newStarter(t *testing.T) (*Starter, *fakeUnits) {
	dir := t.TempDir()
	s := &Starter{RunsDir: filepath.Join(dir, "runs"), Registry: filepath.Join(dir, "reg"), ResultDir: filepath.Join(dir, "res"), UID: os.Getuid()}
	for _, d := range []string{s.RunsDir, s.Registry, s.ResultDir} {
		os.MkdirAll(d, 0o755)
	}
	f := &fakeUnits{s: s, write: func(d string) error {
		return os.WriteFile(filepath.Join(d, resultName), []byte(`{"pages":[]}`), 0o644)
	}}
	s.Units = f
	return s, f
}

func goodStart() *StartRequest {
	return &StartRequest{Run: "r1", Org: "eversports", Command: "ingest", Prompt: "distil in/item.md",
		Files: map[string]string{"item.md": "# Decisions", "pages/eversports/queue.md": "queue facts"}}
}

func TestStartStagesRegistersRunsAndCleansUp(t *testing.T) {
	s, f := newStarter(t)
	resp := s.Start(goodStart())
	if !resp.OK || resp.Result != `{"pages":[]}` || resp.UnitResult != "success" {
		t.Fatalf("resp %+v", resp)
	}
	if f.sawPrompt != "distil in/item.md" || f.sawFile != "queue facts" {
		t.Fatalf("agent saw prompt %q file %q", f.sawPrompt, f.sawFile)
	}
	if f.sawReg != (Registration{Run: "r1", Org: "eversports", Command: "ingest"}) {
		t.Fatalf("registration during run: %+v", f.sawReg)
	}
	for _, p := range []string{filepath.Join(s.RunsDir, "r1"), filepath.Join(s.Registry, "r1.json"), filepath.Join(s.ResultDir, "r1")} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind", p)
		}
	}
	if f.resetCalls != 1 {
		t.Errorf("reset calls %d", f.resetCalls)
	}
}

func TestStartRefusesBadRequests(t *testing.T) {
	for name, edit := range map[string]func(*StartRequest){
		"run id with dash":  func(r *StartRequest) { r.Run = "a-b" },
		"run id traversal":  func(r *StartRequest) { r.Run = "../x" },
		"unknown org":       func(r *StartRequest) { r.Org = "acme" },
		"bad command":       func(r *StartRequest) { r.Command = "Ingest!" },
		"empty prompt":      func(r *StartRequest) { r.Prompt = "" },
		"dotdot path":       func(r *StartRequest) { r.Files["../../etc/x.md"] = "" },
		"hidden segment":    func(r *StartRequest) { r.Files["pages/.claude/x.md"] = "" },
		"absolute path":     func(r *StartRequest) { r.Files["/etc/x.md"] = "" },
		"settings file":     func(r *StartRequest) { r.Files["settings.yaml"] = "" },
		"oversized staging": func(r *StartRequest) { r.Files["big.md"] = strings.Repeat("x", maxStaged+1) },
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStarter(t)
			req := goodStart()
			edit(req)
			if resp := s.Start(req); resp.OK || resp.Error == "" {
				t.Fatalf("accepted: %+v", resp)
			}
			if f.sawPrompt != "" {
				t.Fatal("agent ran")
			}
		})
	}
}

func TestStartRefusesReusedRunID(t *testing.T) {
	s, f := newStarter(t)
	os.Mkdir(filepath.Join(s.RunsDir, "r1"), 0o755)
	if resp := s.Start(goodStart()); resp.OK || f.sawPrompt != "" {
		t.Fatalf("reused run id ran: %+v", resp)
	}
}

func TestStartRefusesLinkedOrHugeResult(t *testing.T) {
	for name, write := range map[string]func(string) error{
		"symlink": func(d string) error { return os.Symlink("/etc/passwd", filepath.Join(d, resultName)) },
		"fifo":    func(d string) error { return mkfifo(filepath.Join(d, resultName)) },
		"huge": func(d string) error {
			return os.WriteFile(filepath.Join(d, resultName), make([]byte, maxResult+1), 0o644)
		},
		"missing": func(string) error { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			s, f := newStarter(t)
			f.write = write
			if resp := s.Start(goodStart()); resp.OK || resp.Result != "" {
				t.Fatalf("result accepted: ok=%v len=%d", resp.OK, len(resp.Result))
			}
		})
	}
}

func TestStartReportsFailedUnitWithItsResult(t *testing.T) {
	s, f := newStarter(t)
	f.err = errors.New("exit status 1")
	resp := s.Start(goodStart())
	if resp.OK || resp.UnitResult != "exit-code" || resp.Result == "" || resp.Error == "" {
		t.Fatalf("resp %+v", resp)
	}
}

func TestStarterServesOnlyItsUID(t *testing.T) {
	for name, uid := range map[string]int{"own uid": os.Getuid(), "other uid": os.Getuid() + 1} {
		t.Run(name, func(t *testing.T) {
			s, _ := newStarter(t)
			s.UID = uid
			sock := filepath.Join(t.TempDir(), "s")
			l, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			go s.Serve(l)
			c, err := net.Dial("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			json.NewEncoder(c).Encode(goodStart())
			var resp StartResponse
			json.NewDecoder(c).Decode(&resp)
			if want := uid == os.Getuid(); resp.OK != want {
				t.Fatalf("ok=%v want %v (%s)", resp.OK, want, resp.Error)
			}
		})
	}
}
