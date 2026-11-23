// Package prlock is the proof that a PR's lock is held. A Locked has
// unexported fields, so code outside this package can make only its zero
// value; a real one comes from Take, which takes the lock. Every per-PR
// state write and row send takes a Locked and checks it against the PR's
// key, so a write outside the lock, after it, or under another PR's lock
// fails at runtime instead of racing a click.
package prlock

import (
	"errors"
	"fmt"
)

// Taker takes the lock named name without waiting and returns its release.
type Taker func(name string) (unlock func(), err error)

var (
	// ErrNotTaken is Check on a zero Locked: no lock was taken.
	ErrNotTaken = errors.New("no lock was taken")
	// ErrReleased is Check on a Locked after its Unlock.
	ErrReleased = errors.New("the lock was released")
	// ErrOtherKey is Check on a Locked taken for another key.
	ErrOtherKey = errors.New("the lock is another PR's")
)

// Locked is a lock taken for one key, until Unlock. Copies share it.
type Locked struct{ h *held }

type held struct {
	key      string
	unlock   func()
	released bool
}

// Take takes key's lock through take. take's error is returned as is, so
// a caller tells a lock someone else holds from a failure.
func Take(key string, take Taker) (Locked, error) {
	unlock, err := take(key)
	if err != nil {
		return Locked{}, err
	}
	return Locked{&held{key: key, unlock: unlock}}, nil
}

// Check fails unless l is a lock taken for key and not yet released.
func (l Locked) Check(key string) error {
	switch {
	case l.h == nil:
		return fmt.Errorf("%s: %w", key, ErrNotTaken)
	case l.h.released:
		return fmt.Errorf("%s: %w", key, ErrReleased)
	case l.h.key != key:
		return fmt.Errorf("%s: %w (%s)", key, ErrOtherKey, l.h.key)
	}
	return nil
}

// Unlock releases l; a second call, or one on the zero value, does nothing.
func (l Locked) Unlock() {
	if l.h == nil || l.h.released {
		return
	}
	l.h.released = true
	l.h.unlock()
}
