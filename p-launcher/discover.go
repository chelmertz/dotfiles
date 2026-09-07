package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Found is a project directory on disk: ~/p/<Namespace>/<Name>.
type Found struct {
	Namespace string
	Name      string
	Path      string // "m/dependabot", the project's identity
}

// Discover lists ~/p/<ns>/<name> directories. "archive" and dot-dirs are
// skipped; files at either level are ignored. Symlinked directories are
// followed. Sorted by Path.
func Discover(root string) ([]Found, error) {
	nss, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read project root %s: %w", root, err)
	}
	var out []Found
	for _, ns := range nss {
		if ns.Name() == "archive" || strings.HasPrefix(ns.Name(), ".") || !isDir(filepath.Join(root, ns.Name())) {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, ns.Name()))
		if err != nil {
			return nil, fmt.Errorf("read namespace %s: %w", ns.Name(), err)
		}
		for _, n := range names {
			if strings.HasPrefix(n.Name(), ".") || !isDir(filepath.Join(root, ns.Name(), n.Name())) {
				continue
			}
			out = append(out, Found{Namespace: ns.Name(), Name: n.Name(), Path: ns.Name() + "/" + n.Name()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// isDir reports whether path is a directory, following symlinks.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
