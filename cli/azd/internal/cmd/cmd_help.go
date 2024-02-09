package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// formatHelpNote provides the expected format in description notes using `•`.
func formatHelpNote(note string) string {
	return fmt.Sprintf("  • %s", note)
}

// generateCmdHelpDescription construct a help text block from a title and description notes.
func generateCmdHelpDescription(title string, notes []string) string {
	var note string
	if len(notes) > 0 {
		note = fmt.Sprintf("%s\n\n", strings.Join(notes, "\n"))
	}
	return fmt.Sprintf("%s\n\n%s", title, note)
}

// generateCmdHelpSamplesBlock converts the samples within the input `samples` to a help text block describing each sample
// title and the command to run it.
func generateCmdHelpSamplesBlock(samples map[string]string) string {
	SamplesCount := len(samples)
	if SamplesCount == 0 {
		return ""
	}
	var lines []string
	for title, command := range samples {
		lines = append(lines, fmt.Sprintf("  %s\n    %s", title, command))
	}
	// sorting lines to keep a deterministic output, as map[string]string is not ordered
	slices.Sort(lines)
	return fmt.Sprintf("%s\n%s\n",
		output.WithBold(output.WithUnderline("Examples")),
		strings.Join(lines, "\n\n"),
	)
}
