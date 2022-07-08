package spin

import (
	"fmt"
	"time"

	"github.com/theckman/yacspin"
)

// RunWithUpdateFunc is the function signature that RunWithUpdater expects
type RunWithUpdateFunc = func(func(string)) error

// RunFunc is the function signature that Run expects
type RunFunc func() error

// Run is the equivalent of RunWithUpdater with no updater specified
func Run(prefix string, runFn RunFunc, finalFuncs ...func(*yacspin.Spinner, bool)) error {
	return RunWithUpdater(
		prefix,
		func(func(string)) error {
			return runFn()
		},
		finalFuncs...,
	)
}

// RunWithUpdater runs runFn with a spinner. The prefix of the spinner is set to prefix,
// and when runFn is complete, each function in finalFuncs is executed in serial, regardless
// of whether runFn errored.
func RunWithUpdater(prefix string, runFn RunWithUpdateFunc, finalFuncs ...func(*yacspin.Spinner, bool)) error {
	spin, _ := yacspin.New(yacspin.Config{
		Frequency: time.Millisecond * 500,
		CharSet:   yacspin.CharSets[9],
	})
	spin.Prefix(prefix)

	err := spin.Start()
	if err != nil {
		return fmt.Errorf("starting spinner: %w", err)
	}

	// When `runFn` completes and this function returns, strop the spinner. We ignore
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

	result = runFn(func(newPrefix string) {
		if newPrefix != "" {
			spin.Prefix(newPrefix)
		}
	})

	return result
}
