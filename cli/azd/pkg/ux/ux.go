// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/azure/azure-dev/internal/terminal"
	"github.com/azure/azure-dev/pkg/ux/internal"
	"github.com/fatih/color"
	"github.com/nathan-fiscaletti/consolesize-go"
)

var ErrCancelled = internal.ErrCancelled

func init() {
	forceColorVal, has := os.LookupEnv("FORCE_COLOR")
	if has && forceColorVal == "1" {
		color.NoColor = false
	}
}

// Hyperlink returns a hyperlink formatted string.
// When stdout is not a terminal (e.g., in CI/CD pipelines like GitHub Actions),
// it returns the plain URL without escape codes to avoid displaying raw ANSI sequences.
func Hyperlink(url string, text ...string) string {
	if len(text) == 0 {
		text = []string{url}
	}

	// Check if stdout is a terminal
	if !terminal.IsTerminal(os.Stdout.Fd(), os.Stdin.Fd()) {
		// Not a terminal - return plain URL without escape codes
		return url
	}

	// Terminal - use hyperlink escape codes
	return fmt.Sprintf("\033]8;;%s\007%s\033]8;;\007", url, text[0])
}

var BoldString = color.New(color.Bold).SprintfFunc()

func Ptr[T any](value T) *T {
	return &value
}

func Render(renderFn RenderFn) Visual {
	return NewVisualElement(renderFn)
}

type RenderFn func(printer Printer) error

// countLineBreaks calculates the number of lines that will be displayed on the screen,
// considering both explicit line breaks (`\n`) and automatic wrapping based on console width.
func CountLineBreaks(content string, width int) int {
	lineCount := strings.Count(content, "\n")
	lines := strings.Split(content, "\n")
	additionalLines := 0

	for _, line := range lines {
		visibleLen := VisibleLength(line)

		if visibleLen > width {
			wrappingLines := int(math.Ceil(float64(visibleLen)/float64(width))) - 1
			additionalLines += wrappingLines
		}
	}

	return lineCount + additionalLines
}

// visibleLength calculates the number of visible characters in a string
// by removing ANSI codes and counting the actual characters.
// Cannot use len() as it counts bytes not runes
func VisibleLength(s string) int {
	// Remove ANSI codes such as color, formatting, etc.
	cleaned := specialTextRegex.ReplaceAllString(s, "")
	// Count actual visible characters
	return utf8.RuneCountInString(cleaned)
}

// ConsoleWidth returns the width of the console in characters.
// It uses the consolesize package to get the size and falls back to check the COLUMNS environment variable
// Defaults to 120 if the console size cannot be determined.
func ConsoleWidth() int {
	width, _ := consolesize.GetConsoleSize()
	if width <= 0 {
		// Default to 120 if console size cannot be determined
		width = 120

		consoleWidth := os.Getenv("COLUMNS")
		if consoleWidth != "" {
			if parsedWidth, err := strconv.Atoi(consoleWidth); err == nil {
				width = parsedWidth
			}
		}
	}

	return width
}
