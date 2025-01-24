// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"fmt"
	"io"
)

// Cursor is a base component for UX components that require cursor manipulation.
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

// MoveCursorUp moves the cursor up by the specified number of lines.
func (c *cursor) MoveCursorUp(lines int) {
	fmt.Fprintf(c.writer, "\033[%dA", lines)
}

// MoveCursorDown moves the cursor down by the specified number of lines.
func (c *cursor) MoveCursorDown(lines int) {
	fmt.Fprintf(c.writer, "\033[%dB", lines)
}

// MoveCursorLeft moves the cursor left by the specified number of columns.
func (c *cursor) MoveCursorLeft(columns int) {
	fmt.Fprintf(c.writer, "\033[%dD", columns)
}

// MoveCursorRight moves the cursor right by the specified number of columns.
func (c *cursor) MoveCursorRight(columns int) {
	fmt.Fprintf(c.writer, "\033[%dC", columns)
}

// MoveCursorToStartOfLine moves the cursor to the start of the current line.
func (c *cursor) MoveCursorToStartOfLine() {
	fmt.Fprint(c.writer, "\r")
}

// HideCursor hides the cursor.
func (c *cursor) HideCursor() {
	fmt.Fprint(c.writer, "\033[?25l")
}

// ShowCursor shows the cursor.
func (c *cursor) ShowCursor() {
	fmt.Fprint(c.writer, "\033[?25h")
}
