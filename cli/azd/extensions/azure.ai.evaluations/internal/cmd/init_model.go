// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"sort"
	"strings"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// judgeModelEnvKey is the key `azd ai agent init` writes the bound deployment
// to, and the only place it appears when the project was created beforehand.
const judgeModelEnvKey = "AZURE_AI_MODEL_DEPLOYMENT_NAME"

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
		// Binding to an existing Foundry project writes `deployments: []` into
		// azure.yaml, so the deployment `azd ai agent init` chose survives only
		// in the azd environment. Reading it there is the difference between a
		// configured project working and erroring.
		if model := modelDeploymentFromAzdEnv(commandContext(cmd)); model != "" {
			return model, nil
		}
		return "", messages.JudgeModelRequired()
	case 1:
		return deployments[0], nil
	}

	if noPrompt(cmd) {
		return "", messages.AmbiguousJudgeModel(deployments)
	}
	return promptJudgeModel(cmd, deployments)
}

// tracesConnected reports whether the azd environment records an Application
// Insights connection, which is what `generate --from` already defaults on.
//
// A local read, so `init` keeps its promise to make no service calls. Absence
// is ordinary: init runs outside an azd project too.
func tracesConnected(ctx context.Context) bool {
	return azdEnvValue(ctx, appInsightsEnvKey) != ""
}

// modelDeploymentFromAzdEnv reads the deployment `azd ai agent init` recorded
// in the active azd environment. Absence is ordinary: `init` runs outside an
// azd project too, and the caller falls back to naming --judge-model.
func modelDeploymentFromAzdEnv(ctx context.Context) string {
	return azdEnvValue(ctx, judgeModelEnvKey)
}

// azdEnvValue reads one key from the azd environment this invocation acts on,
// answering empty whenever there is no daemon, no environment, or no such key.
func azdEnvValue(ctx context.Context, key string) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	envName := azdEnvironmentName(ctx, azdClient)
	if envName == "" {
		return ""
	}
	val, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     key,
	})
	if err != nil {
		return ""
	}
	return val.GetValue()
}

// validateEvaluatorRefs rejects references that cannot name an evaluator.
//
// This needs no service call, so it keeps `init`'s promise to make none. The
// alternative is a config that scaffolds cleanly and fails at `create`, naming
// a value the user passed to a different command.
func validateEvaluatorRefs(refs []string) error {
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return messages.EvaluatorRefEmpty()
		}
		if strings.ContainsAny(ref, " \t") {
			return messages.EvaluatorRefMalformed(ref)
		}
	}
	return nil
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

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectJudgeModelPrompt(),
			Choices: choices,
		},
	})
	if err != nil {
		return "", messages.SelectingJudgeModel(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from
	// GetValue and would read as the first deployment rather than as no answer.
	if resp == nil || resp.Value == nil {
		return "", messages.AmbiguousJudgeModel(deployments)
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(deployments) {
		return "", messages.AmbiguousJudgeModel(deployments)
	}
	return deployments[index], nil
}
