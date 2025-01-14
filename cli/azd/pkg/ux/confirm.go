package ux

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"

	"dario.cat/mergo"
	"github.com/eiannone/keyboard"
	"github.com/fatih/color"
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
func (p *Confirm) Ask() (*bool, error) {
	if p.canvas == nil {
		p.canvas = NewCanvas(p).WithWriter(p.options.Writer)
	}

	if err := p.canvas.Run(); err != nil {
		return nil, err
	}

	inputConfig := &internal.InputConfig{
		InitialValue:   p.displayValue,
		IgnoreHintKeys: true,
	}
	input, done, err := p.input.ReadInput(inputConfig)
	if err != nil {
		return nil, err
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

			if msg.Key == keyboard.KeyEnter {
				p.submitted = true

				if !p.hasValidationError {
					p.complete = true
				}
			} else {
				p.hasValidationError = false
				if msg.Value == "" && p.options.DefaultValue != nil {
					p.value = p.options.DefaultValue
					p.displayValue = getBooleanString(*p.value)
				} else {
					value, err := parseBooleanString(string(msg.Char))
					if err != nil {
						p.hasValidationError = true
						p.value = nil
						p.displayValue = msg.Value
					} else {
						p.value = value
						p.displayValue = getBooleanString(*value)
					}
				}
			}

			if !p.hasValidationError {
				p.input.ResetValue()
			}

			if err := p.canvas.Update(); err != nil {
				done()
				return nil, err
			}

			if p.complete {
				done()
				return p.value, nil
			}
		}
	}
}

// Render renders the Confirm component.
func (p *Confirm) Render(printer Printer) error {
	printer.Fprintf(color.CyanString("? "))

	// Message
	printer.Fprintf(BoldString("%s: ", p.options.Message))

	// Hint
	if !p.cancelled && !p.complete && p.options.Hint != "" {
		printer.Fprintf("%s ", color.CyanString(p.options.Hint))
	}

	// Value
	rawStringValue := p.displayValue
	valueOutput := rawStringValue

	if p.complete || p.value == p.options.DefaultValue {
		valueOutput = color.CyanString(rawStringValue)
	}

	if p.cancelled {
		valueOutput = color.RedString("(Cancelled)")
	}

	printer.Fprintf(valueOutput)
	p.cursorPosition = Ptr(printer.CursorPosition())

	printer.Fprintln()

	if p.complete || p.cancelled {
		return nil
	}

	// Validation error
	if !p.showHelp && p.hasValidationError {
		printer.Fprintln(color.YellowString("Enter a valid value"))
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
