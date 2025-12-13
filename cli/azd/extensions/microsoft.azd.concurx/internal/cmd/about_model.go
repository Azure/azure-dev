// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// aboutModel holds the state for the about screen animation
type aboutModel struct {
	width      int
	height     int
	position   int
	asciiArt   []string
	quitting   bool
	colorIndex int
}

type tickAboutMsg time.Time

// ASCII art for "concurX"
var concurXArt = []string{
	"                                  ",
	"   ██████╗ ██████╗ ███╗   ██╗ ██████╗██╗   ██╗██████╗ ██╗  ██╗",
	"  ██╔════╝██╔═══██╗████╗  ██║██╔════╝██║   ██║██╔══██╗╚██╗██╔╝",
	"  ██║     ██║   ██║██╔██╗ ██║██║     ██║   ██║██████╔╝ ╚███╔╝ ",
	"  ██║     ██║   ██║██║╚██╗██║██║     ██║   ██║██╔══██╗ ██╔██╗ ",
	"  ╚██████╗╚██████╔╝██║ ╚████║╚██████╗╚██████╔╝██║  ██║██╔╝ ██╗",
	"   ╚═════╝ ╚═════╝ ╚═╝  ╚═══╝ ╚═════╝ ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝",
	"                                  ",
}

var artWidth = 68 // Width of the ASCII art

// Rainbow colors for cycling animation
var rainbowColors = []string{
	"39",  // Blue
	"45",  // Light Blue
	"51",  // Cyan
	"87",  // Light Purple
	"201", // Magenta
	"199", // Pink
	"213", // Light Pink
	"207", // Light Purple
	"141", // Purple
	"99",  // Light Purple
}

func newAboutModel() aboutModel {
	return aboutModel{
		width:      80,
		height:     24,
		position:   0,
		asciiArt:   concurXArt,
		colorIndex: 0,
	}
}

func (m aboutModel) Init() tea.Cmd {
	return tickAbout()
}

func tickAbout() tea.Cmd {
	return tea.Tick(time.Millisecond*80, func(t time.Time) tea.Msg {
		return tickAboutMsg(t)
	})
}

func (m aboutModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickAboutMsg:
		if !m.quitting {
			// Move position to the right
			m.position++
			// Wrap position when art has completely scrolled across
			// This creates a seamless loop
			if m.position >= m.width {
				m.position = m.position - m.width
			}
			// Cycle through colors for a rainbow effect
			m.colorIndex = (m.colorIndex + 1) % len(rainbowColors)
			return m, tickAbout()
		}
	}

	return m, nil
}

func (m aboutModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Calculate vertical centering
	artHeight := len(m.asciiArt)
	topPadding := (m.height - artHeight) / 2
	if topPadding < 0 {
		topPadding = 0
	}

	// Add top padding
	for i := 0; i < topPadding; i++ {
		b.WriteString("\n")
	}

	// Calculate horizontal position (can be negative when entering from right)
	xPos := m.position - artWidth

	// Render each line of ASCII art with Italian flag colors
	for _, line := range m.asciiArt {
		renderedLine := m.renderLineWithColors(line, xPos)
		b.WriteString(renderedLine)
		b.WriteString("\n")
	}

	// Add bottom padding and help text
	for i := 0; i < m.height-topPadding-artHeight-2; i++ {
		b.WriteString("\n")
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpText := "Press q, ESC, or Ctrl+C to exit"
	padding := (m.width - len(helpText)) / 2
	if padding < 0 {
		padding = 0
	}
	b.WriteString(strings.Repeat(" ", padding))
	b.WriteString(helpStyle.Render(helpText))

	return b.String()
}

// renderLineWithColors renders a line with Italian flag colors based on screen position
// Green for left third, white for middle third, red for right third
func (m aboutModel) renderLineWithColors(line string, xPos int) string {
	var b strings.Builder

	// Calculate the boundaries for the three color zones
	leftBoundary := m.width / 3
	rightBoundary := (m.width * 2) / 3

	// Define Italian flag colors
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("40"))  // Green
	whiteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255")) // White
	redStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))   // Red

	lineRunes := []rune(line)

	// Track which characters to render and at what position
	type charPos struct {
		ch      rune
		screenX int
	}
	chars := make([]charPos, 0, len(lineRunes))

	for i, ch := range lineRunes {
		screenX := xPos + i
		// Handle wrapping using modulo for seamless looping
		if screenX < 0 {
			screenX = screenX + m.width
		}
		if screenX >= m.width {
			screenX = screenX % m.width
		}
		if screenX >= 0 && screenX < m.width {
			chars = append(chars, charPos{ch: ch, screenX: screenX})
		}
	}

	// Build the line character by character with appropriate colors
	currentPos := 0
	for currentPos < m.width {
		// Find if there's a character at this position
		found := false
		var ch rune
		for _, cp := range chars {
			if cp.screenX == currentPos {
				ch = cp.ch
				found = true
				break
			}
		}

		if !found {
			b.WriteRune(' ')
		} else {
			// Determine color based on position
			var style lipgloss.Style
			if currentPos < leftBoundary {
				style = greenStyle
			} else if currentPos < rightBoundary {
				style = whiteStyle
			} else {
				style = redStyle
			}
			b.WriteString(style.Render(string(ch)))
		}
		currentPos++
	}

	return b.String()
}
