package ux

import (
	"context"
	"io"
	"os"
	"time"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ux/internal"
	"github.com/fatih/color"
)

type Spinner struct {
	canvas Canvas

	cursor         internal.Cursor
	options        *SpinnerConfig
	running        bool
	animationIndex int
	text           string
	clear          bool
}

type SpinnerConfig struct {
	Animation   []string
	Text        string
	Interval    time.Duration
	ClearOnStop bool
	Writer      io.Writer
}

var DefaultSpinnerConfig SpinnerConfig = SpinnerConfig{
	Animation: []string{"|", "/", "-", "\\"},
	Text:      "Loading...",
	Interval:  250 * time.Millisecond,
	Writer:    os.Stdout,
}

func NewSpinner(options *SpinnerConfig) *Spinner {
	mergedConfig := SpinnerConfig{}
	if err := mergo.Merge(&mergedConfig, options); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedConfig, DefaultSpinnerConfig); err != nil {
		panic(err)
	}

	return &Spinner{
		options: &mergedConfig,
		text:    mergedConfig.Text,
		cursor:  internal.NewCursor(mergedConfig.Writer),
	}
}

func (s *Spinner) WithCanvas(canvas Canvas) Visual {
	s.canvas = canvas
	return s
}

func (s *Spinner) Start(ctx context.Context) error {
	if s.canvas == nil {
		s.canvas = NewCanvas(s).WithWriter(s.options.Writer)
	}

	s.clear = false
	s.running = true
	s.cursor.HideCursor()

	go func(ctx context.Context) {
		for {
			if !s.running {
				return
			}

			s.canvas.Update()
			time.Sleep(s.options.Interval)
		}
	}(ctx)

	return s.canvas.Run()
}

func (s *Spinner) Stop(ctx context.Context) error {
	s.running = false
	s.cursor.ShowCursor()

	if s.options.ClearOnStop {
		s.clear = true
		return s.canvas.Update()
	}

	return nil
}

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

func (s *Spinner) UpdateText(text string) {
	s.text = text
}

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
