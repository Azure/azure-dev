// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
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
	lastLines   int // how many lines we rendered last time (for ANSI overwrite)
}

func newDeployProgressTracker(
	writer io.Writer, interactive bool, serviceNames []string,
) *deployProgressTracker {
	services := make([]*serviceStatus, len(serviceNames))
	idx := make(map[string]int, len(serviceNames))
	for i, name := range serviceNames {
		services[i] = &serviceStatus{
			name:  name,
			phase: phaseWaiting,
		}
		idx[name] = i
	}
	return &deployProgressTracker{
		services:    services,
		serviceIdx:  idx,
		writer:      writer,
		interactive: interactive,
	}
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

	if svc.startedAt.IsZero() && phase != phaseWaiting {
		svc.startedAt = time.Now()
	}
	if phase == phaseDone || phase == phaseFailed {
		svc.endedAt = time.Now()
	}

	svc.phase = phase
	svc.detail = detail

	if !t.interactive {
		// Non-interactive (CI): one line per event
		elapsed := svc.elapsed()
		line := fmt.Sprintf("  %s: %s", serviceName, phase)
		if detail != "" {
			line += fmt.Sprintf(" (%s)", detail)
		}
		if elapsed > 0 {
			line += fmt.Sprintf(" [%s]", elapsed)
		}
		fmt.Fprintln(t.writer, line)
	}
}

// Render draws the full progress table. In interactive mode, it overwrites
// the previous render. Call periodically (e.g., every 1s) from a ticker goroutine.
func (t *deployProgressTracker) Render() {
	if !t.interactive {
		return // non-interactive mode uses per-event lines
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var buf strings.Builder

	// Move cursor up to overwrite previous render
	if t.lastLines > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dA", t.lastLines))
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

	buf.WriteString("\033[2K") // clear line
	buf.WriteString(header)
	buf.WriteString("\n")
	buf.WriteString("\033[2K")
	buf.WriteString(divider)
	buf.WriteString("\n")

	lines := 2
	for _, svc := range t.services {
		buf.WriteString("\033[2K") // clear line before writing

		icon := phaseIcon(svc.phase)
		elapsed := ""
		if e := svc.elapsed(); e > 0 {
			elapsed = e.String()
		}

		detail := svc.detail
		if len(detail) > maxDetailLen {
			detail = detail[:maxDetailLen-3] + "..."
		}

		line := fmt.Sprintf(
			"  %s %-*s  %-*s  %-*s  %s",
			icon, maxName, svc.name,
			phaseColWidth, svc.phase,
			elapsedColWidth, elapsed, detail,
		)
		buf.WriteString(line)
		buf.WriteString("\n")
		lines++
	}

	t.lastLines = lines
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
	if t.lastLines > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dA", t.lastLines))
		for range t.lastLines {
			buf.WriteString("\033[2K\n")
		}
		buf.WriteString(fmt.Sprintf("\033[%dA", t.lastLines))
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

	t.lastLines = 0
	fmt.Fprint(t.writer, buf.String())
}

// StartTicker begins periodic rendering and returns a stop function that
// cancels the ticker and waits for the goroutine to exit, ensuring no
// concurrent Render is in flight when the caller proceeds to RenderFinal.
func (t *deployProgressTracker) StartTicker(
	ctx context.Context,
) func() {
	if !t.interactive {
		return func() {} // no-op for non-interactive
	}

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

	return func() { cancel(); wg.Wait() }
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
	default:
		return " "
	}
}
