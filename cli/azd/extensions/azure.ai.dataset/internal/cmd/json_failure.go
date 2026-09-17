// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// jsonError is what a failing command answers with under `-o json`.
//
// A failure used to write nothing at all to stdout, so `... -o json | jq` read
// an empty stream and reported a parse error of its own -- the reason the
// command failed reached the terminal as prose, and the only thing the pipeline
// saw was its own syntax complaint.
type jsonError struct {
	Error jsonErrorBody `json:"error"`
}

type jsonErrorBody struct {
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// exitProcess ends the process. Replaced in tests, which cannot survive a real
// os.Exit.
var exitProcess = os.Exit

// wantsJSON reports whether the caller asked for a machine-readable answer.
//
// The parsed flag is preferred; the raw arguments are the fallback for when
// parsing stopped before reaching it.
func wantsJSON(cmd *cobra.Command) bool {
	return isJSON(cmd) || outputFromRawArgs(os.Args[1:]) == outputJSON
}

// failAs answers err in the format the caller asked for.
//
// Under `-o json` it writes the document and ends the process rather than
// returning: azd writes the error it is handed to stdout, not stderr, so
// returning this one would append prose after the document and leave the stream
// unparseable by the caller that asked for it. The bare binary puts that line on
// stderr, which is why this only shows up when run through azd.
//
// Ending here costs the structured report azd would have made, and the exit code
// is the 1 azd collapses an extension's failure to anyway.
func failAs(cmd *cobra.Command, err error) error {
	if err == nil || !wantsJSON(cmd) {
		return err
	}
	_ = emitJSON(cmd.OutOrStdout(), jsonError{Error: jsonErrorBody{
		Message:    err.Error(),
		Suggestion: azdext.ErrorSuggestion(err),
	}})
	exitProcess(1)
	return err
}

// outputFromRawArgs re-reads -o/--output straight from the arguments.
//
// pflag stops at the first thing it cannot parse, so `--typo -o json` never
// recorded the format the caller asked for -- and a malformed invocation is
// exactly when a script needs to be answered in the format it can read. Without
// this the answer depended on whether -o came before or after the mistake.
//
// Unknown flags are skipped rather than rejected: this is looking for one flag,
// not judging the line.
func outputFromRawArgs(args []string) string {
	fs := pflag.NewFlagSet("output-probe", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist = pflag.ParseErrorsWhitelist{UnknownFlags: true}
	fs.SetOutput(io.Discard)
	out := fs.StringP("output", "o", "", "")
	_ = fs.Parse(args)
	return *out
}

// answeredWriter remembers whether anything reached the caller.
//
// Under `-o json` everything written to stdout is a document -- prose is
// dropped rather than printed -- so having written at all means the caller is
// already receiving one.
type answeredWriter struct {
	w        io.Writer
	answered bool
}

func (a *answeredWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		a.answered = true
	}
	return a.w.Write(p)
}

// reportFailuresAsJSON makes every command answer a failure in the format the
// caller asked for, not just a success.
//
// Wrapped after the tree is assembled rather than at each RunE, so a command
// added later cannot forget.
func reportFailuresAsJSON(root *cobra.Command) {
	// A misspelled flag is rejected during parsing, before any hook a command
	// owns. Cobra looks this up through the parent chain, so setting it on the
	// root covers the tree. An unknown *command* is the one mistake left with no
	// answer: cobra resolves the command before it has one to ask.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return failAs(cmd, err)
	})

	var wrap func(*cobra.Command)
	wrap = func(c *cobra.Command) {
		// The hook runs before Args and RunE, so a failure there is a failure of
		// the command the caller typed and owes them the same answer. `--cwd`
		// naming a directory that is not there was reported as prose to a caller
		// who had asked for json, because this ran before anything below could
		// wrap it.
		if preRun := c.PersistentPreRunE; preRun != nil {
			c.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
				return failAs(cmd, preRun(cmd, args))
			}
		}
		// Argument validation runs instead of RunE, not before it, so a wrapper
		// around RunE alone never sees it. An unquoted shell variable holding a
		// name with spaces arrives as several arguments and fails here -- a
		// scripting mistake, reported to a script, which is the case that most
		// needs an answer it can read.
		if validate := c.Args; validate != nil {
			c.Args = func(cmd *cobra.Command, args []string) error {
				return failAs(cmd, validate(cmd, args))
			}
		}
		if run := c.RunE; run != nil {
			c.RunE = func(cmd *cobra.Command, args []string) error {
				answered := &answeredWriter{w: cmd.OutOrStdout()}
				cmd.SetOut(answered)
				defer cmd.SetOut(answered.w)

				err := run(cmd, args)
				if err == nil {
					return err
				}
				if answered.answered && wantsJSON(cmd) {
					// The document is already on its way, so the reason cannot
					// join it on stdout without breaking it -- and stdout is
					// where azd would put it.
					_, _ = io.WriteString(cmd.ErrOrStderr(), "Error: "+err.Error()+"\n")
					exitProcess(1)
					return err
				}
				return failAs(cmd, err)
			}
		}
		for _, sub := range c.Commands() {
			wrap(sub)
		}
	}
	wrap(root)
}
