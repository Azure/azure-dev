package ux

import (
	"io"
	"os"

	"github.com/azure/azure-dev/cli/sdk/azdcore/ux/internal"

	"dario.cat/mergo"
	"github.com/eiannone/keyboard"
	"github.com/fatih/color"
)

type PromptConfig struct {
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
}

var DefaultPromptConfig PromptConfig = PromptConfig{
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

type Prompt struct {
	input *internal.Input

	canvas             Canvas
	config             *PromptConfig
	hasValidationError bool
	value              string
	showHelp           bool
	complete           bool
	submitted          bool
	validationMessage  string
	cancelled          bool
	cursorPosition     *CanvasPosition
}

func NewPrompt(config *PromptConfig) *Prompt {
	mergedConfig := PromptConfig{}
	if err := mergo.Merge(&mergedConfig, DefaultPromptConfig, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedConfig, config, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	return &Prompt{
		input:  internal.NewInput(),
		config: &mergedConfig,
		value:  mergedConfig.DefaultValue,
	}
}

func (p *Prompt) validate() {
	p.hasValidationError = false
	p.validationMessage = p.config.ValidationMessage

	if p.config.Required && p.value == "" {
		p.hasValidationError = true
		p.validationMessage = p.config.RequiredMessage
		return
	}

	if p.config.ValidationFn != nil {
		ok, msg := p.config.ValidationFn(p.value)
		if !ok {
			p.hasValidationError = true
			if msg != "" {
				p.validationMessage = msg
			}
		}
	}
}

func (p *Prompt) WithCanvas(canvas Canvas) Visual {
	p.canvas = canvas
	return p
}

func (p *Prompt) Ask() (string, error) {
	if p.canvas == nil {
		p.canvas = NewCanvas(p)
	}

	if err := p.canvas.Run(); err != nil {
		return "", err
	}

	inputOptions := &internal.InputConfig{
		InitialValue:   p.config.DefaultValue,
		IgnoreHintKeys: p.config.IgnoreHintKeys,
	}
	input, done, err := p.input.ReadInput(inputOptions)
	if err != nil {
		return "", err
	}

	for {
		select {
		case <-p.input.SigChan:
			p.cancelled = true
			done()
			p.canvas.Update()
			return "", ErrCancelled

		case msg := <-input:
			p.showHelp = msg.Hint
			p.value = msg.Value

			p.validate()

			if msg.Key == keyboard.KeyEnter {
				p.submitted = true

				if !p.hasValidationError {
					p.complete = true
				}
			}

			p.canvas.Update()

			if p.complete {
				done()
				return p.value, nil
			}
		}
	}
}

func (p *Prompt) Render(printer Printer) error {
	if p.config.ClearOnCompletion && p.complete {
		return nil
	}

	printer.Fprintf(color.CyanString("? "))

	// Message
	printer.Fprintf(BoldString("%s: ", p.config.Message))

	// Hint (Only show when a help message has been defined)
	if !p.cancelled && !p.complete && p.config.Hint != "" && p.config.HelpMessage != "" {
		printer.Fprintf("%s ", color.CyanString(p.config.Hint))
	}

	// Placeholder
	if !p.cancelled && p.value == "" && p.config.PlaceHolder != "" {
		p.cursorPosition = Ptr(p.canvas.CursorPosition())
		printer.Fprintf(color.HiBlackString(p.config.PlaceHolder))
	}

	// Value
	if !p.cancelled && p.value != "" {
		valueOutput := p.value

		if p.complete || p.value == p.config.DefaultValue {
			valueOutput = color.CyanString(p.value)
		}

		printer.Fprintf(valueOutput)
		p.cursorPosition = Ptr(p.canvas.CursorPosition())
	}

	if p.cancelled {
		printer.Fprintf(color.HiRedString("(Cancelled)"))
	}

	// We write a new line to ensure anything else written to the terminal is on the next line
	// Cursor position for user input is handled further below
	printer.Fprintln()

	if p.complete || p.cancelled {
		return nil
	}

	// Validation error
	if !p.showHelp && p.submitted && p.hasValidationError {
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

	if !p.complete && p.cursorPosition != nil {
		p.canvas.SetCursorPosition(*p.cursorPosition)
	}

	return nil
}
