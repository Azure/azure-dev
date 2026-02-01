// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Maximum number of visible items in select/multiselect lists
const maxVisibleItems = 3

// promptModel is the Bubble Tea model for handling interactive prompts
type promptModel struct {
	promptType      PromptType
	message         string
	help            string
	choices         []PromptChoice
	filteredIndices []int  // indices of choices that match filter
	filterText      string // current filter text
	defaultValue    any
	textInput       textinput.Model
	selectedIndex   int          // index within filteredIndices
	scrollOffset    int          // for scrolling long lists
	selectedItems   map[int]bool // for multiselect (uses original indices)
	width           int
	height          int
	cancelled       bool
	submitted       bool
}

// Styles for prompt UI
var (
	promptTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("205")).
				MarginBottom(1)

	promptMessageStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("15")).
				MarginBottom(1)

	promptHelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true).
			MarginBottom(1)

	promptChoiceStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252"))

	promptSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true)

	promptCheckedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("46"))

	promptBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("87")).
			Padding(1, 2).
			MarginTop(1)

	promptInstructionStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				MarginTop(1)
)

// newPromptModel creates a new prompt model from a prompt request
func newPromptModel(req *PromptRequest) promptModel {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	// Set default value for text input
	if req.Options.DefaultValue != nil {
		switch v := req.Options.DefaultValue.(type) {
		case string:
			ti.SetValue(v)
		}
	}

	// Handle password type
	if req.Type == PromptTypePassword {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}

	// Initialize selected items for multiselect
	selectedItems := make(map[int]bool)
	if req.Type == PromptTypeMultiSelect {
		// Check for default values in multiselect
		if defaults, ok := req.Options.DefaultValue.([]interface{}); ok {
			for _, d := range defaults {
				if dStr, ok := d.(string); ok {
					for i, choice := range req.Options.Choices {
						if choice.Value == dStr {
							selectedItems[i] = true
							break
						}
					}
				}
			}
		}
	}

	// Set default selection for select
	selectedIndex := 0
	if req.Type == PromptTypeSelect && req.Options.DefaultValue != nil {
		if dStr, ok := req.Options.DefaultValue.(string); ok {
			for i, choice := range req.Options.Choices {
				if choice.Value == dStr {
					selectedIndex = i
					break
				}
			}
		}
	}

	// Set default for confirm
	if req.Type == PromptTypeConfirm && req.Options.DefaultValue != nil {
		if dBool, ok := req.Options.DefaultValue.(bool); ok {
			if dBool {
				selectedIndex = 0 // Yes
			} else {
				selectedIndex = 1 // No
			}
		}
	}

	// Initialize filtered indices to include all choices
	filteredIndices := make([]int, len(req.Options.Choices))
	for i := range req.Options.Choices {
		filteredIndices[i] = i
	}

	return promptModel{
		promptType:      req.Type,
		message:         req.Options.Message,
		help:            req.Options.Help,
		choices:         req.Options.Choices,
		filteredIndices: filteredIndices,
		defaultValue:    req.Options.DefaultValue,
		textInput:       ti,
		selectedIndex:   selectedIndex,
		selectedItems:   selectedItems,
	}
}

func (m promptModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m promptModel) Update(msg tea.Msg) (promptModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			m.submitted = true
			return m, nil

		case "enter":
			// Handle submission
			if m.promptType == PromptTypeMultiSelect {
				m.submitted = true
				return m, nil
			}
			m.submitted = true
			return m, nil

		case "up":
			// Move selection up for select/multiselect/confirm
			if m.promptType == PromptTypeSelect || m.promptType == PromptTypeMultiSelect {
				if m.selectedIndex > 0 {
					m.selectedIndex--
					// Adjust scroll offset if cursor moves above visible area
					if m.selectedIndex < m.scrollOffset {
						m.scrollOffset = m.selectedIndex
					}
				}
			} else if m.promptType == PromptTypeConfirm {
				m.selectedIndex = 0 // Yes
			}
			return m, nil

		case "down":
			// Move selection down for select/multiselect/confirm
			if m.promptType == PromptTypeSelect || m.promptType == PromptTypeMultiSelect {
				if m.selectedIndex < len(m.filteredIndices)-1 {
					m.selectedIndex++
					// Adjust scroll offset if cursor moves below visible area
					if m.selectedIndex >= m.scrollOffset+maxVisibleItems {
						m.scrollOffset = m.selectedIndex - maxVisibleItems + 1
					}
				}
			} else if m.promptType == PromptTypeConfirm {
				m.selectedIndex = 1 // No
			}
			return m, nil

		case "tab":
			// Toggle selection for multiselect using tab (space is used for filter)
			if m.promptType == PromptTypeMultiSelect && len(m.filteredIndices) > 0 {
				originalIdx := m.filteredIndices[m.selectedIndex]
				m.selectedItems[originalIdx] = !m.selectedItems[originalIdx]
			}
			return m, nil

		case "backspace":
			// Handle backspace for filter
			if (m.promptType == PromptTypeSelect || m.promptType == PromptTypeMultiSelect) && len(m.filterText) > 0 {
				m.filterText = m.filterText[:len(m.filterText)-1]
				m.updateFilter()
				return m, nil
			}

		case "y", "Y":
			// Quick yes for confirm
			if m.promptType == PromptTypeConfirm {
				m.selectedIndex = 0
				m.submitted = true
				return m, nil
			}

		case "n", "N":
			// Quick no for confirm
			if m.promptType == PromptTypeConfirm {
				m.selectedIndex = 1
				m.submitted = true
				return m, nil
			}
		}
	}

	// Update text input for string/password/directory types
	if m.promptType == PromptTypeString || m.promptType == PromptTypePassword || m.promptType == PromptTypeDirectory {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	// Handle typing for filter in select/multiselect
	if m.promptType == PromptTypeSelect || m.promptType == PromptTypeMultiSelect {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			// Only handle printable characters for filter
			if len(keyMsg.String()) == 1 && keyMsg.String() >= " " {
				m.filterText += keyMsg.String()
				m.updateFilter()
				return m, nil
			}
		}
	}

	return m, nil
}

// updateFilter updates the filtered indices based on the current filter text
func (m *promptModel) updateFilter() {
	if m.filterText == "" {
		// No filter, show all
		m.filteredIndices = make([]int, len(m.choices))
		for i := range m.choices {
			m.filteredIndices[i] = i
		}
	} else {
		// Filter choices
		m.filteredIndices = nil
		lowerFilter := strings.ToLower(m.filterText)
		for i, choice := range m.choices {
			if strings.Contains(strings.ToLower(choice.Value), lowerFilter) ||
				strings.Contains(strings.ToLower(choice.Detail), lowerFilter) {
				m.filteredIndices = append(m.filteredIndices, i)
			}
		}
	}
	// Reset selection to first item
	m.selectedIndex = 0
	m.scrollOffset = 0
}

func (m promptModel) View() string {
	var content strings.Builder

	// Message (compact, no title)
	content.WriteString(promptMessageStyle.Render(m.message))
	content.WriteString("\n")

	// Render based on prompt type
	switch m.promptType {
	case PromptTypeString, PromptTypePassword, PromptTypeDirectory:
		content.WriteString(m.textInput.View())
		content.WriteString("\n")
		content.WriteString(promptInstructionStyle.Render("Enter to submit • Esc to cancel"))

	case PromptTypeSelect:
		// Show filter input if active
		if m.filterText != "" {
			content.WriteString(promptSelectedStyle.Render(fmt.Sprintf("Filter: %s", m.filterText)))
			content.WriteString("\n")
		}

		if len(m.filteredIndices) == 0 {
			content.WriteString(promptHelpStyle.Render("  No matches"))
			content.WriteString("\n")
		} else {
			// Calculate visible range based on filtered items
			visibleCount := min(maxVisibleItems, len(m.filteredIndices))
			endIndex := min(m.scrollOffset+visibleCount, len(m.filteredIndices))

			// Show scroll indicator at top if not at beginning
			if m.scrollOffset > 0 {
				content.WriteString(promptHelpStyle.Render(fmt.Sprintf("  ↑ %d more", m.scrollOffset)))
				content.WriteString("\n")
			}

			// Render only visible items from filtered list
			for i := m.scrollOffset; i < endIndex; i++ {
				originalIdx := m.filteredIndices[i]
				choice := m.choices[originalIdx]
				cursor := "  "
				style := promptChoiceStyle
				if i == m.selectedIndex {
					cursor = "▸ "
					style = promptSelectedStyle
				}
				line := cursor + choice.Value
				if choice.Detail != "" {
					line += fmt.Sprintf(" (%s)", choice.Detail)
				}
				content.WriteString(style.Render(line))
				content.WriteString("\n")
			}

			// Show scroll indicator at bottom if more items below
			if endIndex < len(m.filteredIndices) {
				content.WriteString(promptHelpStyle.Render(fmt.Sprintf("  ↓ %d more", len(m.filteredIndices)-endIndex)))
				content.WriteString("\n")
			}

			// Show position indicator
			content.WriteString(promptHelpStyle.Render(fmt.Sprintf("  [%d/%d]", m.selectedIndex+1, len(m.filteredIndices))))
			content.WriteString("\n")
		}
		content.WriteString(promptInstructionStyle.Render("Type to filter • ↑/↓ nav • Enter select"))

	case PromptTypeMultiSelect:
		// Show filter input if active
		if m.filterText != "" {
			content.WriteString(promptSelectedStyle.Render(fmt.Sprintf("Filter: %s", m.filterText)))
			content.WriteString("\n")
		}

		if len(m.filteredIndices) == 0 {
			content.WriteString(promptHelpStyle.Render("  No matches"))
			content.WriteString("\n")
		} else {
			// Calculate visible range based on filtered items
			visibleCount := min(maxVisibleItems, len(m.filteredIndices))
			endIndex := min(m.scrollOffset+visibleCount, len(m.filteredIndices))

			// Show scroll indicator at top if not at beginning
			if m.scrollOffset > 0 {
				content.WriteString(promptHelpStyle.Render(fmt.Sprintf("  ↑ %d more", m.scrollOffset)))
				content.WriteString("\n")
			}

			// Render only visible items from filtered list
			for i := m.scrollOffset; i < endIndex; i++ {
				originalIdx := m.filteredIndices[i]
				choice := m.choices[originalIdx]
				cursor := "  "
				checkbox := "[ ]"
				style := promptChoiceStyle

				if i == m.selectedIndex {
					cursor = "▸ "
					style = promptSelectedStyle
				}
				if m.selectedItems[originalIdx] {
					checkbox = "[✓]"
					style = promptCheckedStyle
					if i == m.selectedIndex {
						style = promptSelectedStyle
					}
				}

				line := cursor + checkbox + " " + choice.Value
				if choice.Detail != "" {
					line += fmt.Sprintf(" (%s)", choice.Detail)
				}
				content.WriteString(style.Render(line))
				content.WriteString("\n")
			}

			// Show scroll indicator at bottom if more items below
			if endIndex < len(m.filteredIndices) {
				content.WriteString(promptHelpStyle.Render(fmt.Sprintf("  ↓ %d more", len(m.filteredIndices)-endIndex)))
				content.WriteString("\n")
			}

			// Show position indicator
			content.WriteString(promptHelpStyle.Render(fmt.Sprintf("  [%d/%d]", m.selectedIndex+1, len(m.filteredIndices))))
			content.WriteString("\n")
		}
		content.WriteString(promptInstructionStyle.Render("Type filter • ↑/↓ nav • Tab toggle • Enter submit"))

	case PromptTypeConfirm:
		yesStyle := promptChoiceStyle
		noStyle := promptChoiceStyle
		yesCursor := "  "
		noCursor := "  "

		if m.selectedIndex == 0 {
			yesStyle = promptSelectedStyle
			yesCursor = "▸ "
		} else {
			noStyle = promptSelectedStyle
			noCursor = "▸ "
		}

		content.WriteString(yesStyle.Render(yesCursor + "Yes"))
		content.WriteString("\n")
		content.WriteString(noStyle.Render(noCursor + "No"))
		content.WriteString("\n")
		content.WriteString(promptInstructionStyle.Render("↑/↓ or y/n to select • Enter to confirm • Esc to cancel"))
	}

	// Wrap in a box
	boxContent := promptBoxStyle.Render(content.String())

	// Center the box if we have dimensions
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(
			m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			boxContent,
		)
	}

	return boxContent
}

// GetResponse returns the prompt response based on user input
func (m promptModel) GetResponse() *PromptResponse {
	if m.cancelled {
		return &PromptResponse{
			Status: PromptStatusCancelled,
		}
	}

	switch m.promptType {
	case PromptTypeString, PromptTypePassword, PromptTypeDirectory:
		return &PromptResponse{
			Status: PromptStatusSuccess,
			Value:  m.textInput.Value(),
		}

	case PromptTypeSelect:
		if len(m.filteredIndices) > 0 && m.selectedIndex < len(m.filteredIndices) {
			originalIdx := m.filteredIndices[m.selectedIndex]
			return &PromptResponse{
				Status: PromptStatusSuccess,
				Value:  m.choices[originalIdx].Value,
			}
		}
		return &PromptResponse{
			Status:  PromptStatusError,
			Message: "No selection made",
		}

	case PromptTypeMultiSelect:
		var selected []string
		for i, choice := range m.choices {
			if m.selectedItems[i] {
				selected = append(selected, choice.Value)
			}
		}
		return &PromptResponse{
			Status: PromptStatusSuccess,
			Value:  selected,
		}

	case PromptTypeConfirm:
		value := "false"
		if m.selectedIndex == 0 {
			value = "true"
		}
		return &PromptResponse{
			Status: PromptStatusSuccess,
			Value:  value,
		}
	}

	return &PromptResponse{
		Status:  PromptStatusError,
		Message: "Unknown prompt type",
	}
}
