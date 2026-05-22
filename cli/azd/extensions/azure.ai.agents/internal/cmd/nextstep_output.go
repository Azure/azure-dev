// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"

	"azureaiagent/internal/cmd/nextstep"
)

// writerIsTerminal reports whether w is interactive OS stdout.
func writerIsTerminal(w io.Writer) bool {
	return w == os.Stdout && stdoutIsTerminal()
}

func stdoutIsTerminal() bool {
	return isTerminal(os.Stdout.Fd())
}

func printNextIfTerminal(w io.Writer, suggestions []nextstep.Suggestion) error {
	if len(suggestions) == 0 || !writerIsTerminal(w) {
		return nil
	}

	return nextstep.PrintNext(w, suggestions)
}

func printAllNextIfTerminal(w io.Writer, suggestions []nextstep.Suggestion) error {
	if len(suggestions) == 0 || !writerIsTerminal(w) {
		return nil
	}

	return nextstep.PrintAllNext(w, suggestions)
}
