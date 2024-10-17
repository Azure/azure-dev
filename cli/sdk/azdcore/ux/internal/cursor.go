package internal

import (
	"fmt"
	"io"
)

type Cursor interface {
	MoveCursorUp(lines int)
	MoveCursorDown(lines int)
	MoveCursorLeft(columns int)
	MoveCursorRight(columns int)
	MoveCursorToStartOfLine()
	HideCursor()
	ShowCursor()
}

func NewCursor(writer io.Writer) Cursor {
	return &cursor{
		writer: writer,
	}
}

type cursor struct {
	writer io.Writer
}

func (c *cursor) MoveCursorUp(lines int) {
	fmt.Fprintf(c.writer, "\033[%dA", lines)
}

func (c *cursor) MoveCursorDown(lines int) {
	fmt.Fprintf(c.writer, "\033[%dB", lines)
}

func (c *cursor) MoveCursorLeft(columns int) {
	fmt.Fprintf(c.writer, "\033[%dD", columns)
}

func (c *cursor) MoveCursorRight(columns int) {
	fmt.Fprintf(c.writer, "\033[%dC", columns)
}

func (c *cursor) MoveCursorToStartOfLine() {
	fmt.Fprint(c.writer, "\r")
}

func (c *cursor) HideCursor() {
	fmt.Fprint(c.writer, "\033[?25l")
}

func (c *cursor) ShowCursor() {
	fmt.Fprint(c.writer, "\033[?25h")
}
