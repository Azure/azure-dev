// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// promptAgentInstruction asks what the agent is for.
//
// Generation seeded from nothing comes back marked input_quality: the service
// found the input insufficient, and the rubric it produced grades whatever it
// inferred. One sentence from the person who wrote the agent is the whole
// difference, and they are standing right there.
//
// The answer is not echoed back into the plan or the logs. It is prose about
// what the agent does, and reprinting it turns a confirmation screen into a
// wall of text the reader has to scroll past to find the two lines that say
// what will be billed.
func promptAgentInstruction(cmd *cobra.Command) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Prompt().Prompt(commandContext(cmd), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         messages.EnterAgentInstructionPrompt(),
			HelpMessage:     messages.EnterAgentInstructionHelp(),
			Placeholder:     "A friendly assistant that answers user questions briefly and clearly.",
			Required:        true,
			RequiredMessage: messages.AgentInstructionIsRequired(),
		},
	})
	if err != nil {
		return "", messages.AskingForAgentInstruction(err)
	}
	// Required is the host's rule and this is ours: a blank answer is the
	// question unanswered, and submitting on it bills the job this prompt
	// exists to make worth running.
	if resp == nil || strings.TrimSpace(resp.GetValue()) == "" {
		return "", messages.InstructionsRequired()
	}
	return strings.TrimSpace(resp.GetValue()), nil
}
