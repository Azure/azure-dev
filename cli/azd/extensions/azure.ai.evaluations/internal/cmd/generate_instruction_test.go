// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/messages"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateCmd is a command carrying the flags the interactive paths read.
func generateCmd(t *testing.T, noPromptSet bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", noPromptSet, "")
	require.NoError(t, cmd.Flags().Set("no-prompt", boolText(noPromptSet)))
	return cmd
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Generation seeded from nothing comes back marked input_quality: the service
// says the input was insufficient, and the rubric it produced grades whatever
// it inferred. That is a billed job, so the absence has to stop it.
//
// It used to report "not detected" and carry on, which read as a note rather
// than a problem -- and the evaluator that came back was the one April found.
func TestGenerationRefusesToRunOnNoInstructionsUnderNoPrompt(t *testing.T) {
	ec := &evalContext{}
	var out bytes.Buffer

	_, _, err := ec.resolveGenerationInstruction(
		generateCmd(t, true), "", "", "", &out, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--agent-instruction")
	assert.Contains(t, err.Error(), "--agent-instruction-file",
		"which flag to reach for depends on whether the text is already in a file")
	assert.Contains(t, out.String(), "not detected",
		"the reader still gets told why they are being asked")
}

// An explicit instruction is the caller's own answer and skips every lookup,
// so the prompt cannot fire for someone who already supplied one.
func TestAnExplicitInstructionIsNeverSecondGuessed(t *testing.T) {
	ec := &evalContext{}
	var out bytes.Buffer

	got, source, err := ec.resolveGenerationInstruction(
		generateCmd(t, true), "grade politeness", "--agent-instruction", "", &out, false)

	require.NoError(t, err)
	assert.Equal(t, "grade politeness", got)
	assert.Equal(t, "--agent-instruction", source)
	assert.Empty(t, out.String(), "nothing to report when nothing was detected for")
}

// The typed answer is prose about what the agent does. Echoing it into the plan
// turns the confirmation into something the reader scrolls past to reach the
// two lines that say what will be billed.
func TestTypedInstructionsAreNamedRatherThanQuoted(t *testing.T) {
	assert.Equal(t, "entered interactively", messages.InstructionSourceTyped())
	assert.Equal(t, "entered interactively",
		messages.InstructionsPlanValue(messages.InstructionSourceTyped()))
}
