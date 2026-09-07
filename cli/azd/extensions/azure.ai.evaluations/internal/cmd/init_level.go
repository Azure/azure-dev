// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"slices"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// evaluationLevels are the two things a sample can be, in the order asked.
var evaluationLevels = []string{
	project.EvaluationLevelTurn,
	project.EvaluationLevelConversation,
}

// resolveEvaluationLevel settles what one evaluated sample represents.
//
// init wrote `turn` unconditionally, which is the right default and the wrong
// silence: a conversation-level eval was reachable only by editing the file
// afterwards, and nothing in the command said the choice existed.
func resolveEvaluationLevel(cmd *cobra.Command, flag string) (string, error) {
	if given := strings.ToLower(strings.TrimSpace(flag)); given != "" {
		if !slices.Contains(evaluationLevels, given) {
			return "", messages.EvaluationLevelNotAChoice(flag, evaluationLevels)
		}
		return given, nil
	}
	if noPrompt(cmd) {
		return project.EvaluationLevelTurn, nil
	}
	return promptEvaluationLevel(cmd)
}

// promptEvaluationLevel asks what each sample should represent.
func promptEvaluationLevel(cmd *cobra.Command) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectEvaluationLevelPrompt(),
			Choices: []*azdext.SelectChoice{
				{
					Label: messages.EvaluationLevelChoice(project.EvaluationLevelTurn),
					Value: project.EvaluationLevelTurn,
				},
				{
					Label: messages.EvaluationLevelChoice(project.EvaluationLevelConversation),
					Value: project.EvaluationLevelConversation,
				},
			},
		},
	})
	if err != nil {
		return "", messages.SelectingEvaluationLevel(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from GetValue
	// and would read as a deliberate answer of "turn" rather than as no answer.
	// Turn is the documented default, so it is what an unanswered prompt means.
	if resp == nil || resp.Value == nil {
		return project.EvaluationLevelTurn, nil
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(evaluationLevels) {
		return project.EvaluationLevelTurn, nil
	}
	return evaluationLevels[index], nil
}
