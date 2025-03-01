// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"
	"github.com/fatih/color"
)

var ErrCancelled = internal.ErrCancelled

func init() {
	forceColorVal, has := os.LookupEnv("FORCE_COLOR")
	if has && forceColorVal == "1" {
		color.NoColor = false
	}
}

// Hyperlink returns a hyperlink formatted string.
func Hyperlink(url string, text ...string) string {
	if len(text) == 0 {
		text = []string{url}
	}

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
