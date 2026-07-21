// Package feeder supplies focus context to the daemon: an in-process i3 listener
// (app/workspace) and a unix socket that external processes (neovim, later zsh)
// write to. Both emit ContextMsg values the daemon merges into the focus it
// stamps on counts.
package feeder

import (
	"encoding/json"
	"net"
	"os"
)

// ContextMsg is one focus update. Source selects which fields the daemon applies.
type ContextMsg struct {
	Source    string `json:"source"` // "i3" | "nvim" | "zsh"
	App       string `json:"app,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Filetype  string `json:"filetype,omitempty"`
	Buffer    string `json:"buffer,omitempty"`
}

// Socket is the daemon-side listener external feeders connect to.
type Socket struct {
	path string
	ln   net.Listener
	msgs chan ContextMsg
}

// Listen creates (replacing any stale socket) the feeder socket and starts
// accepting connections.
func Listen(path string) (*Socket, error) {
	_ = os.Remove(path) // clear a stale socket from a crashed run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	s := &Socket{path: path, ln: ln, msgs: make(chan ContextMsg, 32)}
	go s.accept()
	return s, nil
}

func (s *Socket) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			close(s.msgs)
			return
		}
		go s.handle(conn)
	}
}

func (s *Socket) handle(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	for {
		var m ContextMsg
		if err := dec.Decode(&m); err != nil {
			return
		}
		s.msgs <- m
	}
}

// Messages yields context updates from external feeders.
func (s *Socket) Messages() <-chan ContextMsg { return s.msgs }

func (s *Socket) Close() {
	s.ln.Close()
	_ = os.Remove(s.path)
}

// Send is the client side (used by `keylog ctx`). A missing socket (no session
// running) is not an error — the update is simply dropped.
func Send(path string, m ContextMsg) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil // no daemon listening; no-op
	}
	defer conn.Close()
	return json.NewEncoder(conn).Encode(m)
}
