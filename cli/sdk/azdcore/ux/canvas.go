package ux

import (
	"io"
	"sync"
)

type canvas struct {
	visuals    []Visual
	printer    Printer
	writer     io.Writer
	renderMap  map[Visual]*VisualContext
	updateLock sync.Mutex
}

type Canvas interface {
	Run() error
	Update() error
	WithWriter(writer io.Writer) Canvas
	CursorPosition() CanvasPosition
	SetCursorPosition(position CanvasPosition)
}

func NewCanvas(visuals ...Visual) Canvas {
	canvas := &canvas{
		visuals:   visuals,
		renderMap: make(map[Visual]*VisualContext),
	}

	return canvas
}

func (c *canvas) CursorPosition() CanvasPosition {
	return c.printer.CursorPosition()
}

func (c *canvas) SetCursorPosition(position CanvasPosition) {
	c.printer.SetCursorPosition(position)
}

func (c *canvas) WithWriter(writer io.Writer) Canvas {
	c.writer = writer
	return c
}

func (c *canvas) Run() error {
	c.printer = NewPrinter(c.writer)
	return c.Update()
}

func (c *canvas) Update() error {
	if c.printer == nil {
		return nil
	}

	c.updateLock.Lock()
	defer c.updateLock.Unlock()

	c.printer.ClearCanvas()
	return c.render()
}

func (c *canvas) render() error {
	for _, visual := range c.visuals {
		if err := c.renderVisual(visual); err != nil {
			return err
		}
	}

	return nil
}

func (c *canvas) renderVisual(visual Visual) error {
	err := visual.WithCanvas(c).Render(c.printer)
	if err != nil {
		return err
	}

	return nil
}

type CanvasPosition struct {
	Row int
	Col int
}

type CanvasSize struct {
	Rows int
	Cols int
}
