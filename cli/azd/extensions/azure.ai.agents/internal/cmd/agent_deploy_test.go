// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentDeployCommandRegistersDryRunOptions(t *testing.T) {
	t.Parallel()

	command := newAgentDeployCommand(&azdext.ExtensionContext{})

	assert.NotNil(t, command.Flags().Lookup("dry-run"))
	assert.NotNil(t, command.Flags().Lookup("service"))
	assert.Contains(t, command.Long, "azure.yaml")
	assert.Contains(t, command.Long, "never deploys an agent")
}

func TestAgentDeployCommandRequiresDryRun(t *testing.T) {
	t.Parallel()

	command := newAgentDeployCommand(&azdext.ExtensionContext{})
	command.SetArgs(nil)

	err := command.Execute()
	require.Error(t, err)
	assert.ErrorContains(t, err, "only supports deployment previews")
}

func TestLegacyAgentDefinitionInCurrentDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	require.NoError(t, os.WriteFile("agent.yaml", []byte("kind: hosted\n"), 0o600))
	require.NoError(t, os.WriteFile("agent.manifest.yaml", []byte("name: sample\n"), 0o600))

	assert.False(t, projectFileExistsInCurrentDirectory())
	assert.Equal(t, "agent.yaml", legacyAgentDefinitionInCurrentDirectory())

	require.NoError(t, os.WriteFile("azure.yaml", []byte("name: sample\n"), 0o600))
	assert.True(t, projectFileExistsInCurrentDirectory())
}

func TestWriteAgentDeployPlanNoChanges(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := writeAgentDeployPlan(&output, &project.AgentDeployPlan{
		Agent:            "existing-agent",
		Action:           "createVersion",
		RemoteComparison: "compared",
		Source:           "azure.yaml",
	})
	require.NoError(t, err)
	assert.Contains(t, output.String(), "No configuration changes.")
	assert.Contains(t, output.String(), "Deploy would still create a new immutable agent version.")
	assert.Contains(t, output.String(), "Dry run complete. No changes were made.")
}
