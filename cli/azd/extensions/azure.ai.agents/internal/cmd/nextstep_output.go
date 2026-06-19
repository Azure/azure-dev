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

	// w is an interactive terminal here, so opt into command highlighting;
	// it still self-gates on NO_COLOR inside the output package.
	return nextstep.PrintNext(w, suggestions, true)
}

func printAllNextIfTerminal(w io.Writer, suggestions []nextstep.Suggestion) error {
	if len(suggestions) == 0 || !writerIsTerminal(w) {
		return nil
	}

	return nextstep.PrintAllNext(w, suggestions, true)
}
