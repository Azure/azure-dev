// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fatih/color"
)

// evalProgress prints status lines for each job and keeps a single animated
// spinner line at the bottom to show that polling is still in progress.
type evalProgress struct {
	mu       sync.Mutex
	starts   map[string]time.Time
	start    time.Time
	stop     chan struct{}
	done     chan struct{}
	spinning bool
}

func newEvalProgress(_ ...string) *evalProgress {
	return &evalProgress{
		starts: make(map[string]time.Time),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Start launches the background spinner ticker.
func (p *evalProgress) Start() {
	p.start = time.Now()
	p.spinning = true
	go func() {
		defer close(p.done)
		frameIdx := 0
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-ticker.C:
				p.mu.Lock()
				if p.spinning {
					elapsed := time.Since(p.start).Truncate(time.Second)
					spin := spinFrames[frameIdx%len(spinFrames)]
					frameIdx++
					fmt.Fprintf(os.Stdout, "\r  %s waiting · %s", spin, elapsed)
				}
				p.mu.Unlock()
			}
		}
	}()
}

// Stop halts the spinner and clears its line.
func (p *evalProgress) Stop() {
	select {
	case <-p.stop:
		return
	default:
		close(p.stop)
	}
	<-p.done
	p.mu.Lock()
	if p.spinning {
		fmt.Fprintf(os.Stdout, "\r%-60s\r", "")
		p.spinning = false
	}
	p.mu.Unlock()
}

// clearSpinnerLine clears the current spinner line so a status line can be
// printed cleanly. Must be called with p.mu held.
func (p *evalProgress) clearSpinnerLine() {
	if p.spinning {
		fmt.Fprintf(os.Stdout, "\r%-60s\r", "")
	}
}

func (p *evalProgress) setRunning(label string, detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts[label] = time.Now()
	p.clearSpinnerLine()
	if detail != "" {
		fmt.Printf("  %s  %s  %s\n", color.BlueString("(\u2013) Running"), label, color.HiBlackString("(%s)", detail))
	} else {
		fmt.Printf("  %s  %s\n", color.BlueString("(\u2013) Running"), label)
	}
}

func (p *evalProgress) setDone(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	elapsed := durationText(time.Since(p.starts[label]))
	p.clearSpinnerLine()
	fmt.Printf("  %s  %s  (%s)\n", color.GreenString("(✓) Done"), label, elapsed)
}

// printDetail prints an indented detail line (e.g. a portal link) safely
// without conflicting with the spinner.
func (p *evalProgress) printDetail(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearSpinnerLine()
	fmt.Printf("         · %s\n", text)
}
func (p *evalProgress) setFailed(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	elapsed := durationText(time.Since(p.starts[label]))
	p.clearSpinnerLine()
	fmt.Printf("  %s  %s  (%s)\n", color.RedString("(x) Failed"), label, elapsed)
}

func (p *evalProgress) setTimedOut(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	elapsed := durationText(time.Since(p.starts[label]))
	p.clearSpinnerLine()
	fmt.Printf("  %s  %s  (%s)\n", color.YellowString("(!) Timed out"), label, elapsed)
}

// durationText returns a human-friendly elapsed time string.
func durationText(d time.Duration) string {
	s := int(d.Seconds())
	if s < 1 {
		return "less than a second"
	}
	if s == 1 {
		return "1 second"
	}
	if s < 60 {
		return fmt.Sprintf("%d seconds", s)
	}
	m := s / 60
	rem := s % 60
	if rem == 0 {
		if m == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", m)
	}
	return fmt.Sprintf("%dm %ds", m, rem)
}
