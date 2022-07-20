package spin

import (
	"fmt"
	"sync"
	"time"

	"github.com/theckman/yacspin"
)

// SpinnerUpdater contains callbacks for updating the spinner.
type SpinnerUpdater struct {
	// A function, when invoked, updates the prefix portion of the spinner's title
	// Example: Invoking UpdatePrefix("Uploading files ") sets the spinner to look like: "Uploading files <spinner char>"
	UpdatePrefix func(string)

	// A function, when invoked, logs a message to standard output and pushes the spinner onto a new line.
	// Example:
	// Console output before, with spinner title set to "Doing things...":
	// > Doing things... X
	//
	// Console output after LogMessage("Step 1 completed."):
	// > Step 1 completed.
	// > Doing things... X
	LogMessage func(string)
}

// RunWithUpdateFunc is the function signature that RunWithUpdater expects
type RunWithUpdateFunc = func(*SpinnerUpdater) error

// RunFunc is the function signature that Run expects
type RunFunc func() error

// Run is the equivalent of RunWithUpdater with no updater specified
func Run(prefix string, runFn RunFunc, finalFuncs ...func(*yacspin.Spinner, bool)) error {
	return RunWithUpdater(
		prefix,
		func(*SpinnerUpdater) error {
			return runFn()
		},
		finalFuncs...,
	)
}

// RunWithUpdater runs runFn with a spinner. The prefix of the spinner is set to prefix,
// and when runFn is complete, each function in finalFuncs is executed serially, regardless
// of whether runFn errored, but each finalFunction gets a boolean argument indicating if
// runFn succeeded.
func RunWithUpdater(prefix string, runFn RunWithUpdateFunc, finalFuncs ...func(*yacspin.Spinner, bool)) error {
	spin, _ := yacspin.New(yacspin.Config{
		Frequency: time.Millisecond * 500,
		CharSet:   yacspin.CharSets[9],
		// Do not set a StopMessage.
		// The current LogMessage functionality depends on the StopMessage being empty.
	})
	spin.Prefix(prefix)

	err := spin.Start()
	if err != nil {
		return fmt.Errorf("starting spinner: %w", err)
	}

	// When `runFn` completes and this function returns, stop the spinner. We ignore
	// the error because Stop only returns an error if the spinner is not running and
	// we know that it is running.
	// nolint:errcheck
	defer spin.Stop()

	// When `runFn` completes (which causes this function to return), run
	// all the final functions. NOTE: Since go processes `defers` in LIFO
	// order, all of these final functions will run before `Stop` is called.
	var result error
	defer func() {
		for _, finalFunc := range finalFuncs {
			finalFunc(spin, result == nil)
		}
	}()

	logMutex := sync.Mutex{}

	result = runFn(&SpinnerUpdater{
		UpdatePrefix: func(newPrefix string) {
			if newPrefix != "" {
				spin.Prefix(newPrefix)
			}
		},
		LogMessage: func(message string) {
			if message != "" {
				defer logMutex.Unlock()

				logMutex.Lock()
				// Ignore error returned (which can only happen if spinner is not running)
				// We control the spinner's state, so the spinner is guaranteed to be running
				// nolint:errcheck
				spin.Stop()
				fmt.Println(message)

				// Ignore error returned (which can only happen if spinner is running)
				// We control the spinner's state, so the spinner is guaranteed not to be running
				// nolint:errcheck
				spin.Start()
			}
		},
	})

	return result
}
