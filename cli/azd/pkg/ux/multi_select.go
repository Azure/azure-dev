// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"dario.cat/mergo"
	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"
)

// SelectOptions represents the options for the Select component.
type MultiSelectOptions struct {
	// The writer to use for output (default: os.Stdout)
	Writer io.Writer
	// The reader to use for input (default: os.Stdin)
	Reader io.Reader
	// The message to display before the prompt
	Message string
	// The available options to display
	Choices []*MultiSelectChoice
	// The optional message to display when the user types ? (default: "")
	HelpMessage string
	// The maximum number of options to display at one time (default: 6)
	DisplayCount int
	// Whether or not to display the number prefix before each option (default: false)
	DisplayNumbers *bool
	// Whether or not to disable filtering (default: true)
	EnableFiltering *bool
}

var DefaultMultiSelectOptions MultiSelectOptions = MultiSelectOptions{
	Writer:          os.Stdout,
	Reader:          os.Stdin,
	DisplayCount:    6,
	EnableFiltering: Ptr(true),
	DisplayNumbers:  Ptr(false),
}

type MultiSelectChoice struct {
	Value    string
	Label    string
	Selected bool
}

type indexedMultiSelectChoice struct {
	Index int
	*MultiSelectChoice
}

// Select is a component for prompting the user to select an option from a list.
type MultiSelect struct {
	input  *internal.Input
	cursor internal.Cursor
	canvas Canvas

	options            *MultiSelectOptions
	currentIndex       *int // The highlighted row index
	showHelp           bool
	complete           bool
	filter             string
	choices            []*indexedMultiSelectChoice
	filteredChoices    []*indexedMultiSelectChoice
	selectedChoices    map[string]*indexedMultiSelectChoice
	hasValidationError bool
	validationMessage  string
	cancelled          bool
	cursorPosition     *CursorPosition
	submitted          bool
}

// NewSelect creates a new Select instance.
func NewMultiSelect(options *MultiSelectOptions) *MultiSelect {
	mergedOptions := MultiSelectOptions{}
	if err := mergo.Merge(&mergedOptions, options, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedOptions, DefaultMultiSelectOptions, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	selectOptions := make([]*indexedMultiSelectChoice, len(mergedOptions.Choices))
	for index, value := range mergedOptions.Choices {
		selectOptions[index] = &indexedMultiSelectChoice{
			// Index is the original index from the allowed choices
			Index:             index,
			MultiSelectChoice: value,
		}
	}

	// Define default selected indexes
	initialSelectedChoices := map[string]*indexedMultiSelectChoice{}
	for index, choice := range mergedOptions.Choices {
		if choice.Selected {
			initialSelectedChoices[choice.Value] = &indexedMultiSelectChoice{
				Index:             index,
				MultiSelectChoice: choice,
			}
		}
	}

	return &MultiSelect{
		input:           internal.NewInput(),
		cursor:          internal.NewCursor(mergedOptions.Writer),
		options:         &mergedOptions,
		filteredChoices: selectOptions,
		choices:         selectOptions,
		selectedChoices: initialSelectedChoices,
	}
}

// WithCanvas sets the canvas for the select component.
func (p *MultiSelect) WithCanvas(canvas Canvas) Visual {
	p.canvas = canvas
	return p
}

// Ask prompts the user to select an option from a list.
func (p *MultiSelect) Ask(ctx context.Context) ([]*MultiSelectChoice, error) {
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
			p.filter = strings.TrimSpace(args.Value)
		}

		// Ensure currentIndex is initialized if there are any choices.
		if p.currentIndex == nil && len(p.filteredChoices) > 0 {
			p.currentIndex = Ptr(0)
		}

		optionCount := len(p.filteredChoices)
		if optionCount > 0 {
			if args.Key == surveyterm.KeyArrowUp {
				p.currentIndex = Ptr(((*p.currentIndex - 1 + optionCount) % optionCount))
			} else if args.Key == surveyterm.KeyArrowDown {
				p.currentIndex = Ptr(((*p.currentIndex + 1) % optionCount))
			} else if args.Key == surveyterm.KeySpace {
				choice := p.filteredChoices[*p.currentIndex]
				choice.Selected = !choice.Selected

				if choice.Selected {
					p.selectedChoices[choice.Value] = choice
				} else {
					delete(p.selectedChoices, choice.Value)
				}
			}
		}

		if args.Key == surveyterm.KeyArrowRight {
			for _, choice := range p.choices {
				choice.Selected = true
				p.selectedChoices[choice.Value] = choice
			}
		} else if args.Key == surveyterm.KeyArrowLeft {
			for _, choice := range p.choices {
				choice.Selected = false
				delete(p.selectedChoices, choice.Value)
			}
		}

		if args.Key == surveyterm.KeyEnter {
			p.submitted = true
			p.validate()

			if !p.hasValidationError {
				p.complete = true
			}
		}

		if p.complete {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return nil, err
	}

	return p.sortSelectedChoices(), nil
}

func (p *MultiSelect) sortSelectedChoices() []*MultiSelectChoice {
	intSelected := []*indexedMultiSelectChoice{}
	// Convert map of selected to slice
	for _, choice := range p.selectedChoices {
		intSelected = append(intSelected, choice)
	}

	// Sort the slice
	slices.SortFunc(intSelected, func(a, b *indexedMultiSelectChoice) int {
		return a.Index - b.Index
	})

	// Convert slice of selected to slice of MultiSelectChoice
	finalSelected := []*MultiSelectChoice{}
	for _, choice := range intSelected {
		finalSelected = append(finalSelected, choice.MultiSelectChoice)
	}

	return finalSelected
}

func (p *MultiSelect) applyFilter() {
	// Filter options
	if p.filter == "" {
		p.filteredChoices = p.choices
	}

	if p.cancelled || p.complete || p.filter == "" {
		return
	}

	p.filteredChoices = []*indexedMultiSelectChoice{}
	for index, option := range p.choices {
		// Attempt to parse the filter as an index
		if p.options.DisplayNumbers != nil && *p.options.DisplayNumbers {
			parsedIndex, err := strconv.Atoi(p.filter)
			if err == nil {
				if parsedIndex == index+1 {
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

func (p *MultiSelect) renderOptions(printer Printer, indent string) {
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

	for index, option := range p.filteredChoices[start:end] {
		displayValue := option.Label

		// Underline the matching portion of the string
		if p.filter != "" {
			matchIndex := strings.Index(strings.ToLower(displayValue), strings.ToLower(p.filter))
			if matchIndex > -1 {
				displayValue = fmt.Sprintf("%s%s%s",
					displayValue[:matchIndex], // Start of the string
					//nolint:govet
					output.WithUnderline("%s", displayValue[matchIndex:matchIndex+len(p.filter)]), // Highlighted filter
					displayValue[matchIndex+len(p.filter):],                                       // End of the string
				)
			}
		}

		// Show checkbox
		checkbox := " "
		if option.Selected {
			checkbox = output.WithSuccessFormat("✔")
		}

		// Show item digit prefixes
		digitPrefix := ""
		if *p.options.DisplayNumbers {
			digitPrefix = fmt.Sprintf("%*d. ", digitWidth, option.Index+1) // Padded digit prefix
		}

		prefix := " "

		if start+index == selected {
			prefix = ">"

			printer.Fprintf("%s%s %s%s%s %s%s\n",
				indent,
				output.WithHighLightFormat(prefix),
				output.WithHighLightFormat("["),
				output.WithHighLightFormat(checkbox),
				output.WithHighLightFormat("]"),
				output.WithHighLightFormat(digitPrefix),
				output.WithHighLightFormat(displayValue),
			)
		} else {
			printer.Fprintf("%s%s [%s] %s%s\n", indent, prefix, checkbox, digitPrefix, displayValue)
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

func (p *MultiSelect) validate() {
	p.hasValidationError = false
	p.validationMessage = ""

	if len(p.filteredChoices) == 0 {
		p.hasValidationError = true
		p.validationMessage = "No options found matching the filter"
	} else if p.submitted && len(p.selectedChoices) == 0 {
		p.hasValidationError = true
		p.validationMessage = "At least one option must be selected"
	}
}

func (p *MultiSelect) renderHint(printer Printer) {
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

func (p *MultiSelect) renderValidation(printer Printer) {
	// Validation error
	if !p.showHelp && p.hasValidationError {
		printer.Fprintln()
		printer.Fprintln(output.WithWarningFormat("  %s", p.validationMessage))
	}
}

func (p *MultiSelect) renderMessage(printer Printer) {
	printer.Fprintf(output.WithHighLightFormat("? "))

	// Message
	printer.Fprintf(BoldString("%s: ", p.options.Message))

	// Cancelled
	if p.cancelled {
		printer.Fprintf(output.WithErrorFormat("(Cancelled)"))
	}

	// Selected Value(s)
	if !p.cancelled {
		selectedChoices := p.sortSelectedChoices()
		selectionValues := make([]string, len(selectedChoices))
		for index, choice := range selectedChoices {
			selectionValues[index] = choice.Label
		}

		rawValue := strings.Join(selectionValues, ", ")
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
func (p *MultiSelect) Render(printer Printer) error {
	if p.currentIndex == nil {
		p.currentIndex = Ptr(0)
	}

	p.renderMessage(printer)

	if p.complete || p.cancelled {
		return nil
	}

	p.applyFilter()
	p.renderOptions(printer, "  ")

	p.validate()
	p.renderValidation(printer)
	p.renderHint(printer)
	p.renderFooter(printer)

	if p.cursorPosition != nil {
		printer.SetCursorPosition(*p.cursorPosition)
	}

	return nil
}

func (p *MultiSelect) renderFooter(printer Printer) {
	if p.cancelled || p.complete {
		return
	}

	printer.Fprintln()
	printer.Fprintln(output.WithGrayFormat("───────────────────────────────────────"))
	printer.Fprintln(output.WithGrayFormat("Use arrows to move, use space to select"))
	printer.Fprintln(output.WithGrayFormat("Use left/right to select none/all"))
	printer.Fprintln(output.WithGrayFormat("Use enter to submit, type ? for help"))
}
