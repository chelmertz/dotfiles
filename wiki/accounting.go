package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Usage is one run's token use as Anthropic reported it, plus what the proxy
// charged against the budget. Budgeted tokens are input + cache writes +
// output; cache reads are recorded but not budgeted.
type Usage struct {
	Run        string         `json:"run"`
	Org        string         `json:"org"`
	Command    string         `json:"command"`
	Model      string         `json:"model,omitempty"`
	Requests   int64          `json:"requests"`
	Input      int64          `json:"input_tokens"`
	CacheWrite int64          `json:"cache_write_tokens"`
	CacheRead  int64          `json:"cache_read_tokens"`
	Output     int64          `json:"output_tokens"`
	Charged    int64          `json:"charged_tokens"`
	Unsettled  int64          `json:"unsettled_requests"`
	Refusals   map[string]int `json:"refusals,omitempty"`
}

type dayBudget struct {
	Day  string `json:"day"`
	Used int64  `json:"used"`
}

// Accounts holds the daily budget and the per-run usage files. Every change is
// written to disk (fsync, then rename) before the request it covers goes
// upstream, so a crash leaves at worst an over-count: the budget fails closed.
type Accounts struct {
	Dir      string // state directory; budget.json and usage/<run>.json
	DayLimit int64
	RunLimit int64
	Now      func() time.Time

	mu sync.Mutex
}

var errState = errors.New("budget state unreadable")

func (a *Accounts) today() string { return a.Now().Format("2006-01-02") }

func (a *Accounts) loadDay() (dayBudget, error) {
	var d dayBudget
	b, err := os.ReadFile(filepath.Join(a.Dir, "budget.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return dayBudget{Day: a.today()}, nil
	}
	if err != nil || json.Unmarshal(b, &d) != nil || d.Day == "" {
		return d, errState
	}
	if d.Day != a.today() {
		return dayBudget{Day: a.today()}, nil
	}
	return d, nil
}

func (a *Accounts) usagePath(run string) string {
	return filepath.Join(a.Dir, "usage", run+".json")
}

func (a *Accounts) loadRun(rr Registration) (Usage, error) {
	u := Usage{Run: rr.Run, Org: rr.Org, Command: rr.Command}
	b, err := os.ReadFile(a.usagePath(rr.Run))
	if errors.Is(err, fs.ErrNotExist) {
		return u, nil
	}
	if err != nil || json.Unmarshal(b, &u) != nil {
		return u, errState
	}
	if u.Org != rr.Org || u.Command != rr.Command {
		return u, fmt.Errorf("run %s re-registered with another org or command", rr.Run)
	}
	return u, nil
}

// Reserve charges n tokens for one request before it is forwarded. It refuses
// when either cap would be exceeded or the state cannot be read.
func (a *Accounts) Reserve(rr Registration, model string, n int64) (string, *refusal) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n <= 0 {
		return "", refuse("budget-run", "reservation %d", n)
	}
	d, err := a.loadDay()
	if err != nil {
		return "", refuse("state", "%v", err)
	}
	u, err := a.loadRun(rr)
	if err != nil {
		return "", refuse("state", "%v", err)
	}
	if d.Used+n > a.DayLimit {
		return "", refuse("budget-day", "used %d + %d > %d", d.Used, n, a.DayLimit)
	}
	if u.Charged+n > a.RunLimit {
		return "", refuse("budget-run", "charged %d + %d > %d", u.Charged, n, a.RunLimit)
	}
	d.Used += n
	u.Charged += n
	u.Requests++
	u.Unsettled++
	u.Model = model
	if err := writeJSON(filepath.Join(a.Dir, "budget.json"), d); err != nil {
		return "", refuse("state", "%v", err)
	}
	if err := writeJSON(a.usagePath(rr.Run), u); err != nil {
		return "", refuse("state", "%v", err)
	}
	return d.Day, nil
}

// Reported is what one response said it used.
type Reported struct {
	Input, CacheWrite, CacheRead, Output int64
}

func (r Reported) budgeted() int64 { return r.Input + r.CacheWrite + r.Output }

// Settle replaces a reservation made on day with the reported use. Without a
// complete report (stream cut, client gone) the reservation stays charged.
func (a *Accounts) Settle(rr Registration, reserved int64, day string, rep *Reported) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rep == nil {
		return nil
	}
	d, err := a.loadDay()
	if err != nil {
		return err
	}
	u, err := a.loadRun(rr)
	if err != nil {
		return err
	}
	delta := rep.budgeted() - reserved
	// A reservation made on an earlier day was charged to that day; neither
	// its refund nor its overrun touches today's budget.
	if day == d.Day {
		d.Used += delta
	}
	u.Charged += delta
	u.Unsettled--
	u.Input += rep.Input
	u.CacheWrite += rep.CacheWrite
	u.CacheRead += rep.CacheRead
	u.Output += rep.Output
	if err := writeJSON(filepath.Join(a.Dir, "budget.json"), d); err != nil {
		return err
	}
	return writeJSON(a.usagePath(rr.Run), u)
}

// Refused counts a refusal against a known run.
func (a *Accounts) Refused(rr Registration, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	u, err := a.loadRun(rr)
	if err != nil {
		return err
	}
	if u.Refusals == nil {
		u.Refusals = map[string]int{}
	}
	u.Refusals[reason]++
	return writeJSON(a.usagePath(rr.Run), u)
}

// writeJSON replaces path atomically: temp file, fsync, rename, fsync dir.
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
