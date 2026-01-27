// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"

	"dario.cat/mergo"
	"github.com/eiannone/keyboard"
)

// ConfirmOptions represents the options for the Confirm component.
type ConfirmOptions struct {
	// The writer to use for output (default: os.Stdout)
	Writer io.Writer
	// The reader to use for input (default: os.Stdin)
	Reader io.Reader
	// The default value to use for the prompt (default: nil)
	DefaultValue *bool
	// The message to display before the prompt
	Message string
	// The optional message to display when the user types ? (default: "")
	HelpMessage string
	// The optional hint text that display after the message (default: "[Type ? for hint]")
	Hint string
	// The optional placeholder text to display when the value is empty (default: "")
	PlaceHolder string
}

var DefaultConfirmOptions ConfirmOptions = ConfirmOptions{
	Writer: os.Stdout,
	Reader: os.Stdin,
}

// Confirm is a component for prompting the user to confirm a message.
type Confirm struct {
	canvas Canvas
	input  *internal.Input

	options            *ConfirmOptions
	hasValidationError bool
	value              *bool
	showHelp           bool
	complete           bool
	submitted          bool
	displayValue       string
	cancelled          bool
	cursorPosition     *CursorPosition
}

// NewConfirm creates a new Confirm instance.
func NewConfirm(options *ConfirmOptions) *Confirm {
	mergedOptions := ConfirmOptions{}
	if err := mergo.Merge(&mergedOptions, options, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedOptions, DefaultConfirmOptions, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	var displayValue string
	if mergedOptions.DefaultValue != nil {
		displayValue = getBooleanString(*mergedOptions.DefaultValue)
	}

	if mergedOptions.Hint == "" {
		defaultHintText := "[y/n]"

		if mergedOptions.DefaultValue != nil {
			yesValue := "y"
			if *mergedOptions.DefaultValue {
				yesValue = "Y"
			}

			noValue := "n"
			if !*mergedOptions.DefaultValue {
				noValue = "N"
			}

			defaultHintText = fmt.Sprintf("[%s/%s]", yesValue, noValue)
		}

		mergedOptions.Hint = defaultHintText
	}

	return &Confirm{
		input:        internal.NewInput(),
		options:      &mergedOptions,
		displayValue: displayValue,
		value:        mergedOptions.DefaultValue,
	}
}

// WithCanvas sets the canvas for the Confirm component.
func (p *Confirm) WithCanvas(canvas Canvas) Visual {
	p.canvas = canvas
	return p
}

// Ask prompts the user to confirm a message.
func (p *Confirm) Ask(ctx context.Context) (*bool, error) {
	// Auto-apply prompt timeout if configured globally
	var timeout time.Duration
	if timeout = GetPromptTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if p.canvas == nil {
		p.canvas = NewCanvas(p).WithWriter(p.options.Writer)
	}

	release := cm.Focus(p.canvas)
	defer func() {
		release()
		p.canvas.Close()
	}()

	inputConfig := &internal.InputConfig{
		InitialValue: p.displayValue,
	}

	if err := p.canvas.Run(); err != nil {
		return nil, err
	}

	done := func() {
		if err := p.canvas.Update(); err != nil {
			log.Printf("Error updating canvas: %v\n", err)
		}
	}

	err := p.input.ReadInput(ctx, inputConfig, func(args *internal.KeyPressEventArgs) (bool, error) {
		defer done()

		if args.Cancelled {
			p.cancelled = true
			return false, nil
		}

		p.showHelp = args.Hint

		if args.Key == keyboard.KeyEnter {
			p.submitted = true

			if !p.hasValidationError {
				p.complete = true
			}
		} else if !p.showHelp {
			p.hasValidationError = false
			if args.Value == "" && p.options.DefaultValue != nil {
				p.value = p.options.DefaultValue
				p.displayValue = getBooleanString(*p.value)
			} else {
				value, err := parseBooleanString(string(args.Char))
				if err != nil {
					p.hasValidationError = true
					p.value = nil
					p.displayValue = ""
				} else {
					p.value = value
					p.displayValue = getBooleanString(*value)
				}
			}
		}

		if !p.hasValidationError {
			p.input.ResetValue()
		}

		if p.complete {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		// Convert context deadline to prompt timeout error
		if timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
			return nil, &ErrPromptTimeout{Duration: timeout}
		}
		return nil, err
	}

	return p.value, nil
}

// Render renders the Confirm component.
func (p *Confirm) Render(printer Printer) error {
	printer.Fprintf(output.WithHighLightFormat("? "))

	// Message
	printer.Fprintf(BoldString("%s: ", p.options.Message))

	// Hint
	if !p.cancelled && !p.complete && p.options.Hint != "" {
		printer.Fprintf("%s ", output.WithHighLightFormat(p.options.Hint))
	}

	// Value
	rawStringValue := p.displayValue
	valueOutput := rawStringValue

	if p.complete || p.value == p.options.DefaultValue {
		valueOutput = output.WithHighLightFormat(rawStringValue)
	}

	if p.cancelled {
		valueOutput = output.WithErrorFormat("(Cancelled)")
	}

	printer.Fprintf(valueOutput)
	p.cursorPosition = Ptr(printer.CursorPosition())

	printer.Fprintln()

	if p.complete || p.cancelled {
		return nil
	}

	// Validation error
	if p.hasValidationError {
		printer.Fprintln(output.WithWarningFormat("Enter a valid value"))
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

	if p.cursorPosition != nil {
		printer.SetCursorPosition(*p.cursorPosition)
	}

	return nil
}

func getBooleanString(value bool) string {
	if value {
		return "Yes"
	}

	return "No"
}

func parseBooleanString(value string) (*bool, error) {
	yesValues := []string{"y", "yes", "true", "1"}
	noValues := []string{"n", "no", "false", "0"}

	loweredValue := strings.ToLower(value)

	if slices.Contains(yesValues, loweredValue) {
		return Ptr(true), nil
	}

	if slices.Contains(noValues, loweredValue) {
		return Ptr(false), nil
	}

	return nil, fmt.Errorf("invalid boolean value")
}
