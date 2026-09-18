// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// evalNameRetries bounds the re-ask loop. A person can stop by answering
// nothing, which takes the suggestion; the cap is for a prompt that answers
// without one.
const evalNameRetries = 8

// resolveEvalName settles what the new eval is called.
//
// The name was never a question: it was --name or a derived default, and a
// --name the file already used ended the command, so the way out of a
// collision was to rerun and answer everything again. init adds evals and
// never replaces one, so a taken name has to be refused -- but refusing it in
// place and asking for another costs the reader nothing they have already
// answered.
func resolveEvalName(
	cmd *cobra.Command,
	cfg *project.EvalConfig,
	configPath, explicit, suggested string,
) (string, error) {
	if noPrompt(cmd) || isJSON(cmd) {
		if explicit == "" {
			return suggested, nil
		}
		// Nobody to ask, so a name that cannot be used is the end of it.
		if err := evalNameUsable(cfg, configPath, explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	return promptEvalName(cmd, cfg, configPath, explicit, suggested)
}

// promptEvalName asks for a name and keeps asking until one can be used.
func promptEvalName(
	cmd *cobra.Command,
	cfg *project.EvalConfig,
	configPath, explicit, suggested string,
) (string, error) {
	out := cmd.OutOrStdout()
	proposed := suggested

	if explicit != "" {
		// Their name is the answer. Reading a usable one back to them would be
		// asking a question they already answered on the command line.
		problem := evalNameUsable(cfg, configPath, explicit)
		if problem == nil {
			return explicit, nil
		}
		fmt.Fprint(out, messages.EvalNameRejected(problem))
		proposed = uniqueEvalName(cfg, trimEvalName(explicit, 0))
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	for range evalNameRetries {
		resp, err := azdClient.Prompt().Prompt(commandContext(cmd), &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      messages.EvalNamePrompt(),
				HelpMessage:  messages.EvalNameHelp(),
				DefaultValue: proposed,
				Placeholder:  proposed,
			},
		})
		if err != nil {
			return "", messages.AskingForEvalName(err)
		}
		// An unanswered prompt takes the suggestion, which is why the
		// suggestion is made usable before it is offered.
		name := proposed
		if resp != nil && strings.TrimSpace(resp.GetValue()) != "" {
			name = strings.TrimSpace(resp.GetValue())
		}
		if problem := evalNameUsable(cfg, configPath, name); problem != nil {
			fmt.Fprint(out, messages.EvalNameRejected(problem))
			proposed = uniqueEvalName(cfg, trimEvalName(name, 0))
			continue
		}
		return name, nil
	}
	return "", messages.EvalNameStillUnusable()
}

// evalNameUsable reports why a name cannot be used, or nil when it can.
func evalNameUsable(cfg *project.EvalConfig, configPath, name string) error {
	if !validAssetName(name) {
		return messages.EvalNameNotAllowed(name)
	}
	if cfg.HasEval(name) {
		return messages.EvalAlreadyDeclared(name, filepath.ToSlash(configPath))
	}
	return nil
}
