// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/mattn/go-colorable"
	"github.com/theckman/yacspin"
)

type MessageUxType int
type SpinnerUxType int

const (
	ResultSuccess MessageUxType = iota
	ResultError
)

const (
	Step SpinnerUxType = iota
	StepDone
	StepFailed
)

// A shim to allow a single Console construction in the application.
// To be removed once formatter and Console's responsibilities are reconciled
type ConsoleShim interface {
	// True if the console was instantiated with no format options.
	IsUnformatted() bool

	// Gets the underlying formatter used by the console
	GetFormatter() output.Formatter
}

type Console interface {
	// Prints out a message to the underlying console write
	Message(ctx context.Context, message string)
	// Prints out a message following the UX format type
	MessageUx(ctx context.Context, message string, format MessageUxType)
	// Prints out a message following a contract ux item
	MessageUxItem(ctx context.Context, item ux.UXItem)
	// Prints progress spinner with the given title.
	// If a previous spinner is running, the title is updated.
	ShowSpinner(ctx context.Context, title string, format SpinnerUxType)
	// Stop the current spinner from the console and change the spinner bar for the lastMessage
	// Set lastMessage to empty string to clear the spinner message instead of a displaying a last message
	// If there is no spinner running, this is a no-op function
	StopSpinner(ctx context.Context, lastMessage string, format SpinnerUxType)
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
		jsonMessage, err := json.Marshal(c.eventForMessage(message))
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

func (c *AskerConsole) MessageUxItem(ctx context.Context, item ux.UXItem) {
	if c.formatter == nil {
		log.Println(item.ToString(c.currentIndent))
		return
	}

	if c.formatter.Kind() == output.JsonFormat {
		fmt.Fprintln(c.writer, string(item.ToJson()))
		return
	}

	if c.formatter.Kind() == output.EnvVarsFormat {
		fmt.Fprintln(c.writer, item.ToTable())
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

func (c *AskerConsole) MessageUx(ctx context.Context, message string, format MessageUxType) {
	formattedText, err := c.addFormat(message, format)
	// Message and MessageUx don't return errors. Let's log the error and use the original message on error
	if err != nil {
		log.Printf("Failed adding format for MessageUx: %s. Using message with no ux format", err.Error())
		c.Message(ctx, message)
		return
	}

	// backwards compatibility to error messages
	// Remove any formatter before printing the Result
	// This is can be changed in the future if we want to format any error message as Json or Table when user set output.
	if format == ResultError {
		c.formatter = &output.NoneFormatter{}
	}

	if c.spinner != nil && c.spinner.Status() == yacspin.SpinnerRunning {
		c.StopSpinner(ctx, "", Step)
		c.Message(ctx, formattedText)
		_ = c.spinner.Start()
	} else {
		c.Message(ctx, formattedText)
	}
}

func withIndentation(message, indentation string) string {
	if indentation == "" {
		return message
	}
	// Add indentation to each line in the string
	lines := strings.Split(message, "\n")
	updatedLines := make([]string, len(lines))
	for i, line := range lines {
		if len(line) > 0 {
			updatedLines[i] = indentation + line
		}
	}
	return strings.Join(updatedLines, "\n")
}

func (c *AskerConsole) addFormat(message string, format MessageUxType) (withFormat string, err error) {
	switch format {
	case ResultSuccess:
		withFormat = output.WithSuccessFormat("\n%s: %s", "SUCCESS", message)
	case ResultError:
		withFormat = output.WithErrorFormat("\n%s: %s", "ERROR", message)
	default:
		return withFormat, fmt.Errorf("Unknown UX format type")
	}

	return withFormat, nil
}

func (c *AskerConsole) ShowSpinner(ctx context.Context, title string, format SpinnerUxType) {
	// make sure spinner exists
	if c.spinner == nil {
		c.spinner, _ = yacspin.New(yacspin.Config{
			Frequency:       200 * time.Millisecond,
			Writer:          c.writer,
			Suffix:          " ",
			SuffixAutoColon: true,
		})
	}
	// If running, pause to apply style changes
	if c.spinner.Status() == yacspin.SpinnerRunning {
		_ = c.spinner.Pause()
	}

	// Update style according to MessageUxType
	c.spinner.Message(title)
	_ = c.spinner.CharSet(c.getCharset(format))

	// unpause if Paused
	if c.spinner.Status() == yacspin.SpinnerPaused {
		_ = c.spinner.Unpause()
	} else if c.spinner.Status() == yacspin.SpinnerStopped {
		_ = c.spinner.Start()
	}
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
	var requiredSize int
	switch format {
	case Step:
		requiredSize = 2
	case StepDone:
		requiredSize = 2
	case StepFailed:
		requiredSize = 2
	}
	if requiredSize != len(c.currentIndent) {
		c.currentIndent = setIndentation(requiredSize)
	}
	return c.currentIndent
}

func (c *AskerConsole) StopSpinner(ctx context.Context, lastMessage string, format SpinnerUxType) {
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

var donePrefix string = output.WithSuccessFormat("(✓) Done:")

func (c *AskerConsole) getStopChar(format SpinnerUxType) string {
	var stopChar string
	switch format {
	case StepDone:
		stopChar = donePrefix
	case StepFailed:
		stopChar = output.WithErrorFormat("(x) Failed:")
	}
	return fmt.Sprintf("%s%s", c.getIndent(format), stopChar)
}

// jsonObjectForMessage creates a json object representing a message. Any ANSI control sequences from the message are
// removed. A trailing newline is added to the message.
func (c *AskerConsole) eventForMessage(message string) contracts.EventEnvelope {
	// Strip any ANSI colors for the message.
	var buf bytes.Buffer

	// We do not expect the io.Copy to fail since none of these sub-calls will ever return an error (other than
	// EOF when we hit the end of the string)
	if _, err := io.Copy(colorable.NewNonColorable(&buf), strings.NewReader(message)); err != nil {
		panic(fmt.Sprintf("consoleMessageForMessage: did not expect error from io.Copy but got: %v", err))
	}

	// Add the newline that would have been added by fmt.Println when we wrote the message directly to the console.
	buf.WriteByte('\n')

	return newConsoleMessageEvent(buf.String())
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
	}

	var response string

	if err := c.asker(survey, &response); err != nil {
		return "", err
	}

	return response, nil
}

// Prompts the user to select from a set of values
func (c *AskerConsole) Select(ctx context.Context, options ConsoleOptions) (int, error) {
	survey := &survey.Select{
		Message: options.Message,
		Options: options.Options,
		Default: options.DefaultValue,
	}

	var response int

	if err := c.asker(survey, &response); err != nil {
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
		Default: defaultValue,
	}

	var response bool

	if err := c.asker(survey, &response); err != nil {
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

type contextKey string

const (
	consoleContextKey contextKey = "console"
)

// Sets the console instance in the go context and returns the new context
func WithConsole(ctx context.Context, console Console) context.Context {
	return context.WithValue(ctx, consoleContextKey, console)
}

// Gets the console from the go context or nil if not found
func GetConsole(ctx context.Context) Console {
	console, ok := ctx.Value(consoleContextKey).(Console)
	if !ok {
		return nil
	}

	return console
}

func newConsoleMessageEvent(msg string) contracts.EventEnvelope {
	return contracts.EventEnvelope{
		Type:      contracts.ConsoleMessageEventDataType,
		Timestamp: time.Now(),
		Data: contracts.ConsoleMessage{
			Message: msg,
		},
	}
}

func GetStepResultFormat(result error) SpinnerUxType {
	formatResult := StepDone
	if result != nil {
		formatResult = StepFailed
	}
	return formatResult
}
