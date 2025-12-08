// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"io"
	"os"
	"sync"
)

// Canvas is a base component for UX components that require a canvas for rendering.
type canvas struct {
	visuals    []Visual
	printer    Printer
	writer     io.Writer
	updateLock sync.Mutex
	buffer     *bytes.Buffer // Single buffer reused for all rendering
}

type Canvas interface {
	Run() error
	Update() error
	Clear() error
	Close()
	WithWriter(writer io.Writer) Canvas
}

// NewCanvas creates a new Canvas instance.
func NewCanvas(visuals ...Visual) Canvas {
	c := &canvas{
		visuals: visuals,
		buffer:  new(bytes.Buffer),
		writer:  os.Stdout,
	}
	for _, visual := range visuals {
		visual.WithCanvas(c)
	}
	cm.Add(c)
	return c
}

// WithWriter sets the writer for the canvas.
func (c *canvas) WithWriter(writer io.Writer) Canvas {
	c.writer = writer
	return c
}

// Run starts the canvas.
func (c *canvas) Run() error {
	if c.printer == nil {
		c.printer = NewPrinter(c.buffer)
	}
	return c.Update()
}

// Clear clears the canvas.
func (c *canvas) Clear() error {
	c.updateLock.Lock()
	defer c.updateLock.Unlock()

	c.printer.ClearCanvas()
	return c.writeBufferChunked()
}

// Close closes the canvas.
func (c *canvas) Close() {
	cm.Remove(c)
}

// Update updates the canvas to force a re-render.
func (c *canvas) Update() error {
	cm.Lock()
	defer cm.Unlock()

	if !cm.CanUpdate(c) {
		return nil
	}

	c.updateLock.Lock()
	defer c.updateLock.Unlock()

	if c.printer == nil {
		c.printer = NewPrinter(c.buffer)
	}

	c.printer.ClearCanvas()

	if err := c.render(); err != nil {
		return err
	}

	return c.writeBufferChunked()
}

func (c *canvas) writeBufferChunked() error {
	out := c.buffer.Bytes()
	if len(out) > 4096 {
		for i := 0; i < len(out); i += 4096 {
			end := i + 4096
			if end > len(out) {
				end = len(out)
			}
			if _, err := c.writer.Write(out[i:end]); err != nil {
				return err
			}
		}
	} else {
		if _, err := c.writer.Write(out); err != nil {
			return err
		}
	}
	c.buffer.Reset()

	return nil
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
	if err := visual.Render(c.printer); err != nil {
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

type canvasManager struct {
	items         sync.Map
	focusedCanvas Canvas
	focusLock     sync.Mutex
	updateLock    sync.Mutex
}

func newCanvasManager() *canvasManager {
	return &canvasManager{
		items: sync.Map{},
	}
}

func (cm *canvasManager) Add(canvas Canvas) {
	cm.items.Store(canvas, struct{}{})
}

func (cm *canvasManager) Remove(canvas Canvas) {
	cm.items.Delete(canvas)
}

func (cm *canvasManager) Lock() {
	cm.updateLock.Lock()
}

func (cm *canvasManager) Unlock() {
	cm.updateLock.Unlock()
}

// Focus sets the focused canvas and clears non-focused canvases.
func (cm *canvasManager) Focus(canvas Canvas) func() {
	cm.Lock()
	defer cm.Unlock()

	cm.focusLock.Lock()
	cm.focusedCanvas = canvas

	// Clear non-focused canvases
	cm.items.Range(func(key, value any) bool {
		if c, ok := key.(Canvas); ok && c != canvas {
			c.Clear()
		}
		return true
	})

	return func() {
		cm.focusedCanvas = nil
		cm.focusLock.Unlock()
	}
}

func (cm *canvasManager) CanUpdate(canvas Canvas) bool {
	if cm.focusedCanvas == nil || cm.focusedCanvas == canvas {
		return true
	}

	return false
}

var cm = newCanvasManager()
