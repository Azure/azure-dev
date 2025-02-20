// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"
)

var (
	specialTextRegex = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

// Printer is a base component for UX components that require a printer for rendering.
type Printer interface {
	internal.Cursor

	Fprintf(format string, a ...any)
	Fprintln(a ...any)

	ClearCanvas()

	CursorPosition() CursorPosition
	SetCursorPosition(position CursorPosition)
	Size() CanvasSize
}

// NewPrinter creates a new Printer instance.
func NewPrinter(writer io.Writer) Printer {
	if writer == nil {
		writer = os.Stdout
	}

	return &printer{
		Cursor: internal.NewCursor(writer),

		writer:         writer,
		currentLine:    "",
		size:           newCanvasSize(),
		cursorPosition: nil,
	}
}

type printer struct {
	internal.Cursor

	writer         io.Writer
	currentLine    string
	size           *CanvasSize
	cursorPosition *CursorPosition
	clearLock      sync.Mutex
	writeLock      sync.Mutex
}

func (p *printer) Size() CanvasSize {
	return *p.size
}

// CursorPosition represents the position of the cursor on the screen.
func (p *printer) CursorPosition() CursorPosition {
	cursorPosition := CursorPosition{
		Row: p.size.Rows,
		Col: p.size.Cols,
	}

	return cursorPosition
}

// MoveCursorToEnd moves the cursor to the bottom-right corner of the screen.
func (p *printer) MoveCursorToEnd() {
	p.SetCursorPosition(CursorPosition{
		Row: p.size.Rows,
		Col: p.size.Cols,
	})
}

// SetCursorPosition sets the position of the cursor on the screen.
func (p *printer) SetCursorPosition(position CursorPosition) {
	// If the cursor is already at the desired position, do nothing
	if p.cursorPosition != nil && *p.cursorPosition == position {
		return
	}

	// If cursorPosition is nil, assume we're already at the bottom-right of the screen
	if p.cursorPosition == nil {
		p.cursorPosition = &CursorPosition{Row: p.size.Rows, Col: p.size.Cols}
	}

	// Calculate the row and column differences
	rowDiff := position.Row - p.cursorPosition.Row

	// Move vertically if needed
	if rowDiff > 0 {
		p.MoveCursorDown(rowDiff)
	} else if rowDiff < 0 {
		p.MoveCursorUp(int(math.Abs(float64(rowDiff))))
	}

	// Move horizontally if needed
	p.MoveCursorToStartOfLine()
	p.MoveCursorRight(position.Col)

	// Update the stored cursor position
	p.cursorPosition = &position
}

// Fprintf writes formatted text to the screen.
func (p *printer) Fprintf(format string, a ...any) {
	p.writeLock.Lock()
	defer p.writeLock.Unlock()

	content := fmt.Sprintf(format, a...)
	lineCount := strings.Count(content, "\n")

	var lastLine string

	if lineCount > 0 {
		lines := strings.Split(content, "\n")
		lastLine = lines[len(lines)-1]
		p.currentLine = lastLine
	} else {
		lastLine = content
		p.currentLine += lastLine
	}

	fmt.Fprint(p.writer, content)

	visibleContent := specialTextRegex.ReplaceAllString(p.currentLine, "")

	p.size.Cols = len(visibleContent)
	p.size.Rows += lineCount
}

// Fprintln writes text to the screen followed by a newline character.
func (p *printer) Fprintln(a ...any) {
	p.Fprintf("%s\n", fmt.Sprint(a...))
}

// ClearCanvas clears the entire canvas.
func (p *printer) ClearCanvas() {
	p.clearLock.Lock()
	defer p.clearLock.Unlock()

	p.currentLine = ""

	// 1. Move cursor to the bottom-right corner of the canvas
	p.MoveCursorToEnd()

	// 2. Clear each row from the bottom to the top
	for row := p.size.Rows; row > 0; row-- {
		p.ClearLine()
		if row > 1 { // Avoid moving up if we're on the top row
			p.MoveCursorUp(1)
		}
	}

	// 3. Reset the canvas size
	p.size = newCanvasSize()

	// 4. Clear cursor position
	p.cursorPosition = nil
}

// ClearLine clears the current line.
func (p *printer) ClearLine() {
	fmt.Fprint(p.writer, "\033[2K\r")
}
