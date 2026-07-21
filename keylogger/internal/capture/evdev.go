//go:build linux

package capture

import (
	"fmt"
	"strings"
	"sync"

	"github.com/chelmertz/dotfiles/keylogger/internal/model"
	evdev "github.com/holoplot/go-evdev"
)

const (
	keyA        = 30
	valRelease  = 0
	valPress    = 1
	valRepeat   = 2
	maxKeyboard = 256 // codes >= this are BTN_* (mice etc.), not keyboard keys
)

type evdevSource struct {
	out    chan model.KeyEvent
	devs   []*evdev.InputDevice
	wg     sync.WaitGroup
	closed sync.Once

	mu      sync.Mutex
	modmask int
}

// OpenAll opens every attached keyboard (a device advertising KEY_A) read-only
// and starts streaming keydowns. Devices present at call time are snapshotted;
// hotplug mid-session is not handled (see DESIGN.md).
func OpenAll() (Source, error) {
	paths, err := evdev.ListDevicePaths()
	if err != nil {
		return nil, fmt.Errorf("listing input devices: %w", err)
	}
	s := &evdevSource{out: make(chan model.KeyEvent, 256)}
	for _, p := range paths {
		dev, err := evdev.Open(p.Path)
		if err != nil {
			continue // permission or race; skip this device
		}
		if !isKeyboard(dev) {
			dev.Close()
			continue
		}
		s.devs = append(s.devs, dev)
		s.wg.Add(1)
		go s.read(dev, deviceName(dev, p.Name))
	}
	if len(s.devs) == 0 {
		return nil, fmt.Errorf("no readable keyboards found (is your user in the `input` group?)")
	}
	go func() { s.wg.Wait(); close(s.out) }()
	return s, nil
}

func isKeyboard(dev *evdev.InputDevice) bool {
	for _, c := range dev.CapableEvents(evdev.EV_KEY) {
		if int(c) == keyA {
			return true
		}
	}
	return false
}

func deviceName(dev *evdev.InputDevice, fallback string) string {
	if n, err := dev.Name(); err == nil && n != "" {
		return sanitize(n)
	}
	return sanitize(fallback)
}

func sanitize(s string) string { return strings.TrimSpace(s) }

func (s *evdevSource) read(dev *evdev.InputDevice, name string) {
	defer s.wg.Done()
	for {
		ev, err := dev.ReadOne()
		if err != nil {
			return // device gone / revoked
		}
		if ev.Type != evdev.EV_KEY {
			continue
		}
		code := int(ev.Code)
		if code <= 0 || code >= maxKeyboard {
			continue
		}
		switch ev.Value {
		case valPress:
			s.mu.Lock()
			mask := s.modmask
			if bit := modBit(code); bit != 0 {
				s.modmask |= bit // a modifier press shows the mods held *before* it
			}
			s.mu.Unlock()
			s.out <- model.KeyEvent{
				Keycode: code, Modmask: mask, Device: name,
				TsMs: int64(ev.Time.Sec)*1000 + int64(ev.Time.Usec)/1000,
			}
		case valRelease:
			if bit := modBit(code); bit != 0 {
				s.mu.Lock()
				s.modmask &^= bit
				s.mu.Unlock()
			}
		case valRepeat:
			// held key: ignore so it doesn't skew counts
		}
	}
}

func (s *evdevSource) Events() <-chan model.KeyEvent { return s.out }

// Close stops capture by closing the devices, which unblocks each read loop and
// eventually closes the event channel. Idempotent (tail closes on both signal
// and defer).
func (s *evdevSource) Close() {
	s.closed.Do(func() {
		for _, d := range s.devs {
			d.Close()
		}
	})
}
