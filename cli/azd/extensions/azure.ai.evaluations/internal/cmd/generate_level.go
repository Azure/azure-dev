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

// resolveGenerationLevel settles what one generated row is.
//
// Generation only ever produced query-response pairs, so a conversation eval
// had no data to run against unless one was written by hand. The two shapes
// are not a formatting difference -- a conversation row is a seed a simulator
// drives, a turn row is a finished exchange -- so one invocation produces one
// of them, and which one is asked rather than assumed.
func resolveGenerationLevel(cmd *cobra.Command, flag string) (string, error) {
	if given := strings.ToLower(strings.TrimSpace(flag)); given != "" {
		if !slices.Contains(evaluationLevels, given) {
			return "", messages.EvaluationLevelNotAChoice(flag, evaluationLevels)
		}
		return given, nil
	}
	if noPrompt(cmd) || isJSON(cmd) {
		return project.EvaluationLevelTurn, nil
	}
	return promptGenerationLevel(cmd)
}

// promptGenerationLevel asks what the generated data has to support.
func promptGenerationLevel(cmd *cobra.Command) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	choices := make([]*azdext.SelectChoice, 0, len(evaluationLevels))
	for _, level := range evaluationLevels {
		choices = append(choices, &azdext.SelectChoice{
			Label: messages.GenerationLevelChoice(level),
			Value: level,
		})
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       messages.SelectGenerationLevelPrompt(),
			Choices:       choices,
			SelectedIndex: preselect(0),
		},
	})
	if err != nil {
		return "", messages.SelectingEvaluationLevel(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from GetValue
	// and would read as a deliberate Turn rather than as no answer. Turn is the
	// documented default, so both come to the same place.
	if resp == nil || resp.Value == nil {
		return project.EvaluationLevelTurn, nil
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(evaluationLevels) {
		return project.EvaluationLevelTurn, nil
	}
	return evaluationLevels[index], nil
}

// datasetNameSuffix names a generated dataset after what a row in it is.
//
// The old `-dataset` suffix said only that it was data. Two datasets generated
// from one agent at the two levels collided on that name, and the surviving
// file's name did not say which level it held.
func datasetNameSuffix(level string) string {
	if level == project.EvaluationLevelConversation {
		return "conversation-tests"
	}
	return "turn-tests"
}
