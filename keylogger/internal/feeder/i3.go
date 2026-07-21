//go:build linux

package feeder

import (
	i3 "go.i3wm.org/i3/v4"
)

// StartI3 subscribes to i3 window-focus events and emits the focused window's
// class as context. Runs until the process exits (a start/stop daemon tears down
// by exiting, so no explicit shutdown is needed). Workspace is left empty for v1.
func StartI3(out chan<- ContextMsg) {
	recv := i3.Subscribe(i3.WindowEventType)
	defer recv.Close()
	for recv.Next() {
		ev, ok := recv.Event().(*i3.WindowEvent)
		if !ok || ev.Change != "focus" {
			continue
		}
		cls := ev.Container.WindowProperties.Class
		if cls == "" {
			continue
		}
		out <- ContextMsg{Source: "i3", App: cls}
	}
}
