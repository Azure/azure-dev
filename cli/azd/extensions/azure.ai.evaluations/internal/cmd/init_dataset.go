// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// resolveDataset settles which dataset a dataset-backed evaluation grades.
//
// init used to fill this gap by declaring a dataset it would generate later,
// which wrote an eval nothing satisfied. Refusing instead is correct but ends
// the one command a developer runs first on an error, so where there is a
// person to ask, it asks: which of the declared datasets, or -- with none
// declared -- what to point at.
//
// Under --no-prompt there is nobody to ask, so the flag is named. The answer is
// only a proposal: `planScaffold` re-reads the configuration under the lock and
// validates whatever comes back, so a `generate` that landed in between is not
// overwritten by a menu drawn before it.
func resolveDataset(cmd *cobra.Command, cfg *project.EvalConfig, flag string) (string, error) {
	if strings.TrimSpace(flag) != "" {
		return flag, nil
	}

	declared := datasetNames(cfg)
	switch len(declared) {
	case 1:
		// The one declaration in the file is not a guess.
		return declared[0], nil
	case 0:
		if noPrompt(cmd) {
			return "", messages.DatasetSourceNeedsADataset()
		}
		return promptDatasetReference(cmd)
	}

	if noPrompt(cmd) {
		return "", messages.AmbiguousDeclaredDataset(declared)
	}
	return promptDeclaredDataset(cmd, declared)
}

// promptDeclaredDataset asks which of the configuration's datasets to grade.
func promptDeclaredDataset(cmd *cobra.Command, declared []string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	choices := make([]*azdext.SelectChoice, 0, len(declared))
	for i := range declared {
		choices = append(choices, &azdext.SelectChoice{Label: declared[i], Value: declared[i]})
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectDatasetPrompt(),
			Choices: choices,
		},
	})
	if err != nil {
		return "", messages.SelectingDataset(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from GetValue
	// and would read as the first dataset rather than as no answer.
	if resp == nil || resp.Value == nil {
		return "", messages.AmbiguousDeclaredDataset(declared)
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(declared) {
		return "", messages.AmbiguousDeclaredDataset(declared)
	}
	return declared[index], nil
}

// promptDatasetReference asks what to grade when the configuration declares
// nothing to offer.
//
// It takes a path or a registered name rather than a list, because the two
// things it could list are both service calls init does not make: the datasets
// registered in the project, and the files on disk that happen to be evaluation
// rows.
func promptDatasetReference(cmd *cobra.Command) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Prompt().Prompt(commandContext(cmd), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         messages.EnterDatasetPrompt(),
			HelpMessage:     messages.EnterDatasetHelp(),
			Placeholder:     "./evals/datasets/golden.jsonl",
			Required:        true,
			RequiredMessage: messages.DatasetIsRequired(),
		},
	})
	if err != nil {
		return "", messages.SelectingDataset(err)
	}
	// An empty answer is the question unanswered, and continuing on it would
	// write the reference that has no dataset behind it all over again.
	if resp == nil || strings.TrimSpace(resp.GetValue()) == "" {
		return "", messages.DatasetSourceNeedsADataset()
	}
	return strings.TrimSpace(resp.GetValue()), nil
}
