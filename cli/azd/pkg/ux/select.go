// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"

	"dario.cat/mergo"
	"github.com/eiannone/keyboard"
	"github.com/fatih/color"
)

// SelectOptions represents the options for the Select component.
type SelectOptions struct {
	// The writer to use for output (default: os.Stdout)
	Writer io.Writer
	// The reader to use for input (default: os.Stdin)
	Reader io.Reader
	// The default value to use for the prompt (default: nil)
	SelectedIndex *int
	// The message to display before the prompt
	Message string
	// The available options to display
	Choices []*SelectChoice
	// The optional message to display when the user types ? (default: "")
	HelpMessage string
	// The optional hint text that display after the message (default: "[Type ? for hint]")
	Hint string
	// The maximum number of options to display at one time (default: 6)
	DisplayCount int
	// Whether or not to display the number prefix before each option (default: false)
	DisplayNumbers *bool
	// Whether or not to disable filtering (default: true)
	EnableFiltering *bool
}

type SelectChoice struct {
	Value string
	Label string
}

type indexedSelectChoice struct {
	Index int
	*SelectChoice
}

var DefaultSelectOptions SelectOptions = SelectOptions{
	Writer:          os.Stdout,
	Reader:          os.Stdin,
	SelectedIndex:   Ptr(0),
	DisplayCount:    6,
	EnableFiltering: Ptr(true),
	DisplayNumbers:  Ptr(false),
}

// Select is a component for prompting the user to select an option from a list.
type Select struct {
	input  *internal.Input
	cursor internal.Cursor
	canvas Canvas

	options            *SelectOptions
	currentIndex       *int
	showHelp           bool
	complete           bool
	filter             string
	choices            []*indexedSelectChoice
	filteredChoices    []*indexedSelectChoice
	selectedChoice     *indexedSelectChoice
	hasValidationError bool
	validationMessage  string
	cancelled          bool
	cursorPosition     *CursorPosition
}

// NewSelect creates a new Select instance.
func NewSelect(options *SelectOptions) *Select {
	mergedOptions := SelectOptions{}
	if err := mergo.Merge(&mergedOptions, options, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedOptions, DefaultSelectOptions, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	selectOptions := make([]*indexedSelectChoice, len(mergedOptions.Choices))
	for index, value := range mergedOptions.Choices {
		selectOptions[index] = &indexedSelectChoice{
			Index:        index,
			SelectChoice: value,
		}
	}

	// Define default hint message
	if mergedOptions.Hint == "" {
		hintParts := []string{"Use arrows to move"}
		if *mergedOptions.EnableFiltering {
			hintParts = append(hintParts, "type to filter")
		}

		mergedOptions.Hint = fmt.Sprintf("[%s]", strings.Join(hintParts, ", "))
	}

	return &Select{
		input:           internal.NewInput(),
		cursor:          internal.NewCursor(mergedOptions.Writer),
		options:         &mergedOptions,
		filteredChoices: selectOptions,
		choices:         selectOptions,
	}
}

// WithCanvas sets the canvas for the select component.
func (p *Select) WithCanvas(canvas Canvas) Visual {
	p.canvas = canvas
	return p
}

// Ask prompts the user to select an option from a list.
func (p *Select) Ask() (*int, error) {
	if p.canvas == nil {
		p.canvas = NewCanvas(p).WithWriter(p.options.Writer)
	}

	if err := p.canvas.Run(); err != nil {
		return nil, err
	}

	input, done, err := p.input.ReadInput(nil)
	if err != nil {
		return nil, err
	}

	if !*p.options.EnableFiltering {
		p.cursor.HideCursor()
	}

	for {
		select {
		case <-p.input.SigChan:
			p.cancelled = true
			done()
			if err := p.canvas.Update(); err != nil {
				return nil, err
			}

			return nil, ErrCancelled

		case msg := <-input:
			p.showHelp = msg.Hint

			if *p.options.EnableFiltering {
				p.filter = msg.Value
			}

			optionCount := len(p.filteredChoices)
			if optionCount > 0 {
				if msg.Key == keyboard.KeyArrowUp {
					p.currentIndex = Ptr(((*p.currentIndex - 1 + optionCount) % optionCount))
				} else if msg.Key == keyboard.KeyArrowDown {
					p.currentIndex = Ptr(((*p.currentIndex + 1) % optionCount))
				}

				p.selectedChoice = p.filteredChoices[*p.currentIndex]
			}

			if msg.Key == keyboard.KeyEnter && p.currentIndex != nil {
				p.complete = true
			}

			if err := p.canvas.Update(); err != nil {
				done()
				return nil, err
			}

			if p.complete {
				done()
				return &p.selectedChoice.Index, nil
			}
		}
	}
}

func (p *Select) applyFilter() {
	// Filter options
	if p.filter == "" {
		p.filteredChoices = p.choices
	}

	if p.cancelled || p.complete || p.filter == "" {
		return
	}

	p.filteredChoices = []*indexedSelectChoice{}
	for _, option := range p.choices {
		// Attempt to parse the filter as an index
		if p.options.DisplayNumbers != nil && *p.options.DisplayNumbers {
			index, err := strconv.Atoi(p.filter)
			if err == nil {
				if index == option.Index+1 {
					p.filteredChoices = append(p.filteredChoices, option)
					continue
				}
			}
		}

		if strings.Contains(strings.ToLower(option.Value), strings.ToLower(p.filter)) {
			p.filteredChoices = append(p.filteredChoices, option)
		}
	}

	if *p.currentIndex > len(p.filteredChoices)-1 {
		p.currentIndex = Ptr(0)
	}
}

func (p *Select) renderOptions(printer Printer, indent string) {
	// Options
	if p.cancelled || p.complete {
		return
	}

	totalOptionsCount := len(p.choices)
	filteredOptionsCount := len(p.filteredChoices)
	selected := *p.currentIndex

	start := selected - p.options.DisplayCount/2
	end := start + p.options.DisplayCount

	if start < 0 {
		start = 0
		end = min(filteredOptionsCount, p.options.DisplayCount)
	} else if end > filteredOptionsCount {
		end = filteredOptionsCount
		start = max(0, filteredOptionsCount-p.options.DisplayCount)
	}

	if start > 0 {
		if start >= 9 {
			printer.Fprintf("%s  ...\n", indent)
		} else {
			printer.Fprintf("%s  ...\n", indent)
		}
	}

	digitWidth := len(fmt.Sprintf("%d", totalOptionsCount)) // Calculate the width of the digit prefix
	underline := color.New(color.Underline).SprintfFunc()

	for index, option := range p.filteredChoices[start:end] {
		displayValue := option.Label

		// Underline the matching portion of the string
		if p.filter != "" {
			matchIndex := strings.Index(strings.ToLower(displayValue), strings.ToLower(p.filter))
			if matchIndex > -1 {
				displayValue = fmt.Sprintf("%s%s%s",
					displayValue[:matchIndex],                                    // Start of the string
					underline(displayValue[matchIndex:matchIndex+len(p.filter)]), // Highlighted filter
					displayValue[matchIndex+len(p.filter):],                      // End of the string
				)
			}
		}

		// Show item digit prefixes
		digitPrefix := ""
		if *p.options.DisplayNumbers {
			digitPrefix = fmt.Sprintf("%*d. ", digitWidth, option.Index+1) // Padded digit prefix
		}

		if start+index == selected {
			prefix := ">"
			printer.Fprintf("%s%s %s%s\n",
				indent,
				color.CyanString(prefix),
				color.CyanString(digitPrefix),
				color.CyanString(displayValue),
			)
		} else {
			prefix := " "
			printer.Fprintf("%s%s %s%s\n", indent, prefix, digitPrefix, displayValue)
		}
	}

	if end < filteredOptionsCount {
		if end >= 10 {
			printer.Fprintf("%s ...\n", indent)
		} else {
			printer.Fprintf("%s  ...\n", indent)
		}
	}
}

func (p *Select) renderValidation(printer Printer) {
	p.hasValidationError = false
	p.validationMessage = ""

	if len(p.filteredChoices) == 0 {
		p.currentIndex = nil
		p.hasValidationError = true
		p.validationMessage = "No options found matching the filter"
	}

	// Validation error
	if !p.showHelp && p.hasValidationError {
		printer.Fprintln(color.YellowString(p.validationMessage))
	}

	// Hint
	if p.showHelp && p.options.HelpMessage != "" {
		printer.Fprintln()
		printer.Fprintf(
			color.HiMagentaString("%s %s\n",
				BoldString("Hint:"),
				p.options.HelpMessage,
			),
		)
	}
}

func (p *Select) renderMessage(printer Printer) {
	printer.Fprintf(color.CyanString("? "))

	if p.currentIndex == nil && p.options.SelectedIndex != nil {
		p.currentIndex = p.options.SelectedIndex
	}

	// Message
	printer.Fprintf(BoldString("%s: ", p.options.Message))

	// Cancelled
	if p.cancelled {
		printer.Fprintf(color.RedString("(Cancelled)"))
	}

	// Selected Value
	if !p.cancelled && p.selectedChoice != nil {
		rawValue := p.selectedChoice.Label
		if p.complete {
			printer.Fprintf(color.CyanString(rawValue))
		} else {
			printer.Fprintf(rawValue)
		}
	}

	printer.Fprintln()

	// Filter
	if !p.cancelled && !p.complete && *p.options.EnableFiltering {
		printer.Fprintln()
		printer.Fprintf("  Filter: ")

		if p.filter == "" {
			p.cursorPosition = Ptr(printer.CursorPosition())
			printer.Fprintf(color.HiBlackString("Type to filter list"))
		} else {
			printer.Fprintf(p.filter)
			p.cursorPosition = Ptr(printer.CursorPosition())
		}

		printer.Fprintln()
		printer.Fprintln()
	}
}

// Render renders the Select component.
func (p *Select) Render(printer Printer) error {
	indent := "  "
	p.renderMessage(printer)

	if p.complete || p.cancelled {
		return nil
	}

	p.applyFilter()
	p.renderOptions(printer, indent)
	p.renderValidation(printer)
	p.renderFooter(printer)

	if p.cursorPosition != nil {
		printer.SetCursorPosition(*p.cursorPosition)
	}

	return nil
}

func (p *Select) renderFooter(printer Printer) {
	if p.cancelled || p.complete {
		return
	}

	printer.Fprintln()
	printer.Fprintln(color.HiBlackString("───────────────────────────────────"))
	printer.Fprintln(color.HiBlackString("Use arrows to move, type ? for hint"))
}
