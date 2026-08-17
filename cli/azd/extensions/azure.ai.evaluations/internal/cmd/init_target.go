// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"sort"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// agentServices names every agent the project declares, sorted so a prompt and
// an error list the same way twice.
func agentServices(proj *azdext.ProjectConfig) []string {
	var names []string
	for name, svc := range proj.GetServices() {
		if svc.GetHost() == project.AgentHost {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// resolveAgentTarget settles which agent the scaffold is written for.
//
// The spec makes --target default to the project's only agent, so requiring it
// would put a flag in front of the one command a developer runs first. With
// several agents there is nothing to detect, so it asks; under --no-prompt it
// names the flag rather than guessing which agent someone meant.
//
// It does not announce what it found: init prints that for every target, so
// that an explicit --target and a detected one read the same.
func resolveAgentTarget(cmd *cobra.Command, proj *azdext.ProjectConfig) (string, error) {
	agents := agentServices(proj)
	switch len(agents) {
	case 0:
		return "", messages.NoAgentToEvaluate()
	case 1:
		return agents[0], nil
	}

	if noPrompt(cmd) {
		return "", messages.AmbiguousAgentTarget(agents)
	}
	return promptAgentTarget(cmd, agents)
}

// promptAgentTarget asks which of the project's agents to evaluate.
func promptAgentTarget(cmd *cobra.Command, agents []string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	choices := make([]*azdext.SelectChoice, 0, len(agents))
	for i := range agents {
		choices = append(choices, &azdext.SelectChoice{Label: agents[i], Value: agents[i]})
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectAgentPrompt(),
			Choices: choices,
		},
	})
	if err != nil {
		return "", messages.SelectingAgent(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from
	// GetValue and would read as the first agent rather than as no answer.
	if resp == nil || resp.Value == nil {
		return "", messages.AmbiguousAgentTarget(agents)
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(agents) {
		return "", messages.AmbiguousAgentTarget(agents)
	}
	return agents[index], nil
}
