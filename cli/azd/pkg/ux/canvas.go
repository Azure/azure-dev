// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
	updateLock sync.Mutex
}

type Canvas interface {
	Run() error
	Update() error
	Clear()
	Close()
	WithWriter(writer io.Writer) Canvas
}

// NewCanvas creates a new Canvas instance.
func NewCanvas(visuals ...Visual) Canvas {
	canvas := &canvas{
		visuals: visuals,
	}

	for _, visual := range visuals {
		visual.WithCanvas(canvas)
	}

	cm.Add(canvas)

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

// Clear clears the canvas.
func (c *canvas) Clear() {
	c.printer.ClearCanvas()
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
		c.printer.ClearCanvas()
		return nil
	}

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
