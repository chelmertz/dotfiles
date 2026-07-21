package store

import (
	"path/filepath"
	"testing"

	"github.com/chelmertz/dotfiles/keylogger/internal/model"
)

func TestSessionAndFlushRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.BeginSession(1000, "host", "note")
	if err != nil {
		t.Fatal(err)
	}

	ctx := model.Context{Device: "kbd", App: "nvim", Filetype: "go"}
	counts := model.Counts{
		Unigrams: []model.Unigram{
			{Context: ctx, Keycode: 30, Modmask: 0, Count: 5},
			{Context: ctx, Keycode: 28, Modmask: model.ModSuper, Count: 3},
		},
		Bigrams: []model.Bigram{
			{Context: ctx, KC1: 30, KC2: 31, Count: 4, IntervalSumMs: 400},
		},
		Skipgrams: []model.Skipgram{
			{Context: ctx, KC1: 30, KC2: 32, Count: 2},
		},
	}
	if err := s.Flush(id, counts); err != nil {
		t.Fatal(err)
	}

	// re-flush with higher cumulative counts: upsert must overwrite, not double
	counts.Unigrams[0].Count = 9
	if err := s.Flush(id, counts); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadCounts(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Unigrams) != 2 {
		t.Fatalf("unigrams = %d, want 2 (upsert should not duplicate)", len(got.Unigrams))
	}
	var aCount int64
	for _, u := range got.Unigrams {
		if u.Keycode == 30 {
			aCount = u.Count
		}
	}
	if aCount != 9 {
		t.Errorf("a count = %d, want 9 (overwrite semantics)", aCount)
	}
	if len(got.Bigrams) != 1 || got.Bigrams[0].IntervalSumMs != 400 {
		t.Errorf("bigrams = %+v, want 1 with interval 400", got.Bigrams)
	}
	if len(got.Skipgrams) != 1 {
		t.Errorf("skipgrams = %d, want 1", len(got.Skipgrams))
	}
}

func TestLatestSessionID(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, _ := s.LatestSessionID(); got != 0 {
		t.Errorf("empty latest = %d, want 0", got)
	}
	_, _ = s.BeginSession(1, "", "")
	id2, _ := s.BeginSession(2, "", "")
	if got, _ := s.LatestSessionID(); got != id2 {
		t.Errorf("latest = %d, want %d", got, id2)
	}
}
