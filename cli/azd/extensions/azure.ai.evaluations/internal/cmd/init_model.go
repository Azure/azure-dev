// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"sort"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// modelDeployments names every model deployment the project declares, sorted so
// a prompt and an error list the same way twice.
//
// They live under the Foundry project service's deployments:, which is where
// the sibling extensions put them, so reading them is a file read rather than a
// service call. An entry given as a $ref is skipped rather than followed:
// resolving includes is the project extension's job, and a scaffold that cannot
// see a deployment falls back to naming --judge-model, which is a worse message
// but never a wrong one.
func modelDeployments(proj *azdext.ProjectConfig) []string {
	seen := map[string]bool{}
	var names []string

	for _, svc := range proj.GetServices() {
		if svc.GetHost() != aiProjectHost {
			continue
		}
		props := svc.GetAdditionalProperties()
		if props == nil || len(props.GetFields()) == 0 {
			props = svc.GetConfig()
		}
		if props == nil {
			continue
		}
		declared, ok := props.AsMap()["deployments"].([]any)
		if !ok {
			continue
		}
		for _, entry := range declared {
			fields, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			name, ok := fields["name"].(string)
			if !ok || name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	return names
}

// resolveJudgeModel settles the deployment the graders judge with.
//
// The judging built-ins declare the deployment as required, so an eval written
// without one is rejected by the service long after the command that wrote it.
// That is why coming back empty is a failure here rather than something left
// for later: `init` would otherwise exit 0 having written a configuration that
// cannot be deployed.
func resolveJudgeModel(cmd *cobra.Command, proj *azdext.ProjectConfig) (string, error) {
	if model := detectModelDeployment(proj); model != "" {
		return model, nil
	}

	deployments := modelDeployments(proj)
	switch len(deployments) {
	case 0:
		return "", messages.JudgeModelRequired()
	case 1:
		return deployments[0], nil
	}

	if noPrompt(cmd) {
		return "", messages.AmbiguousJudgeModel(deployments)
	}
	return promptJudgeModel(cmd, deployments)
}

// promptJudgeModel asks which of the project's deployments to judge with.
func promptJudgeModel(cmd *cobra.Command, deployments []string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	choices := make([]*azdext.SelectChoice, 0, len(deployments))
	for i := range deployments {
		choices = append(choices, &azdext.SelectChoice{
			Label: deployments[i], Value: deployments[i],
		})
	}

	resp, err := azdClient.Prompt().Select(cmd.Context(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectJudgeModelPrompt(),
			Choices: choices,
		},
	})
	if err != nil {
		return "", messages.SelectingJudgeModel(err)
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(deployments) {
		return "", messages.AmbiguousJudgeModel(deployments)
	}
	return deployments[index], nil
}
