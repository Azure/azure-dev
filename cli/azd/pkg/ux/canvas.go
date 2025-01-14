package ux

import (
	"io"
	"sync"
)

// Canvas is a base component for UX components that require a canvas for rendering.
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
}

// NewCanvas creates a new Canvas instance.
func NewCanvas(visuals ...Visual) Canvas {
	canvas := &canvas{
		visuals:   visuals,
		renderMap: make(map[Visual]*VisualContext),
	}

	return canvas
}

// WithWriter sets the writer for the canvas.
func (c *canvas) WithWriter(writer io.Writer) Canvas {
	c.writer = writer
	return c
}

// Run starts the canvas.
func (c *canvas) Run() error {
	c.printer = NewPrinter(c.writer)
	return c.Update()
}

// Update updates the canvas to force a re-render.
func (c *canvas) Update() error {
	c.updateLock.Lock()
	defer c.updateLock.Unlock()

	if c.printer == nil {
		return nil
	}

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
	err := visual.Render(c.printer)
	if err != nil {
		return err
	}

	return nil
}

// CursorPosition represents the position of the cursor on the canvas.
type CursorPosition struct {
	Row int
	Col int
}

// CanvasSize represents the size of the canvas.
type CanvasSize struct {
	Rows int
	Cols int
}

func newCanvasSize() *CanvasSize {
	return &CanvasSize{
		Rows: 1,
		Cols: 0,
	}
}
