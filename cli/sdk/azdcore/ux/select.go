package ux

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/sdk/azdcore/ux/internal"

	"dario.cat/mergo"
	"github.com/eiannone/keyboard"
	"github.com/fatih/color"
)

type SelectConfig struct {
	// The writer to use for output (default: os.Stdout)
	Writer io.Writer
	// The reader to use for input (default: os.Stdin)
	Reader io.Reader
	// The default value to use for the prompt (default: nil)
	DefaultIndex *int
	// The message to display before the prompt
	Message string
	// The available options to display
	Allowed []string
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

var DefaultSelectConfig SelectConfig = SelectConfig{
	Writer:          os.Stdout,
	Reader:          os.Stdin,
	DefaultIndex:    Ptr(0),
	DisplayCount:    6,
	EnableFiltering: Ptr(true),
	DisplayNumbers:  Ptr(false),
}

type Select struct {
	input  *internal.Input
	cursor internal.Cursor
	canvas Canvas

	config             *SelectConfig
	selectedIndex      *int
	showHelp           bool
	complete           bool
	filter             string
	options            []*selectOption
	filteredOptions    []*selectOption
	hasValidationError bool
	validationMessage  string
	cancelled          bool
	cursorPosition     *CanvasPosition
}

type selectOption struct {
	Index int
	Value string
}

func NewSelect(config *SelectConfig) *Select {
	mergedConfig := SelectConfig{}
	if err := mergo.Merge(&mergedConfig, config, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedConfig, DefaultSelectConfig, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	selectOptions := make([]*selectOption, len(mergedConfig.Allowed))
	for index, value := range mergedConfig.Allowed {
		selectOptions[index] = &selectOption{
			Index: index,
			Value: value,
		}
	}

	// Define default hint message
	if mergedConfig.Hint == "" {
		hintParts := []string{"Use arrows to move"}
		if *mergedConfig.EnableFiltering {
			hintParts = append(hintParts, "type to filter")
		}

		mergedConfig.Hint = fmt.Sprintf("[%s]", strings.Join(hintParts, ", "))
	}

	return &Select{
		input:           internal.NewInput(),
		cursor:          internal.NewCursor(mergedConfig.Writer),
		config:          &mergedConfig,
		filteredOptions: selectOptions,
		options:         selectOptions,
	}
}

func (p *Select) WithCanvas(canvas Canvas) Visual {
	p.canvas = canvas
	return p
}

func (p *Select) Ask() (*int, error) {
	if p.canvas == nil {
		p.canvas = NewCanvas(p)
	}

	if err := p.canvas.Run(); err != nil {
		return nil, err
	}

	input, done, err := p.input.ReadInput(nil)
	if err != nil {
		return nil, err
	}

	if !*p.config.EnableFiltering {
		p.cursor.HideCursor()
	}

	for {
		select {
		case <-p.input.SigChan:
			p.cancelled = true
			done()
			p.canvas.Update()
			return nil, ErrCancelled

		case msg := <-input:
			p.showHelp = msg.Hint

			if *p.config.EnableFiltering {
				p.filter = msg.Value
			}

			optionCount := len(p.filteredOptions)
			if msg.Key == keyboard.KeyArrowUp {
				p.selectedIndex = Ptr(((*p.selectedIndex - 1 + optionCount) % optionCount))
			} else if msg.Key == keyboard.KeyArrowDown {
				p.selectedIndex = Ptr(((*p.selectedIndex + 1) % optionCount))
			}

			if msg.Key == keyboard.KeyEnter && p.selectedIndex != nil {
				p.complete = true
			}

			p.canvas.Update()

			if p.complete {
				done()
				return &p.filteredOptions[*p.selectedIndex].Index, nil
			}
		}
	}
}

func (p *Select) applyFilter() {
	// Filter options
	if p.filter == "" {
		p.filteredOptions = p.options
	}

	if p.cancelled || p.complete || p.filter == "" {
		return
	}

	p.filteredOptions = []*selectOption{}
	for _, option := range p.options {
		// Attempt to parse the filter as an index
		if p.config.DisplayNumbers != nil && *p.config.DisplayNumbers {
			index, err := strconv.Atoi(p.filter)
			if err == nil {
				if index == option.Index+1 {
					p.filteredOptions = append(p.filteredOptions, option)
					continue
				}
			}
		}

		if strings.Contains(strings.ToLower(option.Value), strings.ToLower(p.filter)) {
			p.filteredOptions = append(p.filteredOptions, option)
		}
	}

	if *p.selectedIndex > len(p.filteredOptions)-1 {
		p.selectedIndex = Ptr(0)
	}
}

func (p *Select) renderOptions(printer Printer, indent string) {
	// Options
	if p.cancelled || p.complete {
		return
	}

	totalOptionsCount := len(p.options)
	filteredOptionsCount := len(p.filteredOptions)
	selected := *p.selectedIndex

	start := selected - p.config.DisplayCount/2
	end := start + p.config.DisplayCount

	if start < 0 {
		start = 0
		end = min(filteredOptionsCount, p.config.DisplayCount)
	} else if end > filteredOptionsCount {
		end = filteredOptionsCount
		start = max(0, filteredOptionsCount-p.config.DisplayCount)
	}

	if start > 0 {
		if start >= 9 {
			printer.Fprintf("%s  ...\n", indent)
		} else {
			printer.Fprintf("%s   ...\n", indent)
		}
	}

	digitWidth := len(fmt.Sprintf("%d", totalOptionsCount)) // Calculate the width of the digit prefix
	underline := color.New(color.Underline).SprintfFunc()

	for index, option := range p.filteredOptions[start:end] {
		displayValue := option.Value

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
		if *p.config.DisplayNumbers {
			digitPrefix := fmt.Sprintf("%*d.", digitWidth, option.Index+1) // Padded digit prefix
			displayValue = fmt.Sprintf("%s %s", digitPrefix, displayValue)
		}

		if start+index == selected {
			printer.Fprintf("%s%s\n", indent, color.CyanString("> %s", displayValue))
		} else {
			printer.Fprintf("%s  %s\n", indent, displayValue)
		}
	}

	if end < filteredOptionsCount {
		if end >= 10 {
			printer.Fprintf("%s  ...\n", indent)
		} else {
			printer.Fprintf("%s   ...\n", indent)
		}
	}
}

func (p *Select) renderValidation(printer Printer) {
	p.hasValidationError = false
	p.validationMessage = ""

	if len(p.filteredOptions) == 0 {
		p.selectedIndex = nil
		p.hasValidationError = true
		p.validationMessage = "No options found matching the filter"
	}

	// Validation error
	if !p.showHelp && p.hasValidationError {
		printer.Fprintln(color.YellowString(p.validationMessage))
	}

	// Hint
	if p.showHelp && p.config.HelpMessage != "" {
		printer.Fprintln()
		printer.Fprintf(
			color.HiMagentaString("%s %s\n",
				BoldString("Hint:"),
				p.config.HelpMessage,
			),
		)
	}
}

func (p *Select) renderMessage1(printer Printer) {
	if p.selectedIndex == nil && p.config.DefaultIndex != nil {
		p.selectedIndex = p.config.DefaultIndex
	}

	printer.Fprintf(color.CyanString("? "))

	// Message
	printer.Fprintf(BoldString("%s: ", p.config.Message))

	// Hint
	if !p.cancelled && !p.complete && p.config.Hint != "" {
		printer.Fprintf("%s ", color.CyanString(p.config.Hint))
	}

	// Filter
	if !p.cancelled && !p.complete && p.filter != "" {
		printer.Fprintf(p.filter)
	}

	p.cursorPosition = Ptr(p.canvas.CursorPosition())

	// Cancelled
	if p.cancelled {
		printer.Fprintf(color.RedString("(Cancelled)"))
	}

	// Selected Value
	if !p.cancelled && p.complete {
		rawValue := p.filteredOptions[*p.selectedIndex].Value
		printer.Fprintf(color.CyanString(rawValue))
	}

	printer.Fprintln()
}

func (p *Select) renderMessage2(printer Printer) {
	printer.Fprintf(color.CyanString("? "))

	if p.selectedIndex == nil && p.config.DefaultIndex != nil {
		p.selectedIndex = p.config.DefaultIndex
	}

	// Message
	printer.Fprintf(BoldString("%s: ", p.config.Message))

	// Cancelled
	if p.cancelled {
		printer.Fprintf(color.RedString("(Cancelled)"))
	}

	// Selected Value
	if !p.cancelled && p.complete {
		rawValue := p.filteredOptions[*p.selectedIndex].Value
		printer.Fprintf(color.CyanString(rawValue))
	}

	printer.Fprintln()

	// Filter
	if !p.cancelled && !p.complete && *p.config.EnableFiltering {
		printer.Fprintln()
		printer.Fprintf("  Filter: ")

		if p.filter == "" {
			p.cursorPosition = Ptr(p.canvas.CursorPosition())
			printer.Fprintf(color.HiBlackString("Type to filter list"))
		} else {
			printer.Fprintf(p.filter)
			p.cursorPosition = Ptr(p.canvas.CursorPosition())
		}

		printer.Fprintln()
		printer.Fprintln()
	}
}

func (p *Select) Render(printer Printer) error {
	v2 := true

	if slices.Contains(os.Args[1:], "--selectv2") {
		v2 = true
	}

	indent := ""

	if v2 {
		p.renderMessage2(printer)
		indent = "  "
	} else {
		p.renderMessage1(printer)
	}

	if p.complete || p.cancelled {
		return nil
	}

	p.applyFilter()
	p.renderOptions(printer, indent)
	p.renderValidation(printer)

	if v2 {
		p.renderFooter(printer)
	}

	if p.cursorPosition != nil {
		p.canvas.SetCursorPosition(*p.cursorPosition)
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
