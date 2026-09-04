// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestRenderPipelineDefinitionGitHubEnvironment(t *testing.T) {
	t.Parallel()

	props := projectProperties{
		CiProvider:        ciProviderGitHubActions,
		InfraProvider:     infraProviderBicep,
		BranchName:        "main",
		AuthType:          AuthTypeFederated,
		GitHubEnvironment: "development",
	}
	contents, err := renderPipelineDefinition(props)
	require.NoError(t, err)
	require.Contains(t, string(contents), "type: environment")
	require.Contains(t, string(contents), `default: "development"`)
	require.Contains(t, string(contents), "environment: ${{ inputs.environment || 'development' }}")

	props.GitHubEnvironment = ""
	contents, err = renderPipelineDefinition(props)
	require.NoError(t, err)
	require.NotContains(t, string(contents), "type: environment")
	require.NotContains(t, string(contents), "environment: ${{")
}

func TestAnalyzeGitHubWorkflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		workflow            string
		automatic           bool
		environmentInput    bool
		missing             []string
		unsafe              []string
		ambiguous           []string
		expectedEnvironment string
		expectedDynamic     bool
	}{
		{
			name: "generated environment workflow",
			workflow: `
on:
  workflow_dispatch:
    inputs:
      environment:
        type: environment
  push:
    branches: [main]
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: ${{ inputs.environment || 'development' }}
    steps:
      - run: azd provision --no-prompt
`,
			automatic:           true,
			environmentInput:    true,
			expectedEnvironment: "${{ inputs.environment || 'development' }}",
			expectedDynamic:     true,
		},
		{
			name: "missing environment",
			workflow: `
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - run: azd up --no-prompt
`,
			automatic: true,
			missing:   []string{"deploy"},
		},
		{
			name: "unsafe automatic input",
			workflow: `
on:
  workflow_dispatch:
    inputs:
      environment:
        type: environment
  push:
    branches: [main]
jobs:
  deploy:
    environment: ${{ inputs.environment }}
    steps:
      - run: azd deploy --no-prompt
`,
			automatic:           true,
			environmentInput:    true,
			unsafe:              []string{"deploy"},
			expectedEnvironment: "${{ inputs.environment }}",
			expectedDynamic:     true,
		},
		{
			name: "static environment mapping form",
			workflow: `
on:
  workflow_dispatch:
jobs:
  deploy:
    environment:
      name: production
    steps:
      - run: azd provision --no-prompt
`,
			expectedEnvironment: "production",
		},
		{
			name: "ambiguous expression",
			workflow: `
on: push
jobs:
  deploy:
    environment: ${{ matrix.environment }}
    steps:
      - run: azd deploy --no-prompt
`,
			automatic:           true,
			ambiguous:           []string{"deploy"},
			expectedEnvironment: "${{ matrix.environment }}",
			expectedDynamic:     true,
		},
		{
			name: "dynamic non-literal fallback",
			workflow: `
on:
  workflow_dispatch:
    inputs:
      environment:
        type: environment
  push:
jobs:
  deploy:
    environment: ${{ inputs.environment || vars.DEFAULT_ENVIRONMENT }}
    steps:
      - run: azd deploy --no-prompt
`,
			automatic:           true,
			environmentInput:    true,
			ambiguous:           []string{"deploy"},
			expectedEnvironment: "${{ inputs.environment || vars.DEFAULT_ENVIRONMENT }}",
			expectedDynamic:     true,
		},
		{
			name: "reusable workflow input",
			workflow: `
on:
  workflow_call:
    inputs:
      environment:
        type: string
        required: true
jobs:
  deploy:
    environment: ${{ inputs.environment }}
    steps:
      - run: azd deploy --no-prompt
`,
			environmentInput:    true,
			expectedEnvironment: "${{ inputs.environment }}",
			expectedDynamic:     true,
		},
		{
			name: "legacy event input is unsafe for automatic trigger",
			workflow: `
on:
  workflow_dispatch:
    inputs:
      environment:
        type: environment
  push:
jobs:
  deploy:
    environment: ${{ github.event.inputs.environment }}
    steps:
      - run: azd deploy --no-prompt
`,
			automatic:           true,
			environmentInput:    true,
			unsafe:              []string{"deploy"},
			expectedEnvironment: "${{ github.event.inputs.environment }}",
			expectedDynamic:     true,
		},
		{
			name: "undeclared environment input is ambiguous",
			workflow: `
on:
  workflow_dispatch:
jobs:
  deploy:
    environment: ${{ inputs.environment }}
    steps:
      - run: azd deploy --no-prompt
`,
			ambiguous:           []string{"deploy"},
			expectedEnvironment: "${{ inputs.environment }}",
			expectedDynamic:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			analysis, err := analyzeGitHubWorkflow([]byte(test.workflow))
			require.NoError(t, err)
			require.Equal(t, test.automatic, analysis.hasAutomaticTrigger)
			require.Equal(t, test.environmentInput, analysis.hasEnvironmentInput)
			require.Equal(t, test.missing, analysis.missingEnvironmentJobs)
			require.Equal(t, test.unsafe, analysis.unsafeEnvironmentJobs)
			require.Equal(t, test.ambiguous, analysis.ambiguousEnvironmentJobs)
			require.Len(t, analysis.deploymentJobs, 1)
			require.Equal(t, test.expectedEnvironment, analysis.deploymentJobs[0].environment)
			require.Equal(t, test.expectedDynamic, analysis.deploymentJobs[0].dynamic)
		})
	}
}

func TestGitHubWorkflowEnvironmentFallback(t *testing.T) {
	t.Parallel()

	fallback, ok := githubWorkflowEnvironmentFallback(
		"${{ inputs.environment || 'production' }}",
	)
	require.True(t, ok)
	require.Equal(t, "production", fallback)

	fallback, ok = githubWorkflowEnvironmentFallback(
		"${{ inputs.environment || 'team''s production' }}",
	)
	require.True(t, ok)
	require.Equal(t, "team's production", fallback)

	_, ok = githubWorkflowEnvironmentFallback(
		"${{ github.ref == 'refs/heads/main' && 'production' || 'development' }}",
	)
	require.False(t, ok)
}

func TestGitHubWorkflowIncompatibleJobs(t *testing.T) {
	t.Parallel()

	analysis, err := analyzeGitHubWorkflow([]byte(`
on:
  workflow_dispatch:
    inputs:
      environment:
        type: environment
  push:
jobs:
  matching:
    environment: ${{ inputs.environment || 'development' }}
    steps:
      - run: azd provision --no-prompt
  wrong-static:
    environment: production
    steps:
      - run: azd deploy --no-prompt
  wrong-fallback:
    environment: ${{ inputs.environment || 'staging' }}
    steps:
      - run: azd deploy --no-prompt
`))
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{`wrong-static ("production")`, `wrong-fallback (fallback "staging")`},
		githubWorkflowIncompatibleJobs(analysis, "development"),
	)
}

func TestValidateGitHubWorkflowUpgradesLegacyGeneratedWorkflow(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "azure-dev.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(workflowPath), osutil.PermissionDirectory))

	props := projectProperties{
		CiProvider:        ciProviderGitHubActions,
		RepoRoot:          repoRoot,
		InfraProvider:     infraProviderBicep,
		BranchName:        "main",
		AuthType:          AuthTypeFederated,
		GitHubEnvironment: "development",
	}
	legacyProps := props
	legacyProps.GitHubEnvironment = ""
	legacyContents, err := renderPipelineDefinition(legacyProps)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(workflowPath, legacyContents, osutil.PermissionFile))

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.DefaultValue == true
	}).Respond(true)
	manager := &PipelineManager{
		console: mockContext.Console,
		env:     environment.New("test"),
	}

	require.NoError(t, manager.validateGitHubWorkflow(t.Context(), props))

	expected, err := renderPipelineDefinition(props)
	require.NoError(t, err)
	actual, err := os.ReadFile(workflowPath)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}
