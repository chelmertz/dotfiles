package prlock_test

import (
	"errors"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// fake is a Taker over a set of held names.
type fake map[string]bool

func (f fake) take(name string) (func(), error) {
	if f[name] {
		return nil, errBusy
	}
	f[name] = true
	return func() { delete(f, name) }, nil
}

var errBusy = errors.New("busy")

func TestCheckPassesForItsOwnKey(t *testing.T) {
	l, err := prlock.Take("a", fake{}.take)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if err := l.Check("a"); err != nil {
		t.Errorf("own key: %v", err)
	}
}

func TestCheckFailsOnTheZeroValue(t *testing.T) {
	if err := (prlock.Locked{}).Check("a"); !errors.Is(err, prlock.ErrNotTaken) {
		t.Errorf("zero Locked: want ErrNotTaken, got %v", err)
	}
}

func TestCheckFailsAfterUnlock(t *testing.T) {
	f := fake{}
	l, err := prlock.Take("a", f.take)
	if err != nil {
		t.Fatal(err)
	}
	copied := l
	l.Unlock()
	if f["a"] {
		t.Error("Unlock did not release the lock")
	}
	if err := copied.Check("a"); !errors.Is(err, prlock.ErrReleased) {
		t.Errorf("a copy after Unlock: want ErrReleased, got %v", err)
	}
	l.Unlock() // a second release is a no-op
}

func TestCheckFailsForAnotherKey(t *testing.T) {
	l, err := prlock.Take("a", fake{}.take)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if err := l.Check("b"); !errors.Is(err, prlock.ErrOtherKey) {
		t.Errorf("another key: want ErrOtherKey, got %v", err)
	}
}

func TestTakeReturnsTheTakersError(t *testing.T) {
	f := fake{"a": true}
	l, err := prlock.Take("a", f.take)
	if !errors.Is(err, errBusy) {
		t.Fatalf("a held lock: want the taker's error, got %v", err)
	}
	if err := l.Check("a"); !errors.Is(err, prlock.ErrNotTaken) {
		t.Errorf("a failed Take's value: want ErrNotTaken, got %v", err)
	}
}
