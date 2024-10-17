package ux

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/sdk/azdcore/ux/internal"
)

var (
	specialTextRegex = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

type Printer interface {
	internal.Cursor
	Screen

	Fprintf(format string, a ...any)
	Fprintln(a ...any)

	ClearCanvas()

	CursorPosition() CanvasPosition
	SetCursorPosition(position CanvasPosition)
	Size() CanvasSize
}

func NewPrinter(writer io.Writer) Printer {
	if writer == nil {
		writer = os.Stdout
	}

	return &printer{
		Cursor: internal.NewCursor(writer),
		Screen: NewScreen(writer),

		writer:         writer,
		currentLine:    "",
		size:           &CanvasSize{},
		cursorPosition: nil,
	}
}

type printer struct {
	internal.Cursor
	Screen

	writer         io.Writer
	currentLine    string
	size           *CanvasSize
	cursorPosition *CanvasPosition
	clearLock      sync.Mutex
	writeLock      sync.Mutex
}

func (p *printer) Size() CanvasSize {
	return *p.size
}

func (p *printer) CursorPosition() CanvasPosition {
	return CanvasPosition{
		Row: p.size.Rows,
		Col: p.size.Cols,
	}
}

func (p *printer) SetCursorPosition(position CanvasPosition) {
	p.cursorPosition = &position
	currentPos := p.CursorPosition()

	moveUp := currentPos.Row - position.Row
	p.MoveCursorUp(moveUp)
	p.MoveCursorToStartOfLine()
	p.MoveCursorRight(position.Col)
}

func (p *printer) Fprintf(format string, a ...any) {
	p.writeLock.Lock()
	defer p.writeLock.Unlock()

	content := fmt.Sprintf(format, a...)
	lineCount := strings.Count(content, "\n")
	p.size.Rows += lineCount

	var lastLine string

	if lineCount > 0 {
		lines := strings.Split(content, "\n")
		lastLine = lines[len(lines)-1]
		p.currentLine = lastLine
	} else {
		lastLine = content
		p.currentLine += lastLine
	}

	p.size.Cols = len(specialTextRegex.ReplaceAllString(p.currentLine, ""))

	fmt.Fprint(p.writer, content)
}

func (p *printer) Fprintln(a ...any) {
	p.Fprintf(fmt.Sprintln(a...))
}

func (p *printer) ClearCanvas() {
	p.clearLock.Lock()
	defer p.clearLock.Unlock()

	if p.cursorPosition != nil {
		moveCount := p.size.Rows - p.cursorPosition.Row
		p.MoveCursorToStartOfLine()
		p.MoveCursorDown(moveCount)
	}

	p.ClearLine()

	for i := 0; i < p.size.Rows; i++ {
		p.MoveCursorUp(0)
		p.ClearLine()
	}

	p.size = &CanvasSize{}
	p.cursorPosition = nil
}

func (p *printer) ClearLine() {
	fmt.Fprint(p.writer, "\033[2K\r")
}
