package ux

import (
	"context"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"
	"github.com/fatih/color"
)

// Spinner is a component for displaying a spinner.
type Spinner struct {
	canvas Canvas

	cursor         internal.Cursor
	options        *SpinnerOptions
	running        int32
	animationIndex int
	text           string
	clear          bool
	canvasMutex    sync.Mutex
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
	s.canvasMutex.Lock()
	defer s.canvasMutex.Unlock()

	if canvas != nil {
		s.canvas = canvas
	}

	return s
}

// Start starts the spinner.
func (s *Spinner) Start(ctx context.Context) error {
	s.ensureCanvas()

	s.clear = false
	atomic.StoreInt32(&s.running, 1)
	s.cursor.HideCursor()

	go func(ctx context.Context) {
		for {
			if atomic.LoadInt32(&s.running) == 0 {
				return
			}

			if err := s.update(); err != nil {
				log.Println("Failed to update spinner:", err)
				return
			}

			time.Sleep(s.options.Interval)
		}
	}(ctx)

	return s.run()
}

// Stop stops the spinner.
func (s *Spinner) Stop(ctx context.Context) error {
	s.ensureCanvas()

	atomic.StoreInt32(&s.running, 0)
	s.cursor.ShowCursor()

	if s.options.ClearOnStop {
		s.clear = true
		return s.update()
	}

	return nil
}

// Run runs a task with the spinner.
func (s *Spinner) Run(ctx context.Context, task func(context.Context) error) error {
	s.ensureCanvas()

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

	printer.Fprintf(color.HiMagentaString(s.options.Animation[s.animationIndex]))
	printer.Fprintf(" %s", s.text)

	if s.animationIndex == len(s.options.Animation)-1 {
		s.animationIndex = 0
	} else {
		s.animationIndex++
	}

	return nil
}

func (s *Spinner) ensureCanvas() {
	s.canvasMutex.Lock()
	defer s.canvasMutex.Unlock()

	if s.canvas == nil {
		s.canvas = NewCanvas(s).WithWriter(s.options.Writer)
	}
}

func (s *Spinner) update() error {
	s.canvasMutex.Lock()
	defer s.canvasMutex.Unlock()

	if s.canvas == nil {
		return nil
	}

	return s.canvas.Update()
}

func (s *Spinner) run() error {
	s.canvasMutex.Lock()
	defer s.canvasMutex.Unlock()

	if s.canvas == nil {
		return nil
	}

	return s.canvas.Run()
}
