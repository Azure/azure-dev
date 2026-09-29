// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// promptAgentInstruction lets the reader type instructions or load an existing file.
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
func promptAgentInstruction(cmd *cobra.Command) (instruction, source string, err error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	selection, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectInstructionSourcePrompt(),
			Choices: []*azdext.SelectChoice{
				{Label: messages.TypeInstructionsChoice(), Value: "type"},
				{Label: messages.LoadInstructionsChoice(), Value: "file"},
			},
			SelectedIndex:   preselect(0),
			EnableFiltering: filteringFor(2),
		},
	})
	if err != nil {
		return "", "", messages.AskingForAgentInstruction(err)
	}
	if selection == nil || selection.Value == nil || selection.GetValue() < 0 || selection.GetValue() > 1 {
		return "", "", messages.InstructionsRequired()
	}
	options := &azdext.PromptOptions{
		Message:         messages.EnterAgentInstructionPrompt(),
		HelpMessage:     messages.EnterAgentInstructionHelp(),
		Placeholder:     "A friendly assistant that answers user questions briefly and clearly.",
		Required:        true,
		RequiredMessage: messages.AgentInstructionIsRequired(),
	}
	fromFile := selection.GetValue() == 1
	if fromFile {
		options.Message = messages.EnterInstructionFilePrompt()
		options.HelpMessage = messages.EnterInstructionFileHelp()
		options.Placeholder = "./instructions.txt"
	}
	resp, err := azdClient.Prompt().Prompt(commandContext(cmd), &azdext.PromptRequest{
		Options: options,
	})
	if err != nil {
		return "", "", messages.AskingForAgentInstruction(err)
	}
	// Required is the host's rule and this is ours: a blank answer is the
	// question unanswered, and submitting on it bills the job this prompt
	// exists to make worth running.
	if resp == nil || strings.TrimSpace(resp.GetValue()) == "" {
		return "", "", messages.InstructionsRequired()
	}
	answer := strings.TrimSpace(resp.GetValue())
	if fromFile {
		text, err := resolveInstruction("", answer)
		if err != nil {
			return "", "", err
		}
		return text, messages.InstructionSourceFile(answer), nil
	}
	return answer, messages.InstructionSourceTyped(), nil
}
