package spin

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/theckman/yacspin"
)

// Spinner is a type representing an animated CLi terminal spinner.
type Spinner struct {
	writer   io.Writer
	spinner  *yacspin.Spinner
	logMutex sync.Mutex
}

// Sets the title of the spinner.
func (s *Spinner) Title(title string) {
	if title != "" {
		s.spinner.Message(title)
	}
}

// Logs a message to standard output and pushes the spinner onto a new line.
// Example:
// Console output before, with spinner title set to "Doing things...":
// > Doing things... X
//
// Console output after LogMessage("Step 1 completed."):
// > Step 1 completed.
// > Doing things... X
func (s *Spinner) Println(message string) {
	s.print(message, true)
}

func (s *Spinner) print(message string, addNewLine bool) {
	if message != "" {
		defer s.logMutex.Unlock()

		s.logMutex.Lock()

		s.Stop()
		if addNewLine {
			fmt.Fprintln(s.writer, message)
		} else {
			fmt.Fprint(s.writer, message)
		}
		s.Start()
	}
}

// Implements the standard io.Writer interface
func (s *Spinner) Write(p []byte) (int, error) {
	message := string(p)
	s.print(message, false)

	return len(p), nil
}

// Run renders the spinner while runFn is executing,
// returning the error from executing runFn.
// The spinner message is erased when the spinner is stopped.
func (s *Spinner) Run(runFn func() error) error {
	s.Start()
	defer s.Stop()

	return runFn()
}

// Starts the spinner.
func (s *Spinner) Start() {
	// Only possible error is if the spinner is already running.
	// We ignore the error since the error indicates the spinner is running,
	// which simply reasserts the state of the spinner.
	_ = s.spinner.Start()
}

// Stops the spinner. The spinning message is erased when the spinner is stopped.
func (s *Spinner) Stop() {
	// Only possible error is if the spinner is already stopped.
	// We ignore the error since the error indicates the spinner is stopped,
	// which simply reasserts the state of the spinner.
	_ = s.spinner.Stop()
}

func NewSpinner(writer io.Writer, title string) *Spinner {
	config := yacspin.Config{
		Frequency:    time.Millisecond * 500,
		CharSet:      yacspin.CharSets[9],
		SpinnerAtEnd: true,
		Message:      title,
		// Set prefix to empty space to always append a space between the spinner title and the spinner itself.
		// From yacspin.Spinner: if SpinnerAtEnd is set to true, the printed line will instead look like:
		// <message><prefix><spinner><suffix>
		Prefix: " ",
		Writer: writer,
		// Do not set a StopMessage.
		// The current LogMessage functionality depends on the StopMessage being empty.
	}

	spinner, _ := yacspin.New(config)

	return &Spinner{
		writer:  writer,
		spinner: spinner,
	}
}

type contextKey string

const (
	spinnerContextKey contextKey = "spinner"
)

// Creates and returns new context with the specified spinner instance
func WithSpinner(ctx context.Context, spinner *Spinner) context.Context {
	return context.WithValue(ctx, spinnerContextKey, spinner)
}

// Attempts to retrieve a Spinner instance from the current context.
// Returns the found instance when available or `nil` if not found.
func GetSpinner(ctx context.Context) *Spinner {
	spinner, ok := ctx.Value(spinnerContextKey).(*Spinner)
	if !ok {
		return nil
	}

	return spinner
}

// Gets a spinner from the specified context, otherwise creates a new instance
// Returns a new context when a new spinner is created
func GetOrCreateSpinner(ctx context.Context, w io.Writer, title string) (*Spinner, context.Context) {
	spinner := GetSpinner(ctx)
	if spinner == nil {
		spinner = NewSpinner(w, title)
		ctx = WithSpinner(ctx, spinner)
	}

	spinner.Title(title)

	return spinner, ctx
}
