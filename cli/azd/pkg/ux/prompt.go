// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"

	"dario.cat/mergo"
	"github.com/eiannone/keyboard"
)

// PromptOptions represents the options for the Prompt component.
type PromptOptions struct {
	// The writer to use for output (default: os.Stdout)
	Writer io.Writer
	// The reader to use for input (default: os.Stdin)
	Reader io.Reader
	// The default value to use for the prompt (default: "")
	DefaultValue string
	// The message to display before the prompt
	Message string
	// The optional message to display when the user types ? (default: "")
	HelpMessage string
	// The optional hint text that display after the message (default: "[Type ? for hint]")
	Hint string
	// The optional placeholder text to display when the value is empty (default: "")
	PlaceHolder string
	// The optional validation function to use
	ValidationFn func(string) (bool, string)
	// The optional validation message to display when validation fails (default: "Invalid input")
	ValidationMessage string
	// The optional validation message to display when the value is empty and required (default: "This field is required")
	RequiredMessage string
	// Whether or not the prompt is required (default: false)
	Required bool
	// Whether or not to clear the prompt after completion (default: false)
	ClearOnCompletion bool
	// Whether or not to capture hint keys (default: true)
	IgnoreHintKeys bool
	// The optional help message that displays on the next line (default: "")
	HelpMessageOnNextLine string
}

var DefaultPromptOptions PromptOptions = PromptOptions{
	Writer:            os.Stdout,
	Reader:            os.Stdin,
	Required:          false,
	ValidationMessage: "Invalid input",
	RequiredMessage:   "This field is required",
	Hint:              "[Type ? for hint]",
	ClearOnCompletion: false,
	IgnoreHintKeys:    false,
	ValidationFn: func(input string) (bool, string) {
		return true, ""
	},
}

// Prompt is a component for prompting the user for input.
type Prompt struct {
	input *internal.Input

	canvas             Canvas
	options            *PromptOptions
	hasValidationError bool
	value              string
	showHelp           bool
	complete           bool
	submitted          bool
	validationMessage  string
	cancelled          bool
	cursorPosition     *CursorPosition
}

// NewPrompt creates a new Prompt instance.
func NewPrompt(options *PromptOptions) *Prompt {
	mergedOptions := PromptOptions{}
	if err := mergo.Merge(&mergedOptions, options, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedOptions, DefaultPromptOptions, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	return &Prompt{
		input:   internal.NewInput(),
		options: &mergedOptions,
		value:   mergedOptions.DefaultValue,
	}
}

func (p *Prompt) validate() {
	p.hasValidationError = false
	p.validationMessage = p.options.ValidationMessage

	if p.options.Required && p.value == "" {
		p.hasValidationError = true
		p.validationMessage = p.options.RequiredMessage
		return
	}

	if p.options.ValidationFn != nil {
		ok, msg := p.options.ValidationFn(p.value)
		if !ok {
			p.hasValidationError = true
			if msg != "" {
				p.validationMessage = msg
			}
		}
	}
}

// WithCanvas sets the canvas for the prompt.
func (p *Prompt) WithCanvas(canvas Canvas) Visual {
	p.canvas = canvas
	return p
}

// Ask prompts the user for input.
func (p *Prompt) Ask(ctx context.Context) (string, error) {
	if p.canvas == nil {
		p.canvas = NewCanvas(p).WithWriter(p.options.Writer)
	}

	release := cm.Focus(p.canvas)
	defer func() {
		release()
		p.canvas.Close()
	}()

	inputOptions := &internal.InputConfig{
		InitialValue:   p.options.DefaultValue,
		IgnoreHintKeys: p.options.IgnoreHintKeys,
	}

	if err := p.canvas.Run(); err != nil {
		return "", err
	}

	done := func() {
		if err := p.canvas.Update(); err != nil {
			log.Printf("Error updating canvas: %v\n", err)
		}
	}

	err := p.input.ReadInput(ctx, inputOptions, func(args *internal.KeyPressEventArgs) (bool, error) {
		defer done()

		if args.Cancelled {
			p.cancelled = true
			return false, nil
		}

		p.showHelp = args.Hint
		p.value = args.Value
		p.validate()

		if args.Key == keyboard.KeyEnter {
			p.submitted = true

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
		return "", err
	}

	return p.value, nil
}

// Render renders the prompt.
func (p *Prompt) Render(printer Printer) error {
	if p.options.ClearOnCompletion && p.complete {
		return nil
	}

	printer.Fprintf(output.WithHighLightFormat("? "))

	// Message
	printer.Fprintf(BoldString("%s: ", p.options.Message))

	// Cancelled
	if p.cancelled {
		printer.Fprintln(output.WithErrorFormat("(Cancelled)"))
		return nil
	}

	// Hint (Only show when a help message has been defined)
	if !p.complete && p.options.Hint != "" && p.options.HelpMessage != "" {
		printer.Fprintf("%s ", output.WithHighLightFormat(p.options.Hint))
	}

	// Always capture cursor position for input, used for SecondLineMessage
	if p.cursorPosition == nil {
		p.cursorPosition = Ptr(printer.CursorPosition())
	}

	// Placeholder
	if p.value == "" && p.options.PlaceHolder != "" {
		p.cursorPosition = Ptr(printer.CursorPosition())
		printer.Fprintf(output.WithGrayFormat(p.options.PlaceHolder))
	}

	// Value
	if p.value != "" {
		valueOutput := p.value

		if p.complete || p.value == p.options.DefaultValue {
			valueOutput = output.WithHighLightFormat(p.value)
		}

		printer.Fprintf(valueOutput)
		p.cursorPosition = Ptr(printer.CursorPosition())
	}

	// Display SecondLineMessage on next line in gray
	if !p.complete && p.options.HelpMessageOnNextLine != "" {
		printer.Fprintf("\n%s", output.WithGrayFormat(p.options.HelpMessageOnNextLine))
		// Reset cursor to the input position after showing the message
		if p.cursorPosition != nil {
			printer.SetCursorPosition(*p.cursorPosition)
		}
	}

	// Done
	if p.complete {
		printer.Fprintln()
		return nil
	}

	// Validation error
	if !p.showHelp && p.submitted && p.hasValidationError {
		printer.Fprintln()
		printer.Fprintln(output.WithWarningFormat(p.validationMessage))
	}

	// Hint
	if p.showHelp && p.options.HelpMessage != "" {
		printer.Fprintln()
		printer.Fprintf(
			"%s %s\n",
			output.WithHintFormat(BoldString("Hint:")),
			output.WithHintFormat(p.options.HelpMessage),
		)
	}

	// Only need to reset the cursor position when we are showing a message
	if p.cursorPosition != nil {
		printer.SetCursorPosition(*p.cursorPosition)
	}

	return nil
}
