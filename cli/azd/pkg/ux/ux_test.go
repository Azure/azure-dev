// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestHyperlink(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		text          []string
		expectEscape  bool
		expectedPlain string
	}{
		{
			name:          "URL only",
			url:           "https://example.com",
			text:          nil,
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
		{
			name:          "URL and text are the same",
			url:           "https://example.com",
			text:          []string{"https://example.com"},
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
		{
			name:          "URL and text are different",
			url:           "https://example.com",
			text:          []string{"Example Site"},
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
		{
			name:          "Text is empty string",
			url:           "https://example.com",
			text:          []string{""},
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test non-terminal mode by checking the actual output
			result := Hyperlink(tt.url, tt.text...)
			if len(result) == 0 {
				t.Errorf("expected non-empty result")
			}

			// When running in a non-TTY environment (like most CI systems),
			// the result should be the plain text version
			if !isTTY() {
				if result != tt.expectedPlain {
					t.Errorf("expected %q, got %q", tt.expectedPlain, result)
				}
			}
		})
	}
}

// isTTY checks if stdout is a TTY (for testing purposes)
func isTTY() bool {
	fileInfo, _ := os.Stdout.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func Test_CountLineBreaks(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		width    int
		expected int
	}{
		// Basic cases
		{"Empty string", "", 0, 0},
		{"New Line", "\n", 1, 1},
		{"Short Text", "Hello World", 100, 0},
		{"Multiple Lines", "Hello\nWorld", 100, 1},

		// Edge cases
		{"Multiple Consecutive Newlines", "\n\n\n", 100, 3},
		{"String Ending with Newline", "Hello\n", 100, 1}, // Still counts newline, but no extra
		{"String Starting with Newline", "\nHello", 100, 1},
		{"String with Spaces and Newlines", "   \n   ", 100, 1},

		// Wrapping cases
		{"Exact Width", "1234567890", 10, 0},          // Should not wrap
		{"Slightly Over Width", "12345678901", 10, 1}, // Wraps once
		{"Long Line Wrapping", "This is a very long line that should be wrapped into multiple lines when printed.", 50, 1},
		{"Mixed Short and Long Lines", "Short\nThis is a very long line that wraps.\nAnother short one", 30, 3},

		// Unicode & special characters
		{"Emoji Characters", "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥", 10, 1},             // 10 emoji × 2 cols = 20 cols, wraps once
		{"Emoji Line Wrap", "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥", 10, 2},             // 11 emoji × 2 cols = 22 cols, wraps twice
		{"Mixing Emoji and Text", "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥 Hello!", 10, 2}, // 20 + 7 = 27 cols, wraps twice

		// Trailing newlines shouldn't overcount
		{"Two Printf calls (simulated)", "line 1\nline 2\n", 100, 2}, // Should be exactly 2 lines
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := CountLineBreaks(tc.input, tc.width)
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

func Test_VisibleLength(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected int
	}{
		// Basic cases
		{"Empty String", "", 0},
		{"Plain Text", "Hello World", 11},
		{"Multiple Spaces", "Hello   World", 13},

		// ANSI escape sequence cases
		{"ANSI Color Code", "\x1b[31mHello\x1b[0m", 5},
		{"Multiple ANSI Codes", "\x1b[31mHello\x1b[0m \x1b[32mWorld\x1b[0m", 11},
		{"Mixed ANSI + Spaces", "\x1b[31mHello\x1b[0m   World", 13},
		{"Only ANSI Codes", "\x1b[31m\x1b[0m", 0},
		{"Non-Color ANSI Sequences", "\x1b[1mBold\x1b[22m", 4},
		{"Long ANSI Sequence", "\x1b[38;5;82mGreen Text\x1b[0m", 10},

		// Unicode & special characters
		{"Unicode Characters", "🔥🔥🔥", 6},
		{"Mix of ANSI and Unicode", "\x1b[31m🔥🔥🔥\x1b[0m", 6},

		// Edge Cases
		{"Edge Case: Leading ANSI Code", "\x1b[31mRed\x1b[0mText", 7},
		{"Edge Case: Trailing ANSI Code", "Text\x1b[31mRed\x1b[0m", 7},
		{"Edge Case: ANSI Wrapping Empty String", "\x1b[31m\x1b[0m", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := VisibleLength(tc.input)
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

func Test_TruncateVisible(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		maxWidth int
		expected string
	}{
		// No truncation needed
		{"Short plain text", "Hello", 10, "Hello"},
		{"Exact fit", "Hello", 5, "Hello"},
		{"Empty string", "", 10, ""},

		// Basic truncation
		{"Plain text truncated", "Hello World!", 8, "Hello..."},
		{"Truncated to minimum", "Hello", 3, "..."},

		// Edge cases
		{"Width 0", "Hello", 0, ""},
		{"Width 1", "Hello", 1, "."},
		{"Width 2", "Hello", 2, ".."},
		{
			"ANSI not counted in width",
			"\x1b[31mHello World\x1b[0m",
			8,
			"\x1b[31mHello...\x1b[0m",
		},
		{
			"ANSI fits within width",
			"\x1b[31mHi\x1b[0m",
			10,
			"\x1b[31mHi\x1b[0m",
		},
		{
			"Multiple ANSI sequences truncated",
			"\x1b[1m\x1b[31mBold Red Text\x1b[0m",
			10,
			"\x1b[1m\x1b[31mBold Re...\x1b[0m",
		},

		// Unicode (CJK characters are 2 columns wide)
		{"Unicode truncated", "日本語テスト", 7, "日本..."},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := TruncateVisible(tc.input, tc.maxWidth)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
			// Verify the result doesn't exceed maxWidth visible chars
			if tc.maxWidth > 0 {
				visLen := VisibleLength(result)
				if visLen > tc.maxWidth {
					t.Errorf("visible length %d exceeds maxWidth %d", visLen, tc.maxWidth)
				}
			}
		})
	}
}

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
	switch {
	case p == nil:
		t.Fatal("Ptr should return non-nil pointer")
	case *p != 42:
		t.Fatalf("*Ptr(42) = %d, want 42", *p)
	}

	strVal := "hello"
	sp := Ptr(strVal)
	switch {
	case sp == nil:
		t.Fatal("Ptr should return non-nil pointer for string")
	case *sp != "hello":
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
		t.Fatalf(
			"expected first SetCursorPosition to write escape codes, before = %d, after = %d",
			beforeFirst, afterFirst)
	}

	// Setting same position should not write additional escape codes
	p.SetCursorPosition(pos)
	afterSecond := buf.Len()

	if afterSecond != afterFirst {
		t.Fatalf(
			"expected same-position SetCursorPosition to be a no-op, first = %d, second = %d",
			afterFirst, afterSecond)
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
