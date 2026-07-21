package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/keylogger/internal/capture"
	"github.com/chelmertz/dotfiles/keylogger/internal/feeder"
	"github.com/chelmertz/dotfiles/keylogger/internal/model"
	"github.com/chelmertz/dotfiles/keylogger/internal/store"
)

func TestRunDrainsSourceAndFlushes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := st.BeginSession(0, "", "")

	src := capture.NewFake([]model.KeyEvent{
		{Keycode: 30, Device: "kbd", TsMs: 0},  // a
		{Keycode: 31, Device: "kbd", TsMs: 50}, // s -> bigram a,s
	})
	ctx := make(chan feeder.ContextMsg) // stays open, never sends
	stop := make(chan struct{})

	// source closes after 2 events, so Run returns on its own
	if err := Run(st, sid, src, ctx, stop, Options{FlushInterval: time.Hour}); err != nil {
		t.Fatal(err)
	}

	counts, err := st.LoadCounts(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts.Unigrams) != 2 {
		t.Errorf("unigrams = %d, want 2", len(counts.Unigrams))
	}
	if len(counts.Bigrams) != 1 {
		t.Errorf("bigrams = %d, want 1", len(counts.Bigrams))
	}
	// device rode in on the event
	if counts.Unigrams[0].Device != "kbd" {
		t.Errorf("device = %q, want kbd", counts.Unigrams[0].Device)
	}
}

func TestApplyFocusMerges(t *testing.T) {
	f := model.Context{}
	applyFocus(&f, feeder.ContextMsg{Source: "i3", App: "nvim", Workspace: "2"})
	applyFocus(&f, feeder.ContextMsg{Source: "nvim", Filetype: "go"})
	if f.App != "nvim" || f.Workspace != "2" || f.Filetype != "go" {
		t.Errorf("merged focus = %+v, want app=nvim ws=2 ft=go", f)
	}
	// i3 update must not clobber filetype
	applyFocus(&f, feeder.ContextMsg{Source: "i3", App: "firefox"})
	if f.Filetype != "go" {
		t.Errorf("filetype clobbered by i3 update: %+v", f)
	}
}
