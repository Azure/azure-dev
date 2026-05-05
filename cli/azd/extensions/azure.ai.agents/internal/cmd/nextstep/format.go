// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

// PrintNext writes a "Next: …" block to w. The block matches the format
// described in the issue spec:
//
//	Next:  <primary command>            -- <description>
//	       <secondary command>          -- <description>
//
// Rules:
//   - Always prefixed with a blank line so the block is visually
//     separated from the command's normal output.
//   - The "Next:" label appears only once, in front of the first
//     suggestion. Subsequent lines are indented to align under the
//     first command.
//   - Commands are colored to make them copy-pasteable.
//   - When suggestions is empty, PrintNext writes nothing.
//
// PrintNext is safe to call with a nil writer-tolerant target — a nil
// io.Writer panics on first write, so callers must pass a real writer
// (typically os.Stdout).
func PrintNext(w io.Writer, suggestions []Suggestion) {
	if len(suggestions) == 0 {
		return
	}

	const label = "Next:"
	indent := strings.Repeat(" ", len(label)+2) // "Next:" + two spaces

	// Compute alignment width for "command  -- description".
	maxCmdWidth := 0
	for _, s := range suggestions {
		if l := len(s.Command); l > maxCmdWidth {
			maxCmdWidth = l
		}
	}

	cmdColor := color.New(color.FgHiBlue)

	fmt.Fprintln(w)
	for i, s := range suggestions {
		prefix := indent
		if i == 0 {
			prefix = label + "  "
		}
		// Color only the command token; pad with plain spaces so the
		// "  -- description" tail aligns regardless of color escapes.
		colored := cmdColor.Sprint(s.Command)
		padding := strings.Repeat(" ", maxCmdWidth-len(s.Command))
		if s.Description == "" {
			fmt.Fprintf(w, "%s%s\n", prefix, colored)
			continue
		}
		fmt.Fprintf(w, "%s%s%s  -- %s\n", prefix, colored, padding, s.Description)
	}
}
