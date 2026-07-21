// Package capture reads keydowns from keyboards. The daemon depends only on the
// Source interface, so it can be driven by real evdev devices or, in tests, by a
// scripted stream — which is how the whole pipeline is exercised without the
// input-group permission that real capture needs.
package capture

import "github.com/chelmertz/dotfiles/keylogger/internal/model"

// Source yields cleaned keydowns until its channel closes.
type Source interface {
	Events() <-chan model.KeyEvent
	Close()
}

// FakeSource replays a fixed slice of events, then closes. For tests.
type FakeSource struct{ ch chan model.KeyEvent }

func NewFake(evs []model.KeyEvent) *FakeSource {
	ch := make(chan model.KeyEvent, len(evs))
	for _, e := range evs {
		ch <- e
	}
	close(ch)
	return &FakeSource{ch: ch}
}

func (f *FakeSource) Events() <-chan model.KeyEvent { return f.ch }
func (f *FakeSource) Close()                        {}

// modBit maps a modifier keycode to its bitmask bit (0 if not a modifier).
// AltGr (right alt) folds into the Alt bit, which is what makes Swedish coding
// symbols detectable downstream.
func modBit(keycode int) int {
	switch keycode {
	case 29, 97:
		return model.ModCtrl
	case 42, 54:
		return model.ModShift
	case 56, 100:
		return model.ModAlt
	case 125, 126:
		return model.ModSuper
	}
	return 0
}
