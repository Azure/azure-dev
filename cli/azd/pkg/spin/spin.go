package spin

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"github.com/theckman/yacspin"
)

// Default writer to std.out, with possibility to mock
var writer io.Writer = colorable.NewColorableStdout()

// Spinner is a type representing an animated CLi terminal spinner.
type Spinner struct {
	spinner  *yacspin.Spinner
	logMutex sync.Mutex
}

// Updates the prefix portion of the spinner's title
// Example: Invoking UpdatePrefix("Uploading files ") sets the spinner to look like: "Uploading files <spinner char>"
func (s *Spinner) Prefix(prefix string) {
	if prefix != "" {
		s.spinner.Prefix(prefix)
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
func (s *Spinner) LogMessage(message string) {
	if message != "" {
		defer s.logMutex.Unlock()

		s.logMutex.Lock()
		// Ignore error returned (which can only happen if spinner is not running)
		// We control the spinner's state, so the spinner is guaranteed to be running
		// nolint:errcheck
		s.spinner.Stop()
		fmt.Fprintln(writer, message)

		// Ignore error returned (which can only happen if spinner is running)
		// We control the spinner's state, so the spinner is guaranteed not to be running
		// nolint:errcheck
		s.spinner.Start()
	}
}

func (s *Spinner) Run(runFn func() error) error {
	err := s.spinner.Start()
	if err != nil {
		return fmt.Errorf("starting spinner: %w", err)
	}

	defer s.spinner.Stop()

	return runFn()
}

func (s *Spinner) Start() error {
	return s.Start()
}

func (s *Spinner) Stop() error {
	return s.Stop()
}

func New(prefix string) *Spinner {
	spinner, _ := yacspin.New(yacspin.Config{
		Frequency: time.Millisecond * 500,
		CharSet:   yacspin.CharSets[9],
		Prefix:    prefix,
		Writer:    writer,
		// Do not set a StopMessage.
		// The current LogMessage functionality depends on the StopMessage being empty.
	})

	return &Spinner{
		spinner: spinner,
	}
}
