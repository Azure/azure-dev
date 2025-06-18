// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"
	"github.com/nathan-fiscaletti/consolesize-go"
)

const SIGWINCH = syscall.Signal(0x1c)

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

	width := ConsoleWidth()

	printer := &printer{
		Cursor: internal.NewCursor(writer),

		writer:         writer,
		currentLine:    "",
		size:           newCanvasSize(),
		cursorPosition: nil,
		consoleWidth:   width,
	}

	printer.listenForResize()

	return printer
}

type printer struct {
	internal.Cursor

	writer         io.Writer
	consoleWidth   int
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

	// Check if the content includes any line breaks
	hasNewLines := strings.Count(content, "\n") > 0

	var lastLine string
	newLines := 0

	if hasNewLines {
		// Find text after the last line break
		// This is used to keep track of the current line being printed
		lastNewLineIndex := strings.LastIndex(content, "\n")
		lastLine = content[lastNewLineIndex+1:]
		newLines = CountLineBreaks(content, p.consoleWidth)
		p.currentLine = lastLine
	} else {
		// Need to see if appending content to the current line will cause wrapping
		// (i.e. if the line is longer than the console width)
		p.currentLine += content
		newLines = CountLineBreaks(p.currentLine, p.consoleWidth)
	}

	fmt.Fprint(p.writer, content)

	p.size.Cols = VisibleLength(p.currentLine)
	p.size.Rows += newLines
}

func (p *printer) listenForResize() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, SIGWINCH) // Listen for resize

	go func() {
		for range sigChan {
			p.writeLock.Lock()
			p.consoleWidth, _ = consolesize.GetConsoleSize()
			p.writeLock.Unlock()
		}
	}()
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
