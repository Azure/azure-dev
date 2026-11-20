// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rivo/uniseg"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

// Column widths for the progress table.
const (
	phaseColWidth    = 12
	elapsedColWidth  = 8
	detailColWidth   = 20
	maxDetailLen     = 30
	statusColWidth   = 12
	durationColWidth = 10
)

// deployPhase represents the current lifecycle phase of a service deployment.
type deployPhase string

const (
	phaseWaiting   deployPhase = "Waiting"
	phasePackaging deployPhase = "Packaging"
	phasePublish   deployPhase = "Publishing"
	phaseDeploying deployPhase = "Deploying"
	phaseDone      deployPhase = "Done"
	phaseFailed    deployPhase = "Failed"
	phaseSkipped   deployPhase = "Skipped"
)

// serviceStatus tracks one service's deployment progress.
type serviceStatus struct {
	name      string
	phase     deployPhase
	detail    string
	startedAt time.Time
	endedAt   time.Time
}

func (s *serviceStatus) elapsed() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	end := s.endedAt
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(s.startedAt).Truncate(time.Second)
}

// deployProgressTracker provides a thread-safe, per-service progress view
// for parallel deployments. It renders a table to an io.Writer.
//
// In interactive mode (terminal), it overwrites previous lines using ANSI
// cursor control. In non-interactive mode (CI), it prints one line per event.
type deployProgressTracker struct {
	mu          sync.Mutex
	services    []*serviceStatus
	serviceIdx  map[string]int
	writer      io.Writer
	interactive bool
	// lastRendered holds the lines written by the last dynamic render. Their row count is
	// recomputed at the current width (see renderedRows) to move the cursor back over them.
	lastRendered []string
	// termWidth returns the terminal width in columns, used to account for wrapped rows.
	termWidth func() int
	// paused is set while an interactive prompt owns the terminal; see PauseRendering.
	paused bool
}

// ansiEscapeRe matches ANSI escape sequences (CSI and OSC).
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// sanitizeServiceName strips ANSI escape sequences and non-printable characters
// from a service name to prevent terminal output manipulation.
func sanitizeServiceName(name string) string {
	name = ansiEscapeRe.ReplaceAllString(name, "")
	return strings.Map(func(r rune) rune {
		if r == '\t' || (unicode.IsPrint(r) && !unicode.IsControl(r)) {
			return r
		}
		return -1
	}, name)
}

func newDeployProgressTracker(
	writer io.Writer, interactive bool, serviceNames []string,
) *deployProgressTracker {
	services := make([]*serviceStatus, len(serviceNames))
	idx := make(map[string]int, len(serviceNames))
	for i, name := range serviceNames {
		services[i] = &serviceStatus{
			name:  sanitizeServiceName(name),
			phase: phaseWaiting,
		}
		idx[name] = i
	}
	return &deployProgressTracker{
		services:    services,
		serviceIdx:  idx,
		writer:      writer,
		interactive: interactive,
		termWidth:   ux.ConsoleWidth,
	}
}

// physicalRows returns how many terminal rows a line of text occupies when it is printed
// followed by a newline on a terminal that is width columns wide. Wrapping is counted one
// grapheme at a time because a double-width glyph that doesn't fit in the remaining
// columns wraps early.
func physicalRows(line string, width int) int {
	rows, _ := layoutLine(line, width)
	return rows
}

// layoutLine returns the number of terminal rows a line occupies and the column where its
// last row ends. Explicit line breaks start a new row.
func layoutLine(line string, width int) (rows, col int) {
	if width <= 0 {
		return 1, 0
	}

	line = ansiEscapeRe.ReplaceAllString(line, "")
	rows, state := 1, -1
	for len(line) > 0 {
		var cluster string
		var w int
		cluster, line, w, state = uniseg.FirstGraphemeClusterInString(line, state)
		if cluster == "\n" || cluster == "\r\n" {
			rows++
			col = 0
			continue
		}
		if col+w > width {
			rows++
			col = 0
		}
		col += w
	}
	return rows, col
}

// fitLine makes a line's row count independent of the console's wrap mode. A line that
// ends exactly at the right edge takes one row on terminals that defer wrapping but two
// on consoles that wrap immediately (e.g. some Windows consoles). Appending a space makes
// both wrap, so physicalRows matches what is on screen.
func fitLine(line string, width int) string {
	if _, col := layoutLine(line, width); width > 0 && col == width {
		return line + " "
	}
	return line
}

// renderedRows returns how many terminal rows the last dynamic render occupies at the
// current terminal width. Recomputing at erase time keeps the count correct on terminals
// that reflow text after a resize. Callers must hold t.mu.
func (t *deployProgressTracker) renderedRows() int {
	width := t.termWidth()
	rows := 0
	for _, line := range t.lastRendered {
		rows += physicalRows(line, width)
	}
	return rows
}

// Update sets a service's phase and optional detail message.
func (t *deployProgressTracker) Update(
	serviceName string, phase deployPhase, detail string,
) {
	t.mu.Lock()
	defer t.mu.Unlock()

	i, ok := t.serviceIdx[serviceName]
	if !ok {
		return
	}
	svc := t.services[i]

	// Skipped downstream steps must not erase an earlier failure for this service.
	if svc.phase == phaseFailed && phase == phaseSkipped {
		return
	}

	if svc.startedAt.IsZero() && phase != phaseWaiting && phase != phasePackaging {
		svc.startedAt = time.Now()
	}
	if phase == phaseDone || phase == phaseFailed || phase == phaseSkipped {
		svc.endedAt = time.Now()
	}

	svc.phase = phase
	svc.detail = detail

	if !t.interactive {
		// Non-interactive (CI): one line per event
		elapsed := svc.elapsed()
		line := fmt.Sprintf("  %s: %s", svc.name, phase)
		if detail != "" {
			line += fmt.Sprintf(" (%s)", detail)
		}
		if elapsed > 0 {
			line += fmt.Sprintf(" [%s]", elapsed)
		}
		fmt.Fprintln(t.writer, line)
	}
}

// HasActivity reports whether any service has moved beyond the initial
// "Waiting" phase. Used to suppress the final summary table when no
// deploy-phase work was attempted (e.g., provision failed before deploy
// started).
func (t *deployProgressTracker) HasActivity() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, svc := range t.services {
		if svc.phase != phaseWaiting {
			return true
		}
	}
	return false
}

// Render draws the full progress table. In interactive mode, it overwrites
// the previous render. Call periodically (e.g., every 1s) from a ticker goroutine.
func (t *deployProgressTracker) Render() {
	if !t.interactive {
		return // non-interactive mode uses per-event lines
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.paused {
		return
	}

	var buf strings.Builder

	// Move cursor up to overwrite previous render
	if rows := t.renderedRows(); rows > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dA", rows))
	}

	// Compute column widths
	maxName := 7 // minimum "Service" header
	for _, svc := range t.services {
		if len(svc.name) > maxName {
			maxName = len(svc.name)
		}
	}

	// Header
	header := fmt.Sprintf(
		"  %-*s  %-*s  %-*s  %s",
		maxName, "Service", phaseColWidth, "Phase",
		elapsedColWidth, "Elapsed", "Detail",
	)
	divider := fmt.Sprintf("  %s  %s  %s  %s",
		strings.Repeat("─", maxName),
		strings.Repeat("─", phaseColWidth),
		strings.Repeat("─", elapsedColWidth),
		strings.Repeat("─", detailColWidth))

	width := t.termWidth()
	var rendered []string
	writeLine := func(line string) {
		line = fitLine(line, width)
		buf.WriteString("\033[2K") // clear line before writing
		buf.WriteString(line)
		buf.WriteString("\n")
		rendered = append(rendered, line)
	}

	writeLine(header)
	writeLine(divider)

	for _, svc := range t.services {

		icon := phaseIcon(svc.phase)
		elapsed := ""
		if e := svc.elapsed(); e > 0 {
			elapsed = e.String()
		}

		// Keep each service on a single logical line; multiline details would break the
		// row accounting used to redraw the table.
		detail := strings.Join(strings.Fields(svc.detail), " ")
		if len(detail) > maxDetailLen {
			detail = detail[:maxDetailLen-3] + "..."
		}

		line := fmt.Sprintf(
			"  %s %-*s  %-*s  %-*s  %s",
			icon, maxName, svc.name,
			phaseColWidth, svc.phase,
			elapsedColWidth, elapsed, detail,
		)
		writeLine(line)
	}

	t.lastRendered = rendered
	// Erase from cursor to end of screen so wrapped-line remnants
	// from a previous render don't linger below the table.
	buf.WriteString("\033[J")
	fmt.Fprint(t.writer, buf.String())
}

// RenderFinal draws one last table without ANSI cursor control,
// suitable for the final state after all services complete.
func (t *deployProgressTracker) RenderFinal() {
	if !t.interactive {
		return // JSON / non-interactive mode — no table output
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var buf strings.Builder

	// In interactive mode, overwrite the last dynamic render
	if rows := t.renderedRows(); rows > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dA", rows))
		for range rows {
			buf.WriteString("\033[2K\n")
		}
		buf.WriteString(fmt.Sprintf("\033[%dA", rows))
	}

	maxName := 7
	for _, svc := range t.services {
		if len(svc.name) > maxName {
			maxName = len(svc.name)
		}
	}

	buf.WriteString(fmt.Sprintf(
		"\n  %-*s  %-*s  %s\n",
		maxName, "Service", statusColWidth, "Status", "Duration",
	))
	buf.WriteString(fmt.Sprintf("  %s  %s  %s\n",
		strings.Repeat("─", maxName),
		strings.Repeat("─", statusColWidth),
		strings.Repeat("─", durationColWidth)))

	for _, svc := range t.services {
		icon := phaseIcon(svc.phase)
		elapsed := ""
		if e := svc.elapsed(); e > 0 {
			elapsed = e.String()
		}
		buf.WriteString(fmt.Sprintf("  %s %-*s  %-*s  %s\n",
			icon, maxName, svc.name, statusColWidth, svc.phase, elapsed))
	}

	t.lastRendered = nil
	// Erase from cursor to end of screen to clear any stale content
	// left behind by long lines that wrapped in the terminal.
	buf.WriteString("\033[J")
	fmt.Fprint(t.writer, buf.String())
}

// PauseRendering implements [input.TerminalRenderer]. It erases the dynamic table and
// suppresses redraws so an interactive prompt can own the terminal. Because Render holds
// the same lock while writing, no redraw is in flight once this returns.
func (t *deployProgressTracker) PauseRendering() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.paused = true
	if rows := t.renderedRows(); t.interactive && rows > 0 {
		// Move to the top of the table and erase it, leaving the cursor where the prompt
		// should render.
		fmt.Fprintf(t.writer, "\033[%dA\033[J", rows)
		t.lastRendered = nil
	}
}

// ResumeRendering implements [input.TerminalRenderer]. It re-enables redraws and renders
// the table immediately below whatever the prompt left on screen.
func (t *deployProgressTracker) ResumeRendering() {
	t.mu.Lock()
	t.paused = false
	if t.interactive {
		// The interaction may have left the cursor mid-line (e.g. a hook printing "Done"
		// without a trailing newline). Writing a full row of spaces wraps to a new row only
		// in that case; at column 0 the terminal defers the wrap and "\r" returns to the
		// start of the same row. The table then renders without erasing that output.
		fmt.Fprint(t.writer, strings.Repeat(" ", max(t.termWidth(), 1))+"\r")
	}
	t.mu.Unlock()

	t.Render()
}

// StartTicker begins periodic rendering and returns a stop function that
// cancels the ticker and waits for the goroutine to exit, ensuring no
// concurrent Render is in flight when the caller proceeds to RenderFinal.
//
// While the ticker runs, the tracker is registered as an [input.TerminalRenderer]
// so interactive prompts pause it instead of being overwritten by redraws.
func (t *deployProgressTracker) StartTicker(
	ctx context.Context,
) func() {
	if !t.interactive {
		return func() {} // no-op for non-interactive
	}

	unregister := input.RegisterTerminalRenderer(t)

	tickCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-tickCtx.Done():
				return
			case <-ticker.C:
				t.Render()
			}
		}
	})

	// Initial render
	t.Render()

	return sync.OnceFunc(func() {
		cancel()
		wg.Wait()
		unregister()
	})
}

func phaseIcon(phase deployPhase) string {
	switch phase {
	case phaseWaiting:
		return "○"
	case phasePackaging, phasePublish, phaseDeploying:
		return "◐"
	case phaseDone:
		return "●"
	case phaseFailed:
		return "✗"
	case phaseSkipped:
		return "⊘"
	default:
		return " "
	}
}
