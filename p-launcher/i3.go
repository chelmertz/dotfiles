package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const tagPrefix = "p:"

// tagFor is the WM_CLASS instance a project's ghostty is launched with.
func tagFor(path string) string { return tagPrefix + path }

// Win is an X11 window in the i3 tree, identified by its container id.
type Win struct {
	ConID   int64
	Focused bool
}

type i3Node struct {
	ID               int64  `json:"id"`
	Focused          bool   `json:"focused"`
	Window           *int64 `json:"window"`
	WindowProperties *struct {
		Instance string `json:"instance"`
	} `json:"window_properties"`
	Nodes         []i3Node `json:"nodes"`
	FloatingNodes []i3Node `json:"floating_nodes"`
}

func walk(n *i3Node, visit func(*i3Node)) {
	if n.Window != nil && n.WindowProperties != nil {
		visit(n)
	}
	for i := range n.Nodes {
		walk(&n.Nodes[i], visit)
	}
	for i := range n.FloatingNodes {
		walk(&n.FloatingNodes[i], visit)
	}
}

func parseTree(treeJSON []byte) (*i3Node, error) {
	var root i3Node
	if err := json.Unmarshal(treeJSON, &root); err != nil {
		return nil, fmt.Errorf("parse i3 tree: %w", err)
	}
	return &root, nil
}

// FindTagged returns the windows whose WM_CLASS instance equals tag, in tree
// order (stable between calls for an unchanged layout).
func FindTagged(treeJSON []byte, tag string) ([]Win, error) {
	root, err := parseTree(treeJSON)
	if err != nil {
		return nil, err
	}
	var out []Win
	walk(root, func(n *i3Node) {
		if n.WindowProperties.Instance == tag {
			out = append(out, Win{ConID: n.ID, Focused: n.Focused})
		}
	})
	return out, nil
}

// OpenTags returns the set of p: tags that currently have a window.
func OpenTags(treeJSON []byte) (map[string]bool, error) {
	root, err := parseTree(treeJSON)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	walk(root, func(n *i3Node) {
		if strings.HasPrefix(n.WindowProperties.Instance, tagPrefix) {
			out[n.WindowProperties.Instance] = true
		}
	})
	return out, nil
}

// PickFocus chooses which window to focus: the one after the currently
// focused one (wrapping), or the first when none is focused. Same
// next-after-focused/wrap rule as ~/.local/bin/ror.py, but over tree order
// (the order FindTagged walks the i3 tree), not ror.py's window-id sort.
func PickFocus(wins []Win) (int64, bool) {
	if len(wins) == 0 {
		return 0, false
	}
	for i, w := range wins {
		if w.Focused {
			return wins[(i+1)%len(wins)].ConID, true
		}
	}
	return wins[0].ConID, true
}

// --- live i3 ---

const i3Timeout = 5 * time.Second

func getTree() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), i3Timeout)
	defer cancel()
	return runCmd(ctx, "i3-msg", "-t", "get_tree")
}

func focusCon(conID int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), i3Timeout)
	defer cancel()
	// Older i3-msg exits 0 on a failed command and only the JSON reply
	// reports it; i3 4.25.1 exits 2 and writes "ERROR: ..." to stderr, which
	// runCmd already turns into a CmdError above. Check both.
	out, err := runCmd(ctx, "i3-msg", "[con_id="+strconv.FormatInt(conID, 10)+"] focus")
	if err != nil {
		return err
	}
	if err := checkI3Reply(out); err != nil {
		return fmt.Errorf("i3 focus con_id=%d: %w", conID, err)
	}
	return nil
}

// checkI3Reply parses an i3-msg command reply (`[{"success":bool,"error":string}]`)
// and returns an error naming the first failed command, if any.
func checkI3Reply(out []byte) error {
	var res []struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return fmt.Errorf("parse i3-msg reply %q: %w", out, err)
	}
	for _, r := range res {
		if !r.Success {
			return errors.New(r.Error)
		}
	}
	return nil
}
