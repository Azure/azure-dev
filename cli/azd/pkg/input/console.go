// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	tm "github.com/buger/goterm"
	"github.com/mattn/go-isatty"
	"github.com/nathan-fiscaletti/consolesize-go"
	"github.com/theckman/yacspin"
	"go.uber.org/atomic"
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

// ShowPreviewerOptions provide the settings to start a console previewer.
type ShowPreviewerOptions struct {
	Prefix       string
	MaxLineCount int
	Title        string
}

type PromptDialog struct {
	Title       string
	Description string
	Prompts     []PromptDialogItem
}

type PromptDialogItem struct {
	ID           string
	Kind         string
	DisplayName  string
	Description  *string
	DefaultValue *string
	Required     bool
	Choices      []PromptDialogChoice
}

type PromptDialogChoice struct {
	Value       string
	Description string
}

type Console interface {
	// Prints out a message to the underlying console write
	Message(ctx context.Context, message string)
	// Prints out a message following a contract ux item
	MessageUxItem(ctx context.Context, item ux.UxItem)
	WarnForFeature(ctx context.Context, id alpha.FeatureId)
	// Prints progress spinner with the given title.
	// If a previous spinner is running, the title is updated.
	ShowSpinner(ctx context.Context, title string, format SpinnerUxType)
	// Stop the current spinner from the console and change the spinner bar for the lastMessage
	// Set lastMessage to empty string to clear the spinner message instead of a displaying a last message
	// If there is no spinner running, this is a no-op function
	StopSpinner(ctx context.Context, lastMessage string, format SpinnerUxType)
	// Preview mode brings an embedded console within the current session.
	// Use nil for options to use defaults.
	// Use the returned io.Writer to produce the output within the previewer
	ShowPreviewer(ctx context.Context, options *ShowPreviewerOptions) io.Writer
	// Finalize the preview mode from console.
	StopPreviewer(ctx context.Context, keepLogs bool)
	// Determines if there is a current spinner running.
	IsSpinnerRunning(ctx context.Context) bool
	// Determines if the current spinner is an interactive spinner, where messages are updated periodically.
	// If false, the spinner is non-interactive, which means messages are rendered as a new console message on each
	// call to ShowSpinner, even when the title is unchanged.
	IsSpinnerInteractive() bool
	SupportsPromptDialog() bool
	PromptDialog(ctx context.Context, dialog PromptDialog) (map[string]any, error)
	// Prompts the user for a single value
	Prompt(ctx context.Context, options ConsoleOptions) (string, error)
	// PromptFs prompts the user for a filesystem path or directory.
	PromptFs(ctx context.Context, options ConsoleOptions, fsOptions FsOptions) (string, error)
	// Prompts the user to select a single value from a set of values
	Select(ctx context.Context, options ConsoleOptions) (int, error)
	// Prompts the user to select zero or more values from a set of values
	MultiSelect(ctx context.Context, options ConsoleOptions) ([]string, error)
	// Prompts the user to confirm an operation
	Confirm(ctx context.Context, options ConsoleOptions) (bool, error)
	// block terminal until the next enter
	WaitForEnter()
	// Writes a new line to the writer if there if the last two characters written are not '\n'
	EnsureBlankLine(ctx context.Context)
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
	writer    io.Writer
	formatter output.Formatter

	// isTerminal controls whether terminal-style input/output will be used.
	//
	// When isTerminal is false, the following notable behaviors apply:
	//   - Spinner progress will be written as standard newline messages.
	//   - Prompting assumes a non-terminal environment, where output written and input received are machine-friendly text,
	//     stripped of formatting characters.
	isTerminal bool
	noPrompt   bool
	// when non nil, use this client instead of prompting ourselves on the console.
	promptClient *externalPromptClient

	showProgressMu sync.Mutex // ensures atomicity when swapping the current progress renderer (spinner or previewer)

	spinner             *yacspin.Spinner
	spinnerLineMu       sync.Mutex // secures spinnerCurrentTitle and the line of spinner text
	spinnerTerminalMode yacspin.TerminalMode
	spinnerCurrentTitle string

	previewer *progressLog

	currentIndent *atomic.String
	// consoleWidth is the width of the underlying console window. The value is updated as the window resized. Nil when
	// isTerminal is false.
	consoleWidth *atomic.Int32
	// holds the last 2 bytes written by message or messageUX. This is used to detect when there is already an empty
	// line (\n\n)
	last2Byte [2]byte
}

type ConsoleOptions struct {
	Message string
	Help    string
	Options []string

	// OptionDetails is an optional field that can be used to provide additional information about the options.
	OptionDetails []string
	DefaultValue  any

	// Prompt-only options
	IsPassword bool
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
	} else if c.formatter != nil {
		c.println(ctx, message)
	} else {
		log.Println(message)
	}
	// Adding "\n" b/c calling Fprintln is adding one new line at the end to the msg
	c.updateLastBytes(message + "\n")
}

func (c *AskerConsole) updateLastBytes(msg string) {
	msgLen := len(msg)
	if msgLen == 0 {
		return
	}
	if msgLen < 2 {
		c.last2Byte[0] = c.last2Byte[1]
		c.last2Byte[1] = msg[msgLen-1]
		return
	}
	c.last2Byte[0] = msg[msgLen-2]
	c.last2Byte[1] = msg[msgLen-1]
}

func (c *AskerConsole) WarnForFeature(ctx context.Context, key alpha.FeatureId) {
	if shouldWarn() {
		c.MessageUxItem(ctx, &ux.MultilineMessage{
			Lines: []string{
				"",
				output.WithWarningFormat("WARNING: Feature '%s' is in alpha stage.", string(key)),
				fmt.Sprintf("To learn more about alpha features and their support, visit %s.",
					output.WithLinkFormat("https://aka.ms/azd-feature-stages")),
				"",
			},
		})
	}
}

// shouldWarn returns true if a warning should be emitted when using a given alpha feature.
func shouldWarn() bool {
	noAlphaWarnings, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_NO_ALPHA_WARNINGS"))

	return err != nil || !noAlphaWarnings
}

func (c *AskerConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// no need to check the spinner for json format, as the spinner won't start when using json format
		// instead, there would be a message about starting spinner
		json, _ := json.Marshal(item)
		fmt.Fprintln(c.writer, string(json))
		return
	}

	msg := item.ToString(c.currentIndent.Load())
	c.println(ctx, msg)
	// Adding "\n" b/c calling Fprintln is adding one new line at the end to the msg
	c.updateLastBytes(msg + "\n")
}

func (c *AskerConsole) println(ctx context.Context, msg string) {
	if c.IsSpinnerInteractive() && c.spinner.Status() == yacspin.SpinnerRunning {
		c.StopSpinner(ctx, "", Step)
		// default non-format
		fmt.Fprintln(c.writer, msg)
		_ = c.spinner.Start()
	} else {
		fmt.Fprintln(c.writer, msg)
	}
}

func defaultShowPreviewerOptions() *ShowPreviewerOptions {
	return &ShowPreviewerOptions{
		MaxLineCount: 5,
	}
}

func (c *AskerConsole) ShowPreviewer(ctx context.Context, options *ShowPreviewerOptions) io.Writer {
	c.showProgressMu.Lock()
	defer c.showProgressMu.Unlock()

	// Pause any active spinner
	currentMsg := c.spinnerCurrentTitle
	_ = c.spinner.Pause()

	if options == nil {
		options = defaultShowPreviewerOptions()
	}

	c.previewer = NewProgressLog(options.MaxLineCount, options.Prefix, options.Title, c.currentIndent.Load()+currentMsg)
	c.previewer.Start()
	c.writer = c.previewer
	return &consolePreviewerWriter{
		previewer: &c.previewer,
	}
}

func (c *AskerConsole) StopPreviewer(ctx context.Context, keepLogs bool) {
	c.previewer.Stop(keepLogs)
	c.previewer = nil
	c.writer = c.defaultWriter

	_ = c.spinner.Unpause()
}

// truncationDots is the text we use to indicate that text has been truncated.
const truncationDots = "..."

// The line of text for the spinner, displayed in the format of: <prefix><spinner> <message>
type spinnerLine struct {
	// The prefix before the spinner.
	Prefix string

	// Charset that is used to animate the spinner.
	CharSet []string

	// The message to be displayed.
	Message string
}

func (c *AskerConsole) spinnerLine(title string, indent string) spinnerLine {
	if !c.isTerminal {
		return spinnerLine{
			Prefix:  indent,
			CharSet: spinnerNoTerminalCharSet,
			Message: title,
		}
	}

	spinnerLen := len(indent) + len(spinnerCharSet[0]) + 1 // adding one for the empty space before the message
	width := int(c.consoleWidth.Load())

	switch {
	case width <= 3: // show number of dots up to 3
		return spinnerLine{
			CharSet: spinnerShortCharSet[:width],
		}
	case width <= spinnerLen+len(truncationDots): // show number of dots
		return spinnerLine{
			CharSet: spinnerShortCharSet,
		}
	case width <= spinnerLen+len(title): // truncate title
		return spinnerLine{
			Prefix:  indent,
			CharSet: spinnerCharSet,
			Message: title[:width-spinnerLen-len(truncationDots)] + truncationDots,
		}
	default:
		return spinnerLine{
			Prefix:  indent,
			CharSet: spinnerCharSet,
			Message: title,
		}
	}
}

func (c *AskerConsole) ShowSpinner(ctx context.Context, title string, format SpinnerUxType) {
	c.showProgressMu.Lock()
	defer c.showProgressMu.Unlock()

	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// Spinner is disabled when using json format.
		return
	}

	if c.previewer != nil {
		// spinner is not compatible with previewer.
		c.previewer.Header(c.currentIndent.Load() + title)
		return
	}

	c.spinnerLineMu.Lock()
	c.spinnerCurrentTitle = title

	indentPrefix := c.getIndent()
	line := c.spinnerLine(title, indentPrefix)

	_ = c.spinner.Pause()
	c.spinner.Message(line.Message)
	_ = c.spinner.CharSet(line.CharSet)
	c.spinner.Prefix(line.Prefix)
	_ = c.spinner.Unpause()

	if c.spinner.Status() == yacspin.SpinnerStopped {
		// While it is indeed safe to call Start regardless of whether the spinner is running,
		// calling Start may result in an additional line of output being written in non-tty scenarios
		_ = c.spinner.Start()
	}
	c.spinnerLineMu.Unlock()
}

// spinnerTerminalMode determines the appropriate terminal mode.
func spinnerTerminalMode(isTerminal bool) yacspin.TerminalMode {
	nonInteractiveMode := yacspin.ForceNoTTYMode | yacspin.ForceDumbTerminalMode
	if !isTerminal {
		return nonInteractiveMode
	}
	termMode := yacspin.ForceTTYMode
	if os.Getenv("TERM") == "dumb" {
		termMode |= yacspin.ForceDumbTerminalMode
	} else {
		termMode |= yacspin.ForceSmartTerminalMode
	}
	return termMode
}

var spinnerCharSet []string = []string{
	"|       |", "|=      |", "|==     |", "|===    |", "|====   |", "|=====  |", "|====== |",
	"|=======|", "| ======|", "|  =====|", "|   ====|", "|    ===|", "|     ==|", "|      =|",
}

var spinnerShortCharSet []string = []string{".", "..", "..."}

var spinnerNoTerminalCharSet []string = []string{""}

func setIndentation(spaces int) string {
	bytes := make([]byte, spaces)
	for i := range bytes {
		bytes[i] = byte(' ')
	}
	return string(bytes)
}

func (c *AskerConsole) getIndent() string {
	requiredSize := 2
	if requiredSize != len(c.currentIndent.Load()) {
		c.currentIndent.Store(setIndentation(requiredSize))
	}
	return c.currentIndent.Load()
}

func (c *AskerConsole) StopSpinner(ctx context.Context, lastMessage string, format SpinnerUxType) {
	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// Spinner is disabled when using json format.
		return
	}

	// Do nothing when it is already stopped
	if c.spinner.Status() == yacspin.SpinnerStopped {
		return
	}

	c.spinnerLineMu.Lock()
	c.spinnerCurrentTitle = ""
	// Update style according to MessageUxType
	if lastMessage != "" {
		lastMessage = c.getStopChar(format) + " " + lastMessage
	}

	_ = c.spinner.Stop()
	if lastMessage != "" {
		// Avoid using StopMessage() as it may result in an extra Message line print in non-tty scenarios
		fmt.Fprintln(c.writer, lastMessage)
	}

	c.spinnerLineMu.Unlock()
}

func (c *AskerConsole) IsSpinnerRunning(ctx context.Context) bool {
	return c.spinner.Status() != yacspin.SpinnerStopped
}

func (c *AskerConsole) IsSpinnerInteractive() bool {
	return c.spinnerTerminalMode&yacspin.ForceTTYMode > 0
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
	return fmt.Sprintf("%s%s", c.getIndent(), stopChar)
}

func promptFromOptions(options ConsoleOptions) survey.Prompt {
	if options.IsPassword {
		// different than survey.Input, survey.Password doest not reset the line before rendering the question
		// see password implementation: https://github.com/AlecAivazis/survey/blob/master/password.go#L51
		// and input: https://github.com/AlecAivazis/survey/blob/master/input.go#L141
		// by calling .Render(), the line is reset, cleaning any current message or spinner.
		tm.Print(tm.ResetLine(""))
		tm.Flush()
		return &survey.Password{
			Message: options.Message,
			Help:    options.Help,
		}
	}

	var defaultValue string
	if value, ok := options.DefaultValue.(string); ok {
		defaultValue = value
	}
	return &survey.Input{
		Message: options.Message,
		Default: defaultValue,
		Help:    options.Help,
	}
}

// afterIoSentinel is a sentinel value used after Input/Output operations as the state for the last 2-bytes written.
// For example, after running Prompt or Confirm, the last characters on the terminal should be any char (represented by the
// 0 in the sentinel), followed by a new line.
const afterIoSentinel = "0\n"

func (c *AskerConsole) SupportsPromptDialog() bool {
	return c.promptClient != nil
}

// PromptDialog prompts for multiple values using a single dialog. When successful, it returns a map of prompt IDs to their
// values.
func (c *AskerConsole) PromptDialog(ctx context.Context, dialog PromptDialog) (map[string]any, error) {

	request := externalPromptDialogRequest{
		Title:       dialog.Title,
		Description: dialog.Description,
		Prompts:     make([]externalPromptDialogPrompt, len(dialog.Prompts)),
	}

	for i, prompt := range dialog.Prompts {
		request.Prompts[i] = externalPromptDialogPrompt{
			ID:           prompt.ID,
			Kind:         prompt.Kind,
			DisplayName:  prompt.DisplayName,
			Description:  prompt.Description,
			DefaultValue: prompt.DefaultValue,
			Required:     prompt.Required,
		}
	}

	resp, err := c.promptClient.PromptDialog(ctx, request)
	if err != nil {
		return nil, err
	}

	ret := make(map[string]any, len(*resp.Inputs))
	for _, v := range *resp.Inputs {
		var unmarshalledValue any
		if err := json.Unmarshal(v.Value, &unmarshalledValue); err != nil {
			return nil, fmt.Errorf("unmarshalling value %s: %w", v.ID, err)
		}

		ret[v.ID] = unmarshalledValue
	}

	return ret, nil
}

// Prompts the user for a single value
func (c *AskerConsole) Prompt(ctx context.Context, options ConsoleOptions) (string, error) {
	var response string

	if c.promptClient != nil {
		opts := promptOptions{
			Type: "string",
			Options: promptOptionsOptions{
				Message: options.Message,
				Help:    options.Help,
			},
		}

		if options.IsPassword {
			opts.Type = "password"
		}

		if value, ok := options.DefaultValue.(string); ok {
			opts.Options.DefaultValue = to.Ptr[any](value)
		}

		result, err := c.promptClient.Prompt(ctx, opts)
		if errors.Is(err, promptCancelledErr) {
			return "", terminal.InterruptErr
		} else if err != nil {
			return "", err
		}

		if err := json.Unmarshal(result, &response); err != nil {
			return "", fmt.Errorf("unmarshalling response: %w", err)
		}

		return response, nil
	}

	err := c.doInteraction(func(c *AskerConsole) error {
		return c.asker(promptFromOptions(options), &response)
	})
	if err != nil {
		return response, err
	}
	c.updateLastBytes(afterIoSentinel)
	return response, nil
}

func choicesFromOptions(options ConsoleOptions) []promptChoice {
	choices := make([]promptChoice, len(options.Options))
	for i, option := range options.Options {
		choices[i] = promptChoice{
			Value: option,
		}

		if i < len(options.OptionDetails) && options.OptionDetails[i] != "" {
			choices[i].Detail = &options.OptionDetails[i]
		}
	}
	return choices

}

// Prompts the user to select from a set of values
func (c *AskerConsole) Select(ctx context.Context, options ConsoleOptions) (int, error) {
	if c.promptClient != nil {
		opts := promptOptions{
			Type: "select",
			Options: promptOptionsOptions{
				Message: options.Message,
				Help:    options.Help,
				Choices: to.Ptr(choicesFromOptions(options)),
			},
		}

		if value, ok := options.DefaultValue.(string); ok {
			opts.Options.DefaultValue = to.Ptr[any](value)
		}

		result, err := c.promptClient.Prompt(ctx, opts)
		if errors.Is(err, promptCancelledErr) {
			return -1, terminal.InterruptErr
		} else if err != nil {
			return -1, err
		}

		var choice string

		if err := json.Unmarshal(result, &choice); err != nil {
			return -1, fmt.Errorf("unmarshalling response: %w", err)
		}

		res := slices.Index(options.Options, choice)
		if res == -1 {
			return -1, fmt.Errorf("invalid choice: %s", choice)
		}

		return res, nil
	}

	surveyOptions := make([]string, len(options.Options))
	surveyDefault := options.DefaultValue
	surveyDefaultAsString, surveyDefaultIsString := surveyDefault.(string)

	// Modify the options and default value to include any details
	for i, option := range options.Options {
		surveyOptions[i] = option

		if c.IsSpinnerInteractive() && i < len(options.OptionDetails) {
			if options.OptionDetails[i] != "" {
				detailString := output.WithGrayFormat("(%s)", options.OptionDetails[i])
				surveyOptions[i] += fmt.Sprintf("\n  %s\n", detailString)
			} else {
				surveyOptions[i] += "\n"
			}

			if surveyDefaultIsString && surveyDefaultAsString == option {
				surveyDefault = surveyOptions[i]
			}
		}
	}

	survey := &survey.Select{
		Message: options.Message,
		Options: surveyOptions,
		Default: surveyDefault,
		Help:    options.Help,
	}

	var response int

	err := c.doInteraction(func(c *AskerConsole) error {
		return c.asker(survey, &response)
	})
	if err != nil {
		return -1, err
	}

	c.updateLastBytes(afterIoSentinel)
	return response, nil
}

func (c *AskerConsole) MultiSelect(ctx context.Context, options ConsoleOptions) ([]string, error) {
	var response []string

	if c.promptClient != nil {
		opts := promptOptions{
			Type: "multiSelect",
			Options: promptOptionsOptions{
				Message: options.Message,
				Help:    options.Help,
				Choices: to.Ptr(choicesFromOptions(options)),
			},
		}

		if value, ok := options.DefaultValue.([]string); ok {
			opts.Options.DefaultValue = to.Ptr[any](value)
		}

		result, err := c.promptClient.Prompt(ctx, opts)
		if errors.Is(err, promptCancelledErr) {
			return nil, terminal.InterruptErr
		} else if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(result, &response); err != nil {
			return nil, fmt.Errorf("unmarshalling response: %w", err)
		}

		return response, nil
	}

	surveyOptions := make([]string, len(options.Options))
	surveyDefault := options.DefaultValue
	surveyDefaultAsArr, surveyDefaultIsArr := surveyDefault.([]string)
	// Modify the options and default value to include any details
	for i, option := range options.Options {
		surveyOptions[i] = option

		if c.IsSpinnerInteractive() && i < len(options.OptionDetails) {
			detailString := output.WithGrayFormat("%s", options.OptionDetails[i])
			surveyOptions[i] += fmt.Sprintf("\n  %s\n", detailString)
		}

		if surveyDefaultIsArr {
			for idx, defaultOption := range surveyDefaultAsArr {
				if defaultOption == option {
					surveyDefaultAsArr[idx] = surveyOptions[i]
				}
			}
		}
	}

	survey := &survey.MultiSelect{
		Message: options.Message,
		Options: surveyOptions,
		Default: surveyDefault,
		Help:    options.Help,
	}

	err := c.doInteraction(func(c *AskerConsole) error {
		return c.asker(survey, &response)
	})
	if err != nil {
		return nil, err
	}

	return response, nil
}

// Prompts the user to confirm an operation
func (c *AskerConsole) Confirm(ctx context.Context, options ConsoleOptions) (bool, error) {
	if c.promptClient != nil {
		opts := promptOptions{
			Type: "confirm",
			Options: promptOptionsOptions{
				Message: options.Message,
				Help:    options.Help,
			},
		}

		if value, ok := options.DefaultValue.(bool); ok {
			opts.Options.DefaultValue = to.Ptr[any](value)
		}

		result, err := c.promptClient.Prompt(ctx, opts)
		if errors.Is(err, promptCancelledErr) {
			return false, terminal.InterruptErr
		} else if err != nil {
			return false, err
		}

		var response string

		if err := json.Unmarshal(result, &response); err != nil {
			return false, fmt.Errorf("unmarshalling response: %w", err)
		}

		switch response {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return false, fmt.Errorf("invalid response: %s", response)

		}
	}

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

	c.updateLastBytes(afterIoSentinel)
	return response, nil
}

const c_newLine = '\n'

func (c *AskerConsole) EnsureBlankLine(ctx context.Context) {
	if c.last2Byte[0] == c_newLine && c.last2Byte[1] == c_newLine {
		return
	}
	if c.last2Byte[1] != c_newLine {
		c.Message(ctx, "\n")
		return
	}
	// [1] is '\n' but [0] is not. One new line missing
	c.Message(ctx, "")
}

// wait until the next enter
func (c *AskerConsole) WaitForEnter() {
	if c.noPrompt {
		return
	}

	inputScanner := bufio.NewScanner(c.handles.Stdin)
	if scan := inputScanner.Scan(); !scan {
		if err := inputScanner.Err(); err != nil {
			log.Printf("error while waiting for enter: %v", err)
		}
	}
}

// Gets the underlying writer for the console
func (c *AskerConsole) GetWriter() io.Writer {
	return c.writer
}

func (c *AskerConsole) Handles() ConsoleHandles {
	return c.handles
}

// consoleWidth the number of columns in the active console window
func consoleWidth() int32 {
	widthInt, _ := consolesize.GetConsoleSize()

	// Suppress G115: integer overflow conversion int -> int32 below.
	// Explanation:
	// consolesize.GetConsoleSize() returns an int, but the underlying implementation actually is a uint16 on both
	// Windows and unix systems.
	//
	// In practice, console width is the number of columns (text) in the active console window.
	// We don't ever expect this to be larger than math.MaxInt32, so we can safely cast to int32.
	// nolint:gosec // G115
	return int32(widthInt)
}

func (c *AskerConsole) handleResize(width int32) {
	c.consoleWidth.Store(width)

	c.spinnerLineMu.Lock()
	if c.spinner.Status() == yacspin.SpinnerRunning {
		line := c.spinnerLine(c.spinnerCurrentTitle, c.currentIndent.Load())
		c.spinner.Message(line.Message)
		_ = c.spinner.CharSet(line.CharSet)
		c.spinner.Prefix(line.Prefix)
	}
	c.spinnerLineMu.Unlock()
}

func watchTerminalResize(c *AskerConsole) {
	if runtime.GOOS == "windows" {
		go func() {
			prevWidth := consoleWidth()
			for {
				time.Sleep(time.Millisecond * 250)
				width := consoleWidth()

				if prevWidth != width {
					c.handleResize(width)
				}
				prevWidth = width
			}
		}()
	} else {
		// avoid taking a dependency on syscall.SIGWINCH (unix-only constant) directly
		const SIGWINCH = syscall.Signal(0x1c)
		signalChan := make(chan os.Signal, 1)
		signal.Notify(signalChan, SIGWINCH)
		go func() {
			for range signalChan {
				c.handleResize(consoleWidth())
			}
		}()
	}
}

func watchTerminalInterrupt(c *AskerConsole) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan

		// unhide the cursor if applicable
		_ = c.spinner.Stop()

		os.Exit(1)
	}()
}

// Writers that back the underlying console.
type Writers struct {
	// The writer to write output to.
	Output io.Writer

	// The writer to write spinner output to. If nil, the spinner will write to Output.
	Spinner io.Writer
}

// ExternalPromptConfiguration allows configuring the console to delegate prompts to an external service.
type ExternalPromptConfiguration struct {
	Endpoint    string
	Key         string
	Transporter policy.Transporter
}

// Creates a new console with the specified writers, handles and formatter. When externalPromptCfg is non nil, it is used
// instead of prompting on the console.
func NewConsole(
	noPrompt bool,
	isTerminal bool,
	writers Writers,
	handles ConsoleHandles,
	formatter output.Formatter,
	externalPromptCfg *ExternalPromptConfiguration) Console {
	asker := NewAsker(noPrompt, isTerminal, handles.Stdout, handles.Stdin)

	c := &AskerConsole{
		asker:         asker,
		handles:       handles,
		defaultWriter: writers.Output,
		writer:        writers.Output,
		formatter:     formatter,
		isTerminal:    isTerminal,
		currentIndent: atomic.NewString(""),
		noPrompt:      noPrompt,
	}

	if writers.Spinner == nil {
		writers.Spinner = writers.Output
	}

	if externalPromptCfg != nil {
		c.promptClient = newExternalPromptClient(
			externalPromptCfg.Endpoint, externalPromptCfg.Key, externalPromptCfg.Transporter)
	}

	spinnerConfig := yacspin.Config{
		Frequency:    200 * time.Millisecond,
		Writer:       writers.Spinner,
		Suffix:       " ",
		TerminalMode: spinnerTerminalMode(isTerminal),
	}
	if isTerminal {
		spinnerConfig.CharSet = spinnerCharSet
	} else {
		spinnerConfig.CharSet = spinnerNoTerminalCharSet
	}

	c.spinner, _ = yacspin.New(spinnerConfig)
	c.spinnerTerminalMode = spinnerConfig.TerminalMode
	if isTerminal {
		c.consoleWidth = atomic.NewInt32(consoleWidth())
		watchTerminalResize(c)
		watchTerminalInterrupt(c)
	}

	return c
}

// IsTerminal returns true if the given file descriptors are attached to a terminal,
// taking into account of environment variables that force TTY behavior.
func IsTerminal(stdoutFd uintptr, stdinFd uintptr) bool {
	// User override to force TTY behavior
	if forceTty, err := strconv.ParseBool(os.Getenv("AZD_FORCE_TTY")); err == nil {
		return forceTty
	}

	// By default, detect if we are running on CI and force no TTY mode if we are.
	// If this is affecting you locally while debugging on a CI machine,
	// use the override AZD_FORCE_TTY=true.
	if resource.IsRunningOnCI() {
		return false
	}

	return isatty.IsTerminal(stdoutFd) && isatty.IsTerminal(stdinFd)
}

func GetStepResultFormat(result error) SpinnerUxType {
	formatResult := StepDone
	if result != nil {
		formatResult = StepFailed
	}
	return formatResult
}

// Handle doing interactive calls. It checks if there's a spinner running to pause it before doing interactive actions.
func (c *AskerConsole) doInteraction(promptFn func(c *AskerConsole) error) error {
	if c.spinner.Status() == yacspin.SpinnerRunning {
		_ = c.spinner.Pause()

		// Ensure the spinner is always resumed
		defer func() {
			_ = c.spinner.Unpause()
		}()
	}

	// Track total time for promptFn.
	// It includes the time spent in rendering the prompt (likely <1ms)
	// before the user has a chance to interact with the prompt.
	start := time.Now()
	defer func() {
		tracing.InteractTimeMs.Add(time.Since(start).Milliseconds())
	}()

	// Execute the interactive prompt
	return promptFn(c)
}
