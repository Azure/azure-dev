// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"dario.cat/mergo"
	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"
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
func (p *Select) Ask(ctx context.Context) (*int, error) {
	if p.canvas == nil {
		p.canvas = NewCanvas(p).WithWriter(p.options.Writer)
	}

	release := cm.Focus(p.canvas)
	defer func() {
		release()
		p.canvas.Close()
	}()

	if !*p.options.EnableFiltering {
		p.cursor.HideCursor()
	}

	defer func() {
		p.cursor.ShowCursor()
	}()

	if err := p.canvas.Run(); err != nil {
		return nil, err
	}

	done := func() {
		if err := p.canvas.Update(); err != nil {
			log.Printf("Error updating canvas: %v\n", err)
		}
	}

	err := p.input.ReadInput(ctx, nil, func(args *internal.KeyPressEventArgs) (bool, error) {
		defer done()

		if args.Cancelled {
			p.cancelled = true
			return false, nil
		}

		p.showHelp = args.Hint

		if *p.options.EnableFiltering {
			p.filter = args.Value
		}

		optionCount := len(p.filteredChoices)
		if optionCount > 0 {
			if args.Key == surveyterm.KeyArrowUp {
				p.currentIndex = Ptr(((*p.currentIndex - 1 + optionCount) % optionCount))
			} else if args.Key == surveyterm.KeyArrowDown {
				p.currentIndex = Ptr(((*p.currentIndex + 1) % optionCount))
			}

			p.selectedChoice = p.filteredChoices[*p.currentIndex]
		}

		if args.Key == surveyterm.KeyEnter && p.currentIndex != nil {
			p.complete = true
		}

		if p.complete {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return nil, err
	}

	return &p.selectedChoice.Index, nil
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

		containsValue := strings.Contains(strings.ToLower(option.Value), strings.ToLower(p.filter))
		containsLabel := strings.Contains(strings.ToLower(option.Label), strings.ToLower(p.filter))

		if containsValue || containsLabel {
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
				output.WithHighLightFormat(prefix),
				output.WithHighLightFormat(digitPrefix),
				output.WithHighLightFormat(displayValue),
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
		printer.Fprintln(output.WithWarningFormat("  %s", p.validationMessage))
	}

	// Hint
	if p.showHelp && p.options.HelpMessage != "" {
		printer.Fprintln()
		printer.Fprintf(
			"%s %s\n",
			output.WithHintFormat(BoldString("  Hint:")),
			output.WithHintFormat(p.options.HelpMessage),
		)
	}
}

func (p *Select) renderMessage(printer Printer) {
	printer.Fprintf(output.WithHighLightFormat("? "))

	// Message
	printer.Fprintf(BoldString("%s: ", p.options.Message))

	// Cancelled
	if p.cancelled {
		printer.Fprintf(output.WithErrorFormat("(Cancelled)"))
	}

	// Selected Value
	if !p.cancelled && p.selectedChoice != nil {
		rawValue := p.selectedChoice.Label
		printer.Fprintf(output.WithHighLightFormat(rawValue))
	}

	printer.Fprintln()

	// Filter
	if !p.cancelled && !p.complete && *p.options.EnableFiltering {
		printer.Fprintln()
		printer.Fprintf("  Filter: ")

		if p.filter == "" {
			p.cursorPosition = Ptr(printer.CursorPosition())
			printer.Fprintf(output.WithGrayFormat("Type to filter list"))
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
	if p.currentIndex == nil && p.options.SelectedIndex != nil {
		p.currentIndex = p.options.SelectedIndex
		p.selectedChoice = p.choices[*p.currentIndex]
	}

	p.renderMessage(printer)

	if p.complete || p.cancelled {
		return nil
	}

	p.applyFilter()
	p.renderOptions(printer, "  ")
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
	printer.Fprintln(output.WithGrayFormat("───────────────────────────────────"))
	printer.Fprintln(output.WithGrayFormat("Use arrows to move, type ? for hint"))
}
