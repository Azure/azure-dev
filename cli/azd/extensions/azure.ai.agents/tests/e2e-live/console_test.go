// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package e2elive

import (
	"fmt"
	"os"
	"strings"

	expect "github.com/Netflix/go-expect"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// Key sequences sent to the interactive CLI over the pseudo-terminal.
const (
	keyEnter = "\r"
	keyDown  = "\x1b[B"
)

// console drives an interactive child process through a pseudo-terminal and
// renders its output with a vt10x virtual terminal so tests can assert on the
// on-screen text (the same role tmux capture-pane played in the old driver).
//
// Wiring (mirrors AlecAivazis/survey's posix expect tests):
//
//	child stdio ── ec.Tty() (pts) ─┐
//	                                ├─ go-expect tees child output ─► vt10x screen
//	vt10x query replies ─► extSlave ┘             ▲
//	                       extMaster ─ go-expect feeds back to child stdin
//
// go-expect creates its own internal pty for the child (ec.Tty()). The external
// pty pair (extMaster/extSlave) exists solely so vt10x can answer terminal
// queries (e.g. cursor-position reports) back to the child; it is closed via
// WithCloser when the console is closed.
type console struct {
	term vt10x.Terminal
	ec   *expect.Console
}

// newConsole creates a console with a virtual terminal of the given size.
func newConsole(cols, rows int) (*console, error) {
	extMaster, extSlave, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("open feedback pty: %w", err)
	}

	term := vt10x.New(vt10x.WithWriter(extSlave), vt10x.WithSize(cols, rows))

	// Deliberately no WithDefaultTimeout: the drain goroutine runs ExpectEOF for
	// the whole child lifetime, and a read timeout would stop it (ending screen
	// updates) during the long quiet stretches of init (e.g. template download).
	ec, err := expect.NewConsole(
		expect.WithStdin(extMaster),
		expect.WithStdout(term),
		expect.WithCloser(extMaster, extSlave),
	)
	if err != nil {
		_ = extMaster.Close()
		_ = extSlave.Close()
		return nil, fmt.Errorf("create expect console: %w", err)
	}

	// Match the child tty size to the virtual terminal so line wrapping in the
	// rendered screen matches what the CLI actually drew.
	//nolint:gosec // cols/rows are small fixed test dimensions; no overflow.
	_ = pty.Setsize(ec.Tty(), &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})

	return &console{term: term, ec: ec}, nil
}

// tty returns the slave pseudo-terminal the child process should attach its
// stdin/stdout/stderr to.
func (c *console) tty() *os.File {
	return c.ec.Tty()
}

// send writes raw bytes (keystrokes) to the child's tty.
func (c *console) send(s string) {
	_, _ = c.ec.Send(s)
}

// drain continuously renders child output to the virtual terminal until the
// child's tty closes (process exit). It MUST run for the whole child lifetime:
// go-expect only tees output to the screen while a read is in flight, so
// without this the screen would stay blank and the child would eventually block
// once the output pipe filled.
func (c *console) drain() {
	_, _ = c.ec.ExpectEOF()
}

// screen returns the current rendered virtual-terminal contents, cleaned of NUL
// padding and trailing whitespace on each line.
func (c *console) screen() string {
	return cleanScreen(c.term.String())
}

// close tears down the console and all of its pseudo-terminals.
func (c *console) close() {
	_ = c.ec.Close()
}

// cleanScreen normalizes a vt10x screen dump: empty cells render as NUL, which
// is replaced with spaces, then trailing whitespace is trimmed from each row.
func cleanScreen(s string) string {
	s = strings.ReplaceAll(s, "\x00", " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}

// nonEmptyLines returns the screen's non-blank lines, trimmed.
func nonEmptyLines(screen string) []string {
	var out []string
	for l := range strings.SplitSeq(screen, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// activePrompt returns the lowercased text of the last survey "?" prompt line on
// screen, or "" if none is visible.
func activePrompt(screen string) string {
	lines := nonEmptyLines(screen)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "?") {
			return strings.ToLower(lines[i])
		}
	}
	return ""
}

// screenContains reports whether screen contains sub (case-insensitive).
func screenContains(screen, sub string) bool {
	return strings.Contains(strings.ToLower(screen), strings.ToLower(sub))
}
