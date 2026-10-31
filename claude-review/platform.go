package main

import (
	"fmt"
	"strings"
)

// darwin is runtime.GOOS on macOS.
const darwin = "darwin"

// shellQuote wraps s in single quotes for POSIX sh, escaping embedded ones.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellLine renders "cd <dir> && <argv> && <argv>..." for a POSIX shell:
// each step runs only if the one before it succeeded.
func shellLine(dir string, steps ...[]string) string {
	line := "cd " + shellQuote(dir)
	for _, argv := range steps {
		quoted := make([]string, len(argv))
		for i, a := range argv {
			quoted[i] = shellQuote(a)
		}
		line += " && " + strings.Join(quoted, " ")
	}
	return line
}

// viaInteractiveShell wraps a command line so it runs inside the user's
// interactive shell. A URL handler is started by the desktop with a minimal
// environment where `claude` is usually not on PATH; the interactive shell's
// rc files put it there.
func viaInteractiveShell(line string) []string {
	return []string{"/bin/sh", "-c", `exec "${SHELL:-/bin/sh}" -ic ` + shellQuote(line)}
}

// launcherScript is what a new terminal window runs: the steps in the clone,
// stopping at the first that fails, then an interactive shell so the window
// survives claude exiting and the user can keep working there.
func launcherScript(dir string, steps ...[]string) string {
	inner := shellLine(dir, steps...) + `; exec "${SHELL:-/bin/sh}" -i`
	return "#!/bin/sh\nexec \"${SHELL:-/bin/sh}\" -ic " + shellQuote(inner) + "\n"
}

// defaultTerminalTemplate is the per-OS command that opens a new terminal
// window running {script} in {dir}. Overridable with the "terminal" key.
func defaultTerminalTemplate(goos string) string {
	if goos == darwin {
		return "open -a Terminal {script}"
	}
	return "ghostty --working-directory={dir} -e {script}"
}

// terminalArgv expands a template into an argv. Words are split on
// whitespace with single or double quotes grouping; {dir} and {script} are
// replaced inside any word.
func terminalArgv(template, dir, script string) ([]string, error) {
	words, err := splitWords(template)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("claude-review: empty terminal command")
	}
	r := strings.NewReplacer("{dir}", dir, "{script}", script)
	for i, w := range words {
		words[i] = r.Replace(w)
	}
	return words, nil
}

// splitWords is a small shell-words splitter: whitespace separates, single
// and double quotes group, backslash escapes the next character outside
// single quotes. A trailing backslash is kept literally.
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				i = writeMaybeEscaped(&cur, s, i, quote == '"')
			}
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			i = writeMaybeEscaped(&cur, s, i, true)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("claude-review: unbalanced quote in terminal command %q", s)
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// writeMaybeEscaped writes s[i] to cur, or, when escapes apply and s[i] is a
// backslash with a character after it, writes that character instead. It
// returns the index of the last byte consumed.
func writeMaybeEscaped(cur *strings.Builder, s string, i int, escapes bool) int {
	if escapes && s[i] == '\\' && i+1 < len(s) {
		i++
	}
	cur.WriteByte(s[i])
	return i
}

// notifyArgv is the desktop notification command for goos.
func notifyArgv(goos, title, body string) []string {
	if goos == darwin {
		esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
		return []string{"osascript", "-e", fmt.Sprintf(`display notification "%s" with title "%s"`, esc.Replace(body), esc.Replace(title))}
	}
	return []string{"notify-send", title, body}
}
