package main

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

// memGuardLimit caps the test binary's address space, so a mutant that
// turns a loop into an endless append dies on its own instead of growing
// until the OOM killer takes the whole mutation run: three such binaries
// under gremlins filled a 12 GB cgroup on 2026-11-25. A kernel limit, not a
// polled one: a polling guard saw 7.6 GB before its first 50 ms tick. The
// suite peaks near 25 MB of heap; the Go runtime reserves address space
// well beyond that, hence the headroom.
const memGuardLimit = 4 << 30

func TestMain(m *testing.M) {
	lim := syscall.Rlimit{Cur: memGuardLimit, Max: memGuardLimit}
	if err := syscall.Setrlimit(syscall.RLIMIT_AS, &lim); err != nil {
		fmt.Fprintln(os.Stderr, "memguard:", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}
