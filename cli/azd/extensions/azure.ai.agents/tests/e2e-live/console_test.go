// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package e2elive

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	expect "github.com/Netflix/go-expect"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// Key sequences sent to the interactive CLI over the pseudo-terminal.
const (
	keyEnter = "\r"
	keyDown  = "\x1b[B"
	keyUp    = "\x1b[A"
	keyCtrlU = "\x15"
	keyDel   = "\x7f"
)

// tailBytes caps the rolling raw-output buffer kept for failure diagnostics
// (the interactive init screen is otherwise not echoed to the test log).
const tailBytes = 16 << 10

// console drives an interactive child process through a pseudo-terminal and
// renders its output with a vt10x virtual terminal so tests can both block on
// expected output (go-expect) and assert on the on-screen text (the role tmux
// capture-pane played in the old driver).
//
// Wiring (mirrors AlecAivazis/survey's posix expect tests):
//
//	child stdio ── ec.Tty() (pts) ─┐
//	                                ├─ go-expect tees child output ─► vt10x screen + tail
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
	tail *ringBuffer
}

// newConsole creates a console with a virtual terminal of the given size.
func newConsole(cols, rows int) (*console, error) {
	extMaster, extSlave, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("open feedback pty: %w", err)
	}

	term := vt10x.New(vt10x.WithWriter(extSlave), vt10x.WithSize(cols, rows))
	tail := newRingBuffer(tailBytes)

	// go-expect tees everything it reads to these writers, so every read driven
	// by expect()/waitForQuiet() simultaneously renders the screen (term) and
	// records the raw bytes (tail) for diagnostics. No WithDefaultTimeout: each
	// read's deadline is supplied per call via expect.WithTimeout.
	ec, err := expect.NewConsole(
		expect.WithStdin(extMaster),
		expect.WithStdout(term, tail),
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

	return &console{term: term, ec: ec, tail: tail}, nil
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

// expect reads child output (teeing it to the screen and the tail buffer) until
// one of opts matches, idle elapses with no new byte, or the child's tty
// closes. It is the event-driven synchronization primitive that replaces the
// old fixed-interval polling: go-expect only renders output to the screen while
// a read is in flight, so every wait routes through here.
//
// Return contract (go-expect's passthrough pipe, see passthrough_pipe.go):
//   - a match               => (buf, nil)
//   - idle of silence       => (buf, err) with os.IsTimeout(err) == true
//   - child exit / pts close => (buf, err) with a non-timeout error
func (c *console) expect(idle time.Duration, opts ...expect.ExpectOpt) (string, error) {
	return c.ec.Expect(append(opts, expect.WithTimeout(idle))...)
}

// waitForQuiet renders pending output to the screen until the UI stops emitting
// for quiet (a survey prompt fully drawn and now blocking on input) or the
// child exits. It returns exited=true once the child's tty has closed.
//
// It passes no matchers, so go-expect can only return on the idle read deadline
// (os.IsTimeout) or on a terminal read error (EOF / pts closed == child gone).
func (c *console) waitForQuiet(quiet time.Duration) (exited bool) {
	_, err := c.expect(quiet)
	return err != nil && !os.IsTimeout(err)
}

// screen returns the current rendered virtual-terminal contents, cleaned of NUL
// padding and trailing whitespace on each line.
func (c *console) screen() string {
	return cleanScreen(c.term.String())
}

// tailString returns the most recent raw child output captured for diagnostics.
func (c *console) tailString() string {
	return c.tail.String()
}

// close tears down the console and all of its pseudo-terminals.
func (c *console) close() {
	_ = c.ec.Close()
}

// ringBuffer is an io.Writer that retains only the last max bytes written, used
// to keep a bounded tail of raw child output for failure diagnostics.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
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
// screen, or "" if none is visible. The last "?" line is the one survey is
// currently blocking on (earlier "?" lines are answered prompts it echoed).
func activePrompt(screen string) string {
	lines := nonEmptyLines(screen)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "?") {
			if hasInitProgressAfterPrompt(lines[i+1:]) {
				return ""
			}
			return strings.ToLower(lines[i])
		}
	}
	return ""
}

// isInitProgressLine reports whether line is progress emitted after a prompt was
// already answered. Survey leaves answered prompts on screen while the extension
// downloads/copies samples; those stale prompts should not be treated as active.
func isInitProgressLine(line string) bool {
	line = strings.ToLower(line)
	return strings.Contains(line, "downloading sample from github") ||
		strings.HasPrefix(line, "agents.md") ||
		strings.HasPrefix(line, "claude.md") ||
		strings.HasPrefix(line, "readme.md") ||
		strings.HasPrefix(line, "azure.yaml") ||
		strings.HasPrefix(line, "src/") ||
		strings.Contains(line, "setting up github connection") ||
		strings.Contains(line, "adopting the sample's azure.yaml") ||
		strings.Contains(line, "initializing an app to run on azure") ||
		strings.Contains(line, "copying template code from local path") ||
		strings.Contains(line, "installing required extensions")
}

func hasInitProgressAfterPrompt(lines []string) bool {
	for _, line := range lines {
		if isInitProgressLine(line) {
			return true
		}
	}
	return false
}

// screenContains reports whether screen contains sub (case-insensitive).
func screenContains(screen, sub string) bool {
	return strings.Contains(strings.ToLower(screen), strings.ToLower(sub))
}
