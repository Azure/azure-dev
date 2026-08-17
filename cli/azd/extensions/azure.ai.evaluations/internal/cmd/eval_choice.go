// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// chooseEvalIn is chooseEval for the run commands, which hold a directory
// rather than a loaded configuration. A configuration that will not open is
// left to the command that opens it properly, so the error stays the same one.
func chooseEvalIn(cmd *cobra.Command, evalDir, named string) string {
	if named != "" || noPrompt(cmd) {
		return named
	}
	cfg, err := project.OpenEvalConfig(evalDir)
	if err != nil {
		return named
	}
	return chooseEval(cmd, cfg, named)
}

// chooseEval settles which eval a command means when the caller named none.
//
// Refusing is right under --no-prompt, where there is nobody to ask. Standing
// at a terminal it is not: the command holds the whole candidate list, and the
// documented scenarios declare a second eval, so every bare `run start` after
// that would fail permanently.
//
// Returning the name empty leaves the existing error to the caller, which is
// what happens whenever the prompt cannot run.
func chooseEval(cmd *cobra.Command, cfg *project.EvalConfig, named string) string {
	if named != "" || cfg == nil || len(cfg.Evals) < 2 || noPrompt(cmd) {
		return named
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return named
	}
	defer azdClient.Close()

	names := cfg.EvalNames()
	choices := make([]*azdext.SelectChoice, 0, len(names))
	for i := range names {
		choices = append(choices, &azdext.SelectChoice{Label: names[i], Value: names[i]})
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectEvalPrompt(),
			Choices: choices,
		},
	})
	if err != nil {
		return named
	}
	// Value is optional on the wire, so an unset one arrives as 0 from
	// GetValue -- indistinguishable from the first choice. Reading it as a
	// selection would start a billed run against an eval nobody picked.
	if resp == nil || resp.Value == nil {
		return named
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(names) {
		return named
	}
	return names[index]
}
