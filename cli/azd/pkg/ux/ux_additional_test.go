// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"errors"
	"testing"
)

func TestConsoleWidth_from_env(t *testing.T) {
	t.Setenv("COLUMNS", "200")

	// ConsoleWidth uses consolesize-go first; if that returns <=0 it falls back to COLUMNS
	width := ConsoleWidth()
	if width <= 0 {
		t.Fatalf("ConsoleWidth() = %d, want > 0", width)
	}
}

func TestConsoleWidth_invalid_COLUMNS_fallback(t *testing.T) {
	t.Setenv("COLUMNS", "not-a-number")

	width := ConsoleWidth()
	if width <= 0 {
		t.Fatalf("ConsoleWidth() = %d, want > 0", width)
	}
}

func TestConsoleWidth_empty_COLUMNS_uses_default(t *testing.T) {
	t.Setenv("COLUMNS", "")

	width := ConsoleWidth()
	if width <= 0 {
		t.Fatalf("ConsoleWidth() = %d, want > 0", width)
	}
}

func TestPtr(t *testing.T) {
	intVal := 42
	p := Ptr(intVal)
	if p == nil {
		t.Fatal("Ptr should return non-nil pointer")
	}
	if *p != 42 {
		t.Fatalf("*Ptr(42) = %d, want 42", *p)
	}

	strVal := "hello"
	sp := Ptr(strVal)
	if *sp != "hello" {
		t.Fatalf("*Ptr(hello) = %q, want hello", *sp)
	}
}

func TestRender_creates_visual(t *testing.T) {
	v := Render(func(p Printer) error {
		return nil
	})

	if v == nil {
		t.Fatal("Render should return non-nil Visual")
	}
}

func TestNewVisualElement_Render(t *testing.T) {
	renderCalled := false
	elem := NewVisualElement(func(p Printer) error {
		renderCalled = true
		p.Fprintf("test output")
		return nil
	})

	printer := NewPrinter(&bytes.Buffer{})
	err := elem.Render(printer)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !renderCalled {
		t.Fatal("Render function should have been called")
	}
}

func TestNewVisualElement_Render_error(t *testing.T) {
	expectedErr := errors.New("render failed")
	elem := NewVisualElement(func(p Printer) error {
		return expectedErr
	})

	printer := NewPrinter(&bytes.Buffer{})
	err := elem.Render(printer)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Render error = %v, want %v", err, expectedErr)
	}
}

func TestNewVisualElement_WithCanvas(t *testing.T) {
	elem := NewVisualElement(func(p Printer) error { return nil })

	var buf bytes.Buffer
	canvas := NewCanvas().WithWriter(&buf)
	result := elem.WithCanvas(canvas)

	if result != elem {
		t.Fatal("WithCanvas should return the same element for chaining")
	}
}

func TestNewPrinter_with_buffer(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	if printer == nil {
		t.Fatal("NewPrinter should return non-nil")
	}

	printer.Fprintf("hello %s", "world")
	if !bytes.Contains(buf.Bytes(), []byte("hello world")) {
		t.Fatalf("Fprintf output = %q, want to contain 'hello world'", buf.String())
	}
}

func TestNewPrinter_nil_writer_defaults_to_stdout(t *testing.T) {
	// Should not panic
	printer := NewPrinter(nil)
	if printer == nil {
		t.Fatal("NewPrinter(nil) should return non-nil")
	}
}

func TestPrinter_Fprintln(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	printer.Fprintln("line 1")
	printer.Fprintln("line 2")

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("line 1\n")) {
		t.Fatalf("Fprintln output should contain 'line 1\\n', got %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("line 2\n")) {
		t.Fatalf("Fprintln output should contain 'line 2\\n', got %q", output)
	}
}

func TestPrinter_Size_tracks_rows(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	// Initial size
	size := printer.Size()
	if size.Rows != 1 {
		t.Fatalf("Initial Rows = %d, want 1", size.Rows)
	}

	printer.Fprintf("line 1\n")
	size = printer.Size()
	if size.Rows < 2 {
		t.Fatalf("After one newline, Rows = %d, want >= 2", size.Rows)
	}
}

func TestPrinter_CursorPosition(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	pos := printer.CursorPosition()
	if pos.Row < 1 {
		t.Fatalf("CursorPosition().Row = %d, want >= 1", pos.Row)
	}
}

func TestPrinter_ClearCanvas(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	printer.Fprintf("some content\n")
	printer.Fprintf("more content\n")

	// Clear should not panic and should reset state
	printer.ClearCanvas()

	size := printer.Size()
	if size.Rows != 1 {
		t.Fatalf("After ClearCanvas, Rows = %d, want 1", size.Rows)
	}
}

func TestPrinter_SetCursorPosition_same_position_noop(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf)
	pos := CursorPosition{Row: 5, Col: 3}

	beforeFirst := buf.Len()
	p.SetCursorPosition(pos)
	afterFirst := buf.Len()
	if afterFirst <= beforeFirst {
		t.Fatalf("expected first SetCursorPosition to write initialization escape codes, len before = %d, after = %d", beforeFirst, afterFirst)
	}

	// Setting same position should not write additional escape codes
	p.SetCursorPosition(pos)
	afterSecond := buf.Len()

	if afterSecond != afterFirst {
		t.Fatalf("expected second SetCursorPosition with same position to be a no-op, len after first = %d, after second = %d", afterFirst, afterSecond)
	}
}

func TestNewCanvasSize(t *testing.T) {
	size := newCanvasSize()
	if size.Rows != 1 {
		t.Fatalf("newCanvasSize().Rows = %d, want 1", size.Rows)
	}
	if size.Cols != 0 {
		t.Fatalf("newCanvasSize().Cols = %d, want 0", size.Cols)
	}
}

func TestCanvas_Run_with_visual(t *testing.T) {
	var buf bytes.Buffer
	renderCalled := false

	visual := NewVisualElement(func(p Printer) error {
		renderCalled = true
		p.Fprintf("canvas output")
		return nil
	})

	canvas := NewCanvas(visual).WithWriter(&buf)
	defer canvas.Close()

	err := canvas.Run()
	if err != nil {
		t.Fatalf("Canvas.Run() error: %v", err)
	}

	if !renderCalled {
		t.Fatal("Visual.Render should have been called")
	}

	if !bytes.Contains(buf.Bytes(), []byte("canvas output")) {
		t.Fatalf("Canvas output = %q, want to contain 'canvas output'", buf.String())
	}
}

func TestCanvas_Run_multiple_visuals(t *testing.T) {
	var buf bytes.Buffer
	callOrder := []string{}

	v1 := NewVisualElement(func(p Printer) error {
		callOrder = append(callOrder, "v1")
		p.Fprintf("first")
		return nil
	})

	v2 := NewVisualElement(func(p Printer) error {
		callOrder = append(callOrder, "v2")
		p.Fprintf("second")
		return nil
	})

	canvas := NewCanvas(v1, v2).WithWriter(&buf)
	defer canvas.Close()

	err := canvas.Run()
	if err != nil {
		t.Fatalf("Canvas.Run() error: %v", err)
	}

	if len(callOrder) != 2 || callOrder[0] != "v1" || callOrder[1] != "v2" {
		t.Fatalf("Call order = %v, want [v1 v2]", callOrder)
	}
}

func TestCanvas_render_error_propagates(t *testing.T) {
	var buf bytes.Buffer
	expectedErr := errors.New("visual render failed")

	visual := NewVisualElement(func(p Printer) error {
		return expectedErr
	})

	canvas := NewCanvas(visual).WithWriter(&buf)
	defer canvas.Close()

	err := canvas.Run()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Canvas.Run() error = %v, want %v", err, expectedErr)
	}
}

func TestCanvas_Clear(t *testing.T) {
	var buf bytes.Buffer

	visual := NewVisualElement(func(p Printer) error {
		p.Fprintf("some content")
		return nil
	})

	canvas := NewCanvas(visual).WithWriter(&buf)
	defer canvas.Close()

	err := canvas.Run()
	if err != nil {
		t.Fatalf("Canvas.Run() error: %v", err)
	}

	err = canvas.Clear()
	if err != nil {
		t.Fatalf("Canvas.Clear() error: %v", err)
	}
}

func TestCanvasManager_CanUpdate(t *testing.T) {
	mgr := newCanvasManager()
	var buf bytes.Buffer

	c1 := NewCanvas().WithWriter(&buf)
	c2 := NewCanvas().WithWriter(&buf)
	defer c1.Close()
	defer c2.Close()

	// No focused canvas — any canvas can update
	if !mgr.CanUpdate(c1) {
		t.Fatal("CanUpdate(c1) should be true when no canvas is focused")
	}
}

func TestErrCancelled(t *testing.T) {
	if ErrCancelled == nil {
		t.Fatal("ErrCancelled should not be nil")
	}
	if ErrCancelled.Error() == "" {
		t.Fatal("ErrCancelled.Error() should not be empty")
	}
}
