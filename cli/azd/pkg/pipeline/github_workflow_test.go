// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
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
		IsTerraform:       false,
		BranchName:        "main",
		AuthType:          AuthTypeFederated,
		GitHubEnvironment: "development",
	}
	contents, err := renderPipelineDefinition(props)
	require.NoError(t, err)
	require.Contains(t, string(contents), "type: environment")
	require.Contains(t, string(contents), `default: "development"`)
	require.Contains(t, string(contents), `environment: "${{ inputs.environment || 'development' }}"`)

	props.GitHubEnvironment = ""
	contents, err = renderPipelineDefinition(props)
	require.NoError(t, err)
	require.NotContains(t, string(contents), "type: environment")
	require.NotContains(t, string(contents), "environment: ${{")
}

func TestRenderPipelineDefinitionQuotesGitHubEnvironmentExpression(t *testing.T) {
	t.Parallel()

	for _, environmentName := range []string{"prod: west", "prod #1", `team's "prod"`} {
		t.Run(environmentName, func(t *testing.T) {
			t.Parallel()

			contents, err := renderPipelineDefinition(projectProperties{
				CiProvider:        ciProviderGitHubActions,
				BranchName:        "main",
				AuthType:          AuthTypeFederated,
				GitHubEnvironment: environmentName,
			})
			require.NoError(t, err)
			_, err = analyzeGitHubWorkflow(contents)
			require.NoError(t, err)
		})
	}
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
        required: true
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
			unsafe:              []string{"deploy"},
			expectedEnvironment: "${{ inputs.environment }}",
			expectedDynamic:     true,
		},
		{
			name: "optional dispatch input is unsafe",
			workflow: `
on:
  workflow_dispatch:
    inputs:
      environment:
        type: environment
jobs:
  deploy:
    environment: ${{ inputs.environment }}
    steps:
      - run: azd deploy --no-prompt
`,
			environmentInput:    true,
			unsafe:              []string{"deploy"},
			expectedEnvironment: "${{ inputs.environment }}",
			expectedDynamic:     true,
		},
		{
			name: "event input is unsafe for reusable workflow",
			workflow: `
on:
  workflow_call:
    inputs:
      environment:
        type: string
        required: true
jobs:
  deploy:
    environment: ${{ github.event.inputs.environment }}
    steps:
      - run: azd deploy --no-prompt
`,
			environmentInput:    true,
			unsafe:              []string{"deploy"},
			expectedEnvironment: "${{ github.event.inputs.environment }}",
			expectedDynamic:     true,
		},
		{
			name: "azd update is not a deployment command",
			workflow: `
on: push
jobs:
  update:
    steps:
      - run: azd update
`,
			automatic: true,
		},
		{
			name: "aliased environment",
			workflow: `
on: push
jobs:
  first:
    environment: &deploy_env production
    steps:
      - run: azd deploy --no-prompt
  second:
    environment: *deploy_env
    steps:
      - run: azd  provision --no-prompt
`,
			automatic:           true,
			expectedEnvironment: "production",
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
			if test.name == "azd update is not a deployment command" {
				require.Empty(t, analysis.deploymentJobs)
				return
			}
			if test.name == "aliased environment" {
				require.Len(t, analysis.deploymentJobs, 2)
				require.Equal(t, "production", analysis.deploymentJobs[1].environment)
			} else {
				require.Len(t, analysis.deploymentJobs, 1)
			}
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

func TestGitHubWorkflowCanReachEnvironment(t *testing.T) {
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
	require.True(t, githubWorkflowCanReachEnvironment(analysis, "development"))
	require.True(t, githubWorkflowCanReachEnvironment(analysis, "production"))
	require.True(t, githubWorkflowCanReachEnvironment(analysis, "staging"))
	require.True(t, githubWorkflowCanReachEnvironment(analysis, "qa"))
}

func TestValidateGitHubWorkflowUpgradesLegacyGeneratedWorkflowWithCRLF(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "azure-dev.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(workflowPath), osutil.PermissionDirectory))
	props := projectProperties{
		CiProvider:        ciProviderGitHubActions,
		RepoRoot:          repoRoot,
		BranchName:        "main",
		AuthType:          AuthTypeFederated,
		GitHubEnvironment: "development",
	}
	legacyProps := props
	legacyProps.GitHubEnvironment = ""
	legacyContents, err := renderPipelineDefinition(legacyProps)
	require.NoError(t, err)
	legacyContents = []byte(strings.ReplaceAll(string(legacyContents), "\n", "\r\n"))
	require.NoError(t, os.WriteFile(workflowPath, legacyContents, osutil.PermissionFile))

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.DefaultValue == true
	}).Respond(true)
	manager := &PipelineManager{console: mockContext.Console, env: environment.New("test")}

	require.NoError(t, manager.validateGitHubWorkflow(t.Context(), props))
}

func TestValidateGitHubWorkflowScopes(t *testing.T) {
	t.Parallel()

	t.Run("repository scope rejects environment jobs", func(t *testing.T) {
		t.Parallel()

		manager, props := newGitHubWorkflowValidationTest(
			t,
			`
on: push
jobs:
  deploy:
    environment: production
    steps:
      - run: azd deploy --no-prompt
`,
			"",
		)
		err := manager.validateGitHubWorkflow(t.Context(), props)
		require.ErrorContains(t, err, "repository-scoped variables and secrets")
	})

	t.Run("selected environment only needs one reachable job", func(t *testing.T) {
		t.Parallel()

		manager, props := newGitHubWorkflowValidationTest(
			t,
			`
on: push
jobs:
  development:
    environment: development
    steps:
      - run: azd deploy --no-prompt
  production:
    environment: production
    steps:
      - run: azd deploy --no-prompt
`,
			"production",
		)
		require.NoError(t, manager.validateGitHubWorkflow(t.Context(), props))
	})

	t.Run("custom workflow can be confirmed interactively", func(t *testing.T) {
		t.Parallel()

		repoRoot := t.TempDir()
		customWorkflow := filepath.Join(repoRoot, ".github", "workflows", "deploy.yml")
		require.NoError(t, os.MkdirAll(filepath.Dir(customWorkflow), osutil.PermissionDirectory))
		require.NoError(t, os.WriteFile(customWorkflow, []byte("name: deploy\n"), osutil.PermissionFile))
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "configured GitHub environment")
		}).Respond(true)
		manager := &PipelineManager{console: mockContext.Console, env: environment.New("test")}
		props := projectProperties{
			CiProvider:        ciProviderGitHubActions,
			RepoRoot:          repoRoot,
			GitHubEnvironment: "production",
		}

		require.NoError(t, manager.validateGitHubWorkflow(t.Context(), props))
	})
}

func newGitHubWorkflowValidationTest(
	t *testing.T,
	workflow string,
	environmentName string,
) (*PipelineManager, projectProperties) {
	t.Helper()

	repoRoot := t.TempDir()
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "azure-dev.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(workflowPath), osutil.PermissionDirectory))
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflow), osutil.PermissionFile))
	mockContext := mocks.NewMockContext(t.Context())
	manager := &PipelineManager{console: mockContext.Console, env: environment.New("test")}
	return manager, projectProperties{
		CiProvider:        ciProviderGitHubActions,
		RepoRoot:          repoRoot,
		GitHubEnvironment: environmentName,
	}
}

func TestValidateGitHubWorkflowUpgradesLegacyGeneratedWorkflow(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "azure-dev.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(workflowPath), osutil.PermissionDirectory))

	props := projectProperties{
		CiProvider:        ciProviderGitHubActions,
		RepoRoot:          repoRoot,
		IsTerraform:       false,
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
