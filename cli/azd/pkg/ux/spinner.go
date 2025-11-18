// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"context"
	"io"
	"os"
	"time"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/pkg/output"
	"github.com/azure/azure-dev/pkg/ux/internal"
)

// Spinner is a component for displaying a spinner.
type Spinner struct {
	canvas Canvas

	cursor         internal.Cursor
	options        *SpinnerOptions
	animationIndex int
	text           string
	clear          bool
	cancel         context.CancelFunc
}

// SpinnerOptions represents the options for the Spinner component.
type SpinnerOptions struct {
	Animation   []string
	Text        string
	Interval    time.Duration
	ClearOnStop bool
	Writer      io.Writer
}

var DefaultSpinnerOptions SpinnerOptions = SpinnerOptions{
	Animation: []string{"|", "/", "-", "\\"},
	Text:      "Loading...",
	Interval:  250 * time.Millisecond,
	Writer:    os.Stdout,
}

// NewSpinner creates a new Spinner instance.
func NewSpinner(options *SpinnerOptions) *Spinner {
	mergedConfig := SpinnerOptions{}
	if err := mergo.Merge(&mergedConfig, options); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedConfig, DefaultSpinnerOptions); err != nil {
		panic(err)
	}

	return &Spinner{
		options: &mergedConfig,
		text:    mergedConfig.Text,
		cursor:  internal.NewCursor(mergedConfig.Writer),
	}
}

// WithCanvas sets the canvas for the spinner.
func (s *Spinner) WithCanvas(canvas Canvas) Visual {
	if canvas != nil {
		s.canvas = canvas
	}

	return s
}

// Start starts the spinner.
func (s *Spinner) Start(ctx context.Context) error {
	if s.canvas == nil {
		s.canvas = NewCanvas(s).WithWriter(s.options.Writer)
	}

	// Use a context to determine when to stop the spinner
	cancelCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.clear = false
	s.cursor.HideCursor()

	if err := s.canvas.Run(); err != nil {
		return err
	}

	// Periodic update goroutine
	go func(ctx context.Context) {
		for {
			select {
			// Context is stopped, exit
			case <-ctx.Done():
				return
			// Update the spinner on each tick interval
			case <-time.After(s.options.Interval):
				_ = s.canvas.Update()
			}
		}
	}(cancelCtx)

	return nil
}

// Stop stops the spinner.
func (s *Spinner) Stop(ctx context.Context) error {
	defer func() {
		s.cursor.ShowCursor()
	}()

	if s.cancel == nil {
		return nil
	}

	if s.options.ClearOnStop {
		s.clear = true
	}

	s.cancel()
	s.cancel = nil

	if err := s.canvas.Update(); err != nil {
		return err
	}

	return nil
}

// Run runs a task with the spinner.
func (s *Spinner) Run(ctx context.Context, task func(context.Context) error) error {
	s.options.ClearOnStop = true

	if err := s.Start(ctx); err != nil {
		return err
	}

	defer func() {
		_ = s.Stop(ctx)
	}()

	return task(ctx)
}

// UpdateText updates the text of the spinner.
func (s *Spinner) UpdateText(text string) {
	s.text = text
}

// Render renders the spinner.
func (s *Spinner) Render(printer Printer) error {
	if s.clear {
		return nil
	}

	printer.Fprintf("%s %s", output.WithHintFormat(s.options.Animation[s.animationIndex]), s.text)

	if s.animationIndex == len(s.options.Animation)-1 {
		s.animationIndex = 0
	} else {
		s.animationIndex++
	}

	return nil
}
