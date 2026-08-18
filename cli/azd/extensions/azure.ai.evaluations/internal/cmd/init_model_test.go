// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectWithDeployments builds a project whose Foundry project service
// declares the given deployments, which is the shape azd hands the extension
// for a `deployments:` list in azure.yaml.
func projectWithDeployments(t *testing.T, names ...string) *azdext.ProjectConfig {
	t.Helper()
	declared := make([]any, 0, len(names))
	for _, n := range names {
		declared = append(declared, map[string]any{
			"name":  n,
			"model": map[string]any{"name": n, "format": "OpenAI", "version": "1"},
		})
	}
	proj := projectWith()
	proj.Services["ai-project"] = &azdext.ServiceConfig{
		Name: "ai-project",
		Host: aiProjectHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"deployments": declared,
		}),
	}
	return proj
}

// The deployments live under the Foundry project service, which is where the
// sibling extensions put them. Reading them is a file read, so `init` keeps
// making no service calls.
func TestModelDeployments_ReadFromTheProjectService(t *testing.T) {
	assert.Empty(t, modelDeployments(projectWith("api", "web")),
		"a project with no Foundry project service declares no deployments")

	assert.Equal(t, []string{"gpt-4.1-nano", "gpt-4o-mini"},
		modelDeployments(projectWithDeployments(t, "gpt-4o-mini", "gpt-4.1-nano")),
		"sorted, so a prompt and an error list them the same way twice")
}

// A single declared deployment is the one to judge with, so the common project
// needs no flag at all.
func TestResolveJudgeModel_DetectsTheOnlyDeployment(t *testing.T) {
	model, err := resolveJudgeModel(newInitCommand(),
		projectWithDeployments(t, "gpt-4.1-nano"))

	require.NoError(t, err)
	assert.Equal(t, "gpt-4.1-nano", model)
}

// The judging built-ins declare the deployment as required, so a project that
// declares none has to say which to use. Failing here is the point: `init` used
// to exit 0 having written a configuration the service later rejects.
func TestResolveJudgeModel_NoDeploymentsNamesTheFlag(t *testing.T) {
	_, err := resolveJudgeModel(newInitCommand(), projectWith("api", "web"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--judge-model")
}

// With several there is nothing to detect. Under --no-prompt the flag is the
// only way to say which, so the error names it and lists the candidates.
func TestResolveJudgeModel_AmbiguousUnderNoPrompt(t *testing.T) {
	cmd := newInitCommand()
	// --no-prompt is inherited from the root in the real tree, so the test has
	// to supply it the way the root does.
	cmd.Flags().Bool("no-prompt", true, "")

	_, err := resolveJudgeModel(cmd, projectWithDeployments(t, "gpt-4.1-nano", "gpt-4o-mini"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--judge-model")
	assert.Contains(t, err.Error(), "gpt-4.1-nano")
	assert.Contains(t, err.Error(), "gpt-4o-mini")
}
