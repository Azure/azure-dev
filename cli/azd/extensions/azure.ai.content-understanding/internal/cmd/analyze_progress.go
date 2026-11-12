// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"azure.ai.content-understanding/internal/analysis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

type analysisProgress struct {
	mu      sync.Mutex
	writer  io.Writer
	width   int
	active  map[int]string
	frame   int
	visible bool
	err     error
	cancel  context.CancelFunc
	done    chan struct{}
	ctx     context.Context
}

func progressTerminal(writer io.Writer, format string) (*os.File, bool) {
	file, ok := writer.(*os.File)
	return file, ok && (format == "default" || format == "markdown") && azdext.IsInteractiveTerminal(file)
}

func newAnalysisProgress(ctx context.Context, writer io.Writer, width int) *analysisProgress {
	ctx, cancel := context.WithCancel(ctx)
	progress := &analysisProgress{
		writer: writer, width: width, active: map[int]string{}, cancel: cancel, done: make(chan struct{}), ctx: ctx,
	}
	go func() {
		defer close(progress.done)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				progress.mu.Lock()
				progress.draw(-1)
				progress.mu.Unlock()
			}
		}
	}()
	return progress
}

func (p *analysisProgress) update(index int, input analysis.Input, active bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if active {
		p.active[index] = analysisTarget(input)
	} else {
		delete(p.active, index)
	}
	if !active {
		index = -1
	}
	p.draw(index)
	return p.err
}

func (p *analysisProgress) draw(index int) {
	if p.err != nil || len(p.active) == 0 && !p.visible {
		return
	}
	line := "\r\x1b[2K"
	if len(p.active) != 0 {
		if index < 0 {
			indices := slices.Sorted(maps.Keys(p.active))
			index = indices[(p.frame/4)%len(indices)]
		}
		target := p.active[index]
		label := fmt.Sprintf("%c Analyzing %s...", "|/-\\"[p.frame%4], target)
		line += actionText("default", ansi.Truncate(label, max(1, p.width-1), "..."))
		p.frame++
	}
	n, err := io.WriteString(p.writer, line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		p.err = fmt.Errorf("write analysis progress: %w", err)
		p.cancel()
		return
	}
	p.visible = len(p.active) != 0
}

func (p *analysisProgress) stop() error {
	p.cancel()
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.active)
	p.draw(-1)
	return p.err
}

func analysisTarget(input analysis.Input) string {
	target := filepath.Base(input.Path)
	if input.URL != "" {
		target, _, _ = strings.Cut(analysis.RedactURL(input.URL), "?")
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, ansi.Strip(target))
}

func executeAnalysis(
	ctx context.Context, cmdWriter io.Writer, format string,
	run func(context.Context, func(int, analysis.Input, bool) error) (analysis.Execution, error),
) (analysis.Execution, error) {
	file, enabled := progressTerminal(cmdWriter, format)
	if !enabled {
		return run(ctx, nil)
	}
	width, _, err := term.GetSize(int(file.Fd())) //nolint:gosec // File descriptor fits in int.
	if err != nil {
		return analysis.Execution{}, fmt.Errorf("read progress terminal size: %w", err)
	}
	progress := newAnalysisProgress(ctx, cmdWriter, width)
	result, runErr := run(progress.ctx, progress.update)
	if err := progress.stop(); err != nil {
		return result, errors.Join(&azdext.LocalError{
			Code: "progress_write_failed", Category: azdext.LocalErrorCategoryInternal,
			Message:    "Analysis progress could not be written to the terminal.",
			Suggestion: "Check the terminal or redirect stdout to disable progress before retrying.",
		}, runErr, err)
	}
	return result, runErr
}
