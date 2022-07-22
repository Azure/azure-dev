package spin

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"github.com/theckman/yacspin"
)

// Default writer to std.out, with possibility to mock for tests
var writer io.Writer = colorable.NewColorableStdout()

// Spinner is a type representing an animated CLi terminal spinner.
type Spinner struct {
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
func (s *Spinner) Println(message string) error {
	if message != "" {
		defer s.logMutex.Unlock()

		s.logMutex.Lock()
		err := s.spinner.Stop()
		if err != nil {
			return err
		}

		fmt.Fprintln(writer, message)

		err = s.spinner.Start()
		if err != nil {
			return err
		}
	}

	return nil
}

// Run renders the spinner while runFn is executing,
// returning the error from executing runFn.
func (s *Spinner) Run(runFn func() error) error {
	err := s.spinner.Start()
	if err != nil {
		return fmt.Errorf("starting spinner: %w", err)
	}

	defer s.spinner.Stop()

	return runFn()
}

// Starts the spinner. Only possible error is if the spinner is already running.
func (s *Spinner) Start() error {
	return s.spinner.Start()
}

// Stops the spinner. Only possible error is if the spinner is already stopped.
func (s *Spinner) Stop() error {
	return s.spinner.Stop()
}

func New(title string) *Spinner {
	config := yacspin.Config{
		Frequency:    time.Millisecond * 500,
		CharSet:      yacspin.CharSets[9],
		SpinnerAtEnd: true,
		Message:      title,
		// Set prefix to empty space to always append a space between the spinner title and the spinner itself.
		// From yacspin.Spinner: if SpinnerAtEnd is set to true, the printed line will instead look like: <message><prefix><spinner><suffix>
		Prefix: " ",
		Writer: writer,
		// Do not set a StopMessage.
		// The current LogMessage functionality depends on the StopMessage being empty.
	}

	spinner, _ := yacspin.New(config)

	return &Spinner{
		spinner: spinner,
	}
}
