package ux

import (
	"fmt"
	"io"
)

type Screen interface {
	ClearLine()
}

func NewScreen(writer io.Writer) Screen {
	return &screen{
		writer: writer,
	}
}

type screen struct {
	writer io.Writer
}

func (c *screen) ClearLine() {
	fmt.Fprint(c.writer, "\033[2K\r")
}
