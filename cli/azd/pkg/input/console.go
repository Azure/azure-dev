// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/theckman/yacspin"
)

type SpinnerUxType int

const (
	Step SpinnerUxType = iota
	StepDone
	StepFailed
	StepWarning
	StepSkipped
)

// A shim to allow a single Console construction in the application.
// To be removed once formatter and Console's responsibilities are reconciled
type ConsoleShim interface {
	// True if the console was instantiated with no format options.
	IsUnformatted() bool

	// Gets the underlying formatter used by the console
	GetFormatter() output.Formatter
}

type PromptValidator func(response string) error

type Console interface {
	// Prints out a message to the underlying console write
	Message(ctx context.Context, message string)
	// Prints out a message following a contract ux item
	MessageUxItem(ctx context.Context, item ux.UxItem)
	// Prints progress spinner with the given title.
	// If a previous spinner is running, the title is updated.
	ShowSpinner(ctx context.Context, title string, format SpinnerUxType)
	// Stop the current spinner from the console and change the spinner bar for the lastMessage
	// Set lastMessage to empty string to clear the spinner message instead of a displaying a last message
	// If there is no spinner running, this is a no-op function
	StopSpinner(ctx context.Context, lastMessage string, format SpinnerUxType)
	// Determines if there is a current spinner running.
	IsSpinnerRunning(ctx context.Context) bool
	// Prompts the user for a single value
	Prompt(ctx context.Context, options ConsoleOptions) (string, error)
	// Prompts the user to select from a set of values
	Select(ctx context.Context, options ConsoleOptions) (int, error)
	// Prompts the user to confirm an operation
	Confirm(ctx context.Context, options ConsoleOptions) (bool, error)
	// Sets the underlying writer for the console
	SetWriter(writer io.Writer)
	// Gets the underlying writer for the console
	GetWriter() io.Writer
	// Gets the standard input, output and error stream
	Handles() ConsoleHandles
	ConsoleShim
}

type AskerConsole struct {
	asker   Asker
	handles ConsoleHandles
	// the writer the console was constructed with, and what we reset to when SetWriter(nil) is called.
	defaultWriter io.Writer
	// the writer which output is written to.
	writer        io.Writer
	formatter     output.Formatter
	spinner       *yacspin.Spinner
	currentIndent string
}

type ConsoleOptions struct {
	Message      string
	Help         string
	Options      []string
	DefaultValue any
}

type ConsoleHandles struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Sets the underlying writer for output the console or
// if writer is nil, sets it back to the default writer.
func (c *AskerConsole) SetWriter(writer io.Writer) {
	if writer == nil {
		writer = c.defaultWriter
	}

	c.writer = writer
}

func (c *AskerConsole) GetFormatter() output.Formatter {
	return c.formatter
}

func (c *AskerConsole) IsUnformatted() bool {
	return c.formatter == nil || c.formatter.Kind() == output.NoneFormat
}

// Prints out a message to the underlying console write
func (c *AskerConsole) Message(ctx context.Context, message string) {
	// Disable output when formatting is enabled
	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// we call json.Marshal directly, because the formatter marshalls using indentation, and we would prefer
		// these objects be written on a single line.
		jsonMessage, err := json.Marshal(output.EventForMessage(message))
		if err != nil {
			panic(fmt.Sprintf("Message: unexpected error during marshaling for a valid object: %v", err))
		}
		fmt.Fprintln(c.writer, string(jsonMessage))
	} else if c.formatter == nil || c.formatter.Kind() == output.NoneFormat {
		fmt.Fprintln(c.writer, message)
	} else {
		log.Println(message)
	}
}

func (c *AskerConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// no need to check the spinner for json format, as the spinner won't start when using json format
		// instead, there would be a message about starting spinner
		json, _ := json.Marshal(item)
		fmt.Fprintln(c.writer, string(json))
		return
	}

	if c.spinner != nil && c.spinner.Status() == yacspin.SpinnerRunning {
		c.StopSpinner(ctx, "", Step)
		// default non-format
		fmt.Fprintln(c.writer, item.ToString(c.currentIndent))
		_ = c.spinner.Start()
	} else {
		fmt.Fprintln(c.writer, item.ToString(c.currentIndent))
	}
}

func (c *AskerConsole) ShowSpinner(ctx context.Context, title string, format SpinnerUxType) {
	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// Spinner is disabled when using json format.
		c.Message(ctx, "Show spinner with title: "+title)
		return
	}

	// mutating an existing spinner brings issues on how the messages are formatted
	// so, instead of mutating, we stop any current spinner and replaced it for a new one
	if c.spinner != nil {
		_ = c.spinner.Stop()
	}
	spinnerConfig := yacspin.Config{
		Frequency:       200 * time.Millisecond,
		Writer:          c.writer,
		Suffix:          " ",
		SuffixAutoColon: true,
		Message:         title,
		CharSet:         c.getCharset(format),
	}
	if os.Getenv("AZD_DEBUG_FORCE_NO_TTY") == "1" {
		spinnerConfig.TerminalMode = yacspin.ForceNoTTYMode | yacspin.ForceDumbTerminalMode
	}
	c.spinner, _ = yacspin.New(spinnerConfig)

	_ = c.spinner.Start()
}

var customCharSet []string = []string{
	"|       |", "|=      |", "|==     |", "|===    |", "|====   |", "|=====  |", "|====== |",
	"|=======|", "| ======|", "|  =====|", "|   ====|", "|    ===|", "|     ==|", "|      =|",
}

func (c *AskerConsole) getCharset(format SpinnerUxType) []string {
	newCharSet := make([]string, len(customCharSet))
	for i, value := range customCharSet {
		newCharSet[i] = fmt.Sprintf("%s%s", c.getIndent(format), value)
	}
	return newCharSet
}

func setIndentation(spaces int) string {
	bytes := make([]byte, spaces)
	for i := range bytes {
		bytes[i] = byte(' ')
	}
	return string(bytes)
}

func (c *AskerConsole) getIndent(format SpinnerUxType) string {
	requiredSize := 2
	if requiredSize != len(c.currentIndent) {
		c.currentIndent = setIndentation(requiredSize)
	}
	return c.currentIndent
}

func (c *AskerConsole) StopSpinner(ctx context.Context, lastMessage string, format SpinnerUxType) {
	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// Spinner is disabled when using json format.
		c.Message(ctx, "Stop spinner with title: "+lastMessage)
		return
	}

	// calling stop for non existing spinner
	if c.spinner == nil {
		return
	}
	// Do nothing when it is already stopped
	if c.spinner.Status() == yacspin.SpinnerStopped {
		return
	}

	// Update style according to MessageUxType
	if lastMessage == "" {
		c.spinner.StopCharacter("")
	} else {
		c.spinner.StopCharacter(c.getStopChar(format))
	}

	c.spinner.StopMessage(lastMessage)
	_ = c.spinner.Stop()
}

func (c *AskerConsole) IsSpinnerRunning(ctx context.Context) bool {
	return c.spinner != nil && c.spinner.Status() != yacspin.SpinnerStopped
}

var donePrefix string = output.WithSuccessFormat("(✓) Done:")

func (c *AskerConsole) getStopChar(format SpinnerUxType) string {
	var stopChar string
	switch format {
	case StepDone:
		stopChar = donePrefix
	case StepFailed:
		stopChar = output.WithErrorFormat("(x) Failed:")
	case StepWarning:
		stopChar = output.WithWarningFormat("(!) Warning:")
	case StepSkipped:
		stopChar = output.WithGrayFormat("(-) Skipped:")
	}
	return fmt.Sprintf("%s%s", c.getIndent(format), stopChar)
}

// Prompts the user for a single value
func (c *AskerConsole) Prompt(ctx context.Context, options ConsoleOptions) (string, error) {
	var defaultValue string
	if value, ok := options.DefaultValue.(string); ok {
		defaultValue = value
	}

	survey := &survey.Input{
		Message: options.Message,
		Default: defaultValue,
		Help:    options.Help,
	}

	var response string

	err := c.doInteraction(func(c *AskerConsole) error {
		return c.asker(survey, &response)
	})
	if err != nil {
		return response, err
	}

	return response, nil
}

// Prompts the user to select from a set of values
func (c *AskerConsole) Select(ctx context.Context, options ConsoleOptions) (int, error) {
	survey := &survey.Select{
		Message: options.Message,
		Options: options.Options,
		Default: options.DefaultValue,
		Help:    options.Help,
	}

	var response int

	err := c.doInteraction(func(c *AskerConsole) error {
		return c.asker(survey, &response)
	})
	if err != nil {
		return -1, err
	}

	return response, nil
}

// Prompts the user to confirm an operation
func (c *AskerConsole) Confirm(ctx context.Context, options ConsoleOptions) (bool, error) {
	var defaultValue bool
	if value, ok := options.DefaultValue.(bool); ok {
		defaultValue = value
	}

	survey := &survey.Confirm{
		Message: options.Message,
		Help:    options.Help,
		Default: defaultValue,
	}

	var response bool

	err := c.doInteraction(func(c *AskerConsole) error {
		return c.asker(survey, &response)
	})
	if err != nil {
		return false, err
	}

	return response, nil
}

// Gets the underlying writer for the console
func (c *AskerConsole) GetWriter() io.Writer {
	return c.writer
}

func (c *AskerConsole) Handles() ConsoleHandles {
	return c.handles
}

// Creates a new console with the specified writer, handles and formatter.
func NewConsole(noPrompt bool, isTerminal bool, w io.Writer, handles ConsoleHandles, formatter output.Formatter) Console {
	asker := NewAsker(noPrompt, isTerminal, handles.Stdout, handles.Stdin)

	return &AskerConsole{
		asker:         asker,
		handles:       handles,
		defaultWriter: w,
		writer:        w,
		formatter:     formatter,
	}
}

func GetStepResultFormat(result error) SpinnerUxType {
	formatResult := StepDone
	if result != nil {
		formatResult = StepFailed
	}
	return formatResult
}

// Handle doing interactive calls. It check if there's a spinner running to pause it before doing interactive actions.
func (c *AskerConsole) doInteraction(fn func(c *AskerConsole) error) error {

	if c.spinner != nil && c.spinner.Status() == yacspin.SpinnerRunning {
		_ = c.spinner.Pause()

		// calling fn might return an error. This defer make sure to recover the spinner
		// status.
		defer func() {
			_ = c.spinner.Unpause()
		}()
	}

	if err := fn(c); err != nil {
		return err
	}
	return nil
}

type ProgressStopper func()

// A messaging system that displays messages. Use this for application logic components that shouldn't be interactive
// or require any formatting, but needs simple messages to be displayed.
//
// Currently, this outputs to console.
// For ShowProgress which renders a spinner, priority is given to higher-level components
// that have a spinner already running.
type Messaging interface {
	// Prints out a message to the underlying console write
	Message(ctx context.Context, message string)
	// Displays a progress message. Returns a closer() func that stops the progress message display.
	ShowProgress(ctx context.Context, message string) ProgressStopper
}

// A messaging system that displays messages to console.
type consoleMessaging struct {
	console Console
}

func NewConsoleMessaging(console Console) Messaging {
	return &consoleMessaging{
		console: console,
	}
}

func (m *consoleMessaging) Message(ctx context.Context, message string) {
	m.console.Message(ctx, message)
}

// ShowProgress displays a spinner on console, if one isn't already running.
//
// Note that it is still possible to override existing spinners in multi-thread or multi-goroutine scenarios.
func (m *consoleMessaging) ShowProgress(ctx context.Context, message string) ProgressStopper {
	// This should be lower priority than any running console spinners
	if m.console.IsSpinnerRunning(ctx) {
		log.Println(message)
		return func() {}
	}

	m.console.ShowSpinner(ctx, message, Step)
	return func() {
		m.console.StopSpinner(ctx, "", StepDone)
	}
}
