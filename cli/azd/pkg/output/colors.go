// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/fatih/color"
	"github.com/nathan-fiscaletti/consolesize-go"
)

// withLinkFormat creates string with hyperlink-looking color
func WithLinkFormat(link string, a ...interface{}) string {
	return color.HiCyanString(link, a...)
}

// withHighLightFormat creates string with highlight-looking color
func WithHighLightFormat(text string, a ...interface{}) string {
	return color.HiBlueString(text, a...)
}

func WithErrorFormat(text string, a ...interface{}) string {
	return color.RedString(text, a...)
}

func WithWarningFormat(text string, a ...interface{}) string {
	return color.YellowString(text, a...)
}

func WithSuccessFormat(text string, a ...interface{}) string {
	return color.GreenString(text, a...)
}

func WithGrayFormat(text string, a ...interface{}) string {
	return color.HiBlackString(text, a...)
}

func WithHintFormat(text string, a ...interface{}) string {
	return color.MagentaString(text, a...)
}

func WithBold(text string, a ...interface{}) string {
	format := color.New(color.FgHiWhite, color.Bold)
	return format.Sprintf(text, a...)
}

func WithUnderline(text string, a ...interface{}) string {
	format := color.New(color.Underline)
	return format.Sprintf(text, a...)
}

// WithBackticks wraps text with the backtick (`) character.
func WithBackticks(s string) string {
	return fmt.Sprintf("`%s`", s)
}

func AzdLabel() string {
	return "[azd]"
}

func AzdAgentLabel() string {
	return color.HiMagentaString(fmt.Sprintf("🤖 %s Agent", AzdLabel()))
}

// WithMarkdown converts markdown to terminal-friendly colorized output using glamour.
// This provides rich markdown rendering including bold, italic, code blocks, headers, etc.
func WithMarkdown(markdownText string) string {
	// Get dynamic console width with fallback to 120
	consoleWidth := getConsoleWidth()

	// Create a custom glamour renderer with auto-style detection
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(consoleWidth), // Use dynamic console width
	)
	if err != nil {
		// Fallback to returning original text if glamour fails
		return markdownText
	}

	// Render the markdown
	rendered, err := r.Render(markdownText)
	if err != nil {
		// Fallback to returning original text if rendering fails
		return markdownText
	}

	// Trim trailing whitespace that glamour sometimes adds
	return strings.TrimSpace(rendered)
}

// WithHyperlink wraps text with the colored hyperlink format escape sequence.
func WithHyperlink(url string, text string) string {
	return WithLinkFormat(fmt.Sprintf("\033]8;;%s\007%s\033]8;;\007", url, text))
}

// getConsoleWidth gets the console width with fallback logic.
// It uses the consolesize package to get the size and falls back to check the COLUMNS environment variable.
// Defaults to 120 if the console size cannot be determined.
func getConsoleWidth() int {
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
