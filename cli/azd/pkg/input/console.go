// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/mattn/go-isatty"
	"github.com/nathan-fiscaletti/consolesize-go"
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

// ShowPreviewerOptions provide the settings to start a console previewer.
type ShowPreviewerOptions struct {
	Prefix       string
	MaxLineCount int
	Title        string
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
	StopPreviewer(ctx context.Context)
	// Determines if there is a current spinner running.
	IsSpinnerRunning(ctx context.Context) bool
	// Determines if the current spinner is an interactive spinner, where messages are updated periodically.
	// If false, the spinner is non-interactive, which means messages are rendered as a new console message on each
	// call to ShowSpinner, even when the title is unchanged.
	IsSpinnerInteractive() bool
	// Prompts the user for a single value
	Prompt(ctx context.Context, options ConsoleOptions) (string, error)
	// Prompts the user to select from a set of values
	Select(ctx context.Context, options ConsoleOptions) (int, error)
	// Prompts the user to confirm an operation
	Confirm(ctx context.Context, options ConsoleOptions) (bool, error)
	// block terminal until the next enter
	WaitForEnter()
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
	writer     io.Writer
	formatter  output.Formatter
	isTerminal bool
	noPrompt   bool

	spinner                 *yacspin.Spinner
	spinnerTerminalMode     yacspin.TerminalMode
	spinnerTerminalModeOnce sync.Once

	currentIndent         string
	consoleWidth          int
	previewer             *progressLog
	initialWriter         io.Writer
	currentSpinnerMessage string
	// writeControlMutex ensures no race conditions happen while methods are writing to the terminal.
	// AskerConsole can be used as a singleton, hence, more than one component can invoke its methods at the same time.
	// A method should lock this mutex if no other writing to he terminal should occur at the same time.
	writeControlMutex sync.Mutex
}

type ConsoleOptions struct {
	Message      string
	Help         string
	Options      []string
	DefaultValue any

	// Prompt-only options

	IsPassword bool
	Suggest    func(input string) (completions []string)
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

func (c *AskerConsole) WarnForFeature(ctx context.Context, key alpha.FeatureId) {
	if shouldWarn(key) {
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
func shouldWarn(key alpha.FeatureId) bool {
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

	if c.spinner != nil && c.spinner.Status() == yacspin.SpinnerRunning {
		c.StopSpinner(ctx, "", Step)
		// default non-format
		fmt.Fprintln(c.writer, item.ToString(c.currentIndent))
		_ = c.spinner.Start()
	} else {
		fmt.Fprintln(c.writer, item.ToString(c.currentIndent))
	}
}

func defaultShowPreviewerOptions() *ShowPreviewerOptions {
	return &ShowPreviewerOptions{
		MaxLineCount: 5,
	}
}

func (c *AskerConsole) ShowPreviewer(ctx context.Context, options *ShowPreviewerOptions) io.Writer {
	c.writeControlMutex.Lock()
	defer c.writeControlMutex.Unlock()

	// auto-stop any spinner
	currentMsg := c.currentSpinnerMessage
	c.StopSpinner(ctx, "", Step)

	if options == nil {
		options = defaultShowPreviewerOptions()
	}

	c.previewer = NewProgressLog(options.MaxLineCount, options.Prefix, options.Title, c.currentIndent+currentMsg)
	c.previewer.Start()
	c.writer = c.previewer
	return &consolePreviewerWriter{
		previewer: &c.previewer,
	}
}

func (c *AskerConsole) StopPreviewer(ctx context.Context) {
	c.previewer.Stop()
	c.previewer = nil
	c.writer = c.initialWriter
}

const cPostfix = "..."

func (c *AskerConsole) spinnerText(title, charset string) string {

	spinnerLen := len(charset) + 1 // adding one for the empty space before the message

	if len(title)+spinnerLen >= c.consoleWidth {
		return fmt.Sprintf("%s%s", title[:c.consoleWidth-spinnerLen-len(cPostfix)], cPostfix)
	}
	return title
}

func (c *AskerConsole) ShowSpinner(ctx context.Context, title string, format SpinnerUxType) {
	c.writeControlMutex.Lock()
	defer c.writeControlMutex.Unlock()

	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// Spinner is disabled when using json format.
		return
	}

	if c.consoleWidth <= cMinConsoleWidth {
		// no spinner for consoles with width <= cMinConsoleWidth
		c.Message(ctx, title)
		return
	}

	if c.previewer != nil {
		// spinner is not compatible with previewer.
		c.previewer.Header(c.currentIndent + title)
		return
	}

	// mutating an existing spinner brings issues on how the messages are formatted
	// so, instead of mutating, we stop any current spinner and replaced it for a new one
	if c.spinner != nil {
		_ = c.spinner.Stop()
	}

	charSet := c.getCharset(format)
	c.currentSpinnerMessage = title

	// determine the terminal mode once
	c.spinnerTerminalModeOnce.Do(func() {
		c.spinnerTerminalMode = GetSpinnerTerminalMode(&c.isTerminal)
	})

	spinnerConfig := yacspin.Config{
		Frequency:       200 * time.Millisecond,
		Writer:          c.writer,
		Suffix:          " ",
		SuffixAutoColon: true,
		Message:         c.spinnerText(title, charSet[0]),
		CharSet:         charSet,
	}
	spinnerConfig.TerminalMode = c.spinnerTerminalMode

	c.spinner, _ = yacspin.New(spinnerConfig)

	_ = c.spinner.Start()
}

// GetSpinnerTerminalMode gets the appropriate terminal mode for the spinner based on the current environment,
// taking into account of environment variables that can control the terminal mode behavior.
func GetSpinnerTerminalMode(isTerminal *bool) yacspin.TerminalMode {
	nonInteractiveMode := yacspin.ForceNoTTYMode | yacspin.ForceDumbTerminalMode
	if isTerminal != nil && !*isTerminal {
		return nonInteractiveMode
	}

	// isTerminal not provided, determine it ourselves
	if isTerminal == nil && !isatty.IsTerminal(os.Stdout.Fd()) {
		return nonInteractiveMode
	}

	// User override to force non-TTY behavior
	if os.Getenv("AZD_DEBUG_FORCE_NO_TTY") == "1" {
		return nonInteractiveMode
	}

	// By default, detect if we are running on CI and force no TTY mode if we are.
	// Allow for an override if this is not desired.
	shouldDetectCI := true
	if strVal, has := os.LookupEnv("AZD_TERM_SKIP_CI_DETECT"); has {
		skip, err := strconv.ParseBool(strVal)
		if err != nil {
			log.Println("AZD_TERM_SKIP_CI_DETECT is not a valid boolean value")
		} else if skip {
			shouldDetectCI = false
		}
	}

	if shouldDetectCI && resource.IsRunningOnCI() {
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
	c.currentSpinnerMessage = ""
	if c.formatter != nil && c.formatter.Kind() == output.JsonFormat {
		// Spinner is disabled when using json format.
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

	_ = c.spinner.Pause()
	c.spinner.StopMessage(lastMessage)
	_ = c.spinner.Stop()
}

func (c *AskerConsole) IsSpinnerRunning(ctx context.Context) bool {
	return c.spinner != nil && c.spinner.Status() != yacspin.SpinnerStopped
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
	return fmt.Sprintf("%s%s", c.getIndent(format), stopChar)
}

func promptFromOptions(options ConsoleOptions) survey.Prompt {
	if options.IsPassword {
		return &survey.Password{
			Message: options.Message,
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
		Suggest: options.Suggest,
	}
}

// Prompts the user for a single value
func (c *AskerConsole) Prompt(ctx context.Context, options ConsoleOptions) (string, error) {
	var response string

	err := c.doInteraction(func(c *AskerConsole) error {
		return c.asker(promptFromOptions(options), &response)
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

const cMinConsoleWidth int = 40

func getConsoleWidth() int {
	width, _ := consolesize.GetConsoleSize()
	if width < cMinConsoleWidth {
		return cMinConsoleWidth
	}
	return width
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
		isTerminal:    isTerminal,
		consoleWidth:  getConsoleWidth(),
		initialWriter: w,
		noPrompt:      noPrompt,
	}
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
	if c.spinner != nil && c.spinner.Status() == yacspin.SpinnerRunning {
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
