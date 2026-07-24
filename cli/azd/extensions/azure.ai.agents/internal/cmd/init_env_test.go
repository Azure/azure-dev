// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindAzureYamlEnvironmentReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    []azureYamlEnvironmentReference
		wantErr bool
	}{
		{
			name: "finds issue references and classifies credential as secret",
			content: `name: playwright-agent
services:
  playwright:
    host: azure.ai.connection
    credentials:
      key: '${PLAYWRIGHT_SERVICE_ACCESS_TOKEN}'
      connectionString: '${DATABASE_CONNECTION_STRING}'
    metadata:
      resourceId: '${PLAYWRIGHT_SERVICE_RESOURCE_ID}'
`,
			want: []azureYamlEnvironmentReference{
				{Name: "DATABASE_CONNECTION_STRING", Secret: true},
				{Name: "PLAYWRIGHT_SERVICE_ACCESS_TOKEN", Secret: true},
				{Name: "PLAYWRIGHT_SERVICE_RESOURCE_ID"},
			},
		},
		{
			name: "deduplicates and upgrades secret classification",
			content: `name: sample
services:
  connection:
    host: azure.ai.connection
    metadata:
      resourceId: ${SHARED_VALUE}
    credentials:
      key: ${SHARED_VALUE}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "SHARED_VALUE", Secret: true},
			},
		},
		{
			name: "ignores defaults foundry templates comments and escaped references",
			content: `name: sample
# ${COMMENT_ONLY}
services:
  agent:
    host: azure.ai.agent
    kind: hosted
    environmentVariables:
      - name: DEFAULTED
        value: ${DEFAULTED:-fallback}
      - name: FOUNDRY
        value: ${{connections.search.credentials.key}}
      - name: MULTILINE_FOUNDRY
        value: |
         ${{ event.body
           ?? '${FOUNDRY_INNER_VALUE}'
         }}
      - name: ESCAPED
        value: $${ESCAPED}
      - name: EXPANDED_AFTER_LITERAL_DOLLAR
        value: $$${EXPANDED_AFTER_LITERAL_DOLLAR}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "EXPANDED_AFTER_LITERAL_DOLLAR"},
			},
		},
		{
			name: "does not infer secrets from variable names outside credential paths",
			content: `name: sample
services:
  agent:
    host: azure.ai.agent
    kind: hosted
    environmentVariables:
      - name: API_TOKEN
        value: ${SERVICE_API_TOKEN}
      - name: DATABASE_CONNECTION_STRING
        value: ${DATABASE_CONNECTION_STRING}
      - name: ENDPOINT
        value: ${SERVICE_ENDPOINT}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "SERVICE_API_TOKEN"},
				{Name: "DATABASE_CONNECTION_STRING"},
				{Name: "SERVICE_ENDPOINT"},
			},
		},
		{
			name: "ignores project hooks service hooks and unrelated services",
			content: `name: sample
hooks:
  preprovision:
    shell: sh
    run: echo ${PROJECT_HOOK_VALUE}
services:
  web:
    host: containerapp
    env:
      WEB_VALUE: ${WEB_VALUE}
  agent:
    host: azure.ai.agent
    kind: hosted
    hooks:
      predeploy:
        shell: sh
        run: echo ${SERVICE_HOOK_VALUE}
    environmentVariables:
      - name: AGENT_VALUE
        value: ${AGENT_VALUE}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "AGENT_VALUE"},
			},
		},
		{
			name: "project scans only expanded network fields",
			content: `name: sample
services:
  project:
    host: azure.ai.project
    endpoint: ${RAW_PROJECT_ENDPOINT}
    deployments:
      - name: ${RAW_DEPLOYMENT_NAME}
        model:
          name: ${RAW_MODEL_NAME}
        sku:
          name: ${RAW_SKU_NAME}
    network:
      agentSubnet:
        vnet: $${AGENT_VNET_ID}
      peSubnet:
        vnet: ${PE_VNET_ID}
      dns:
        subscription: ${DNS_SUBSCRIPTION_ID}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "AGENT_VNET_ID"},
				{Name: "PE_VNET_ID"},
				{Name: "DNS_SUBSCRIPTION_ID"},
			},
		},
		{
			name: "connection scans only target credentials and metadata",
			content: `name: sample
services:
  connection:
    host: azure.ai.connection
    category: ${RAW_CATEGORY}
    authType: ${RAW_AUTH_TYPE}
    target: ${CONNECTION_TARGET}
    credentials:
      key: ${CONNECTION_KEY}
    metadata:
      resourceId: ${CONNECTION_RESOURCE_ID}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "CONNECTION_TARGET"},
				{Name: "CONNECTION_KEY", Secret: true},
				{Name: "CONNECTION_RESOURCE_ID"},
			},
		},
		{
			name: "toolbox scans endpoint and tools but not description",
			content: `name: sample
services:
  toolbox:
    host: azure.ai.toolbox
    description: ${RAW_TOOLBOX_DESCRIPTION}
    endpoint: ${TOOLBOX_ENDPOINT}
    tools:
      - name: search
        configuration:
          key: ${TOOLBOX_KEY}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "TOOLBOX_ENDPOINT"},
				{Name: "TOOLBOX_KEY"},
			},
		},
		{
			name: "routine scans action input but not triggers or description",
			content: `name: sample
services:
  routine:
    host: azure.ai.routine
    description: ${RAW_ROUTINE_DESCRIPTION}
    triggers:
      - type: ${RAW_TRIGGER_TYPE}
    action:
      input:
        value: ${ROUTINE_INPUT}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "ROUTINE_INPUT"},
			},
		},
		{
			name: "skill fields are not expanded",
			content: `name: sample
services:
  skill:
    host: azure.ai.skill
    description: ${RAW_SKILL_DESCRIPTION}
    instructions: ${RAW_SKILL_INSTRUCTIONS}
`,
			want: nil,
		},
		{
			name: "unsupported host refs are ignored before resolution",
			content: `name: sample
services:
  skill:
    host: azure.ai.skill
    $ref: ./missing-skill.yaml
`,
			want: nil,
		},
		{
			name: "deprecated toolbox and routine config fields are scanned",
			content: `name: sample
services:
  agent:
    host: azure.ai.agent
    config:
      kind: hosted
      environmentVariables:
        - name: LEGACY_AGENT_VALUE
          value: ${LEGACY_AGENT_VALUE}
  routine:
    host: azure.ai.routine
    config:
      action:
        input:
          value: ${LEGACY_ROUTINE_INPUT}
  toolbox:
    host: azure.ai.toolbox
    config:
      endpoint: ${LEGACY_TOOLBOX_ENDPOINT}
      tools:
        - name: legacy
          configuration:
            key: ${LEGACY_TOOLBOX_KEY}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "LEGACY_AGENT_VALUE"},
				{Name: "LEGACY_ROUTINE_INPUT"},
				{Name: "LEGACY_TOOLBOX_ENDPOINT"},
				{Name: "LEGACY_TOOLBOX_KEY"},
			},
		},
		{
			name: "inline properties take precedence over stale config fields",
			content: `name: sample
services:
  agent:
    host: azure.ai.agent
    kind: hosted
    environmentVariables:
      - name: INLINE_AGENT_VALUE
        value: ${INLINE_AGENT_VALUE}
    config:
      kind: hosted
      environmentVariables:
        - name: STALE_AGENT_VALUE
          value: ${STALE_AGENT_VALUE}
  routine:
    host: azure.ai.routine
    description: inline routine
    action:
      input:
        value: ${INLINE_ROUTINE_INPUT}
    config:
      action:
        input:
          value: ${STALE_ROUTINE_INPUT}
  toolbox:
    host: azure.ai.toolbox
    description: inline toolbox
    endpoint: ${INLINE_TOOLBOX_ENDPOINT}
    config:
      endpoint: ${STALE_TOOLBOX_ENDPOINT}
      tools:
        - name: stale
          configuration:
            key: ${STALE_TOOLBOX_KEY}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "INLINE_AGENT_VALUE"},
				{Name: "INLINE_ROUTINE_INPUT"},
				{Name: "INLINE_TOOLBOX_ENDPOINT"},
			},
		},
		{
			name:    "malformed yaml",
			content: "name: [unterminated",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := findAzureYamlEnvironmentReferences([]byte(tt.content), ".")
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestConfigureAzureYamlEnvironmentVariables_ResolvesServiceRefs(t *testing.T) {
	t.Setenv("REFERENCED_API_TOKEN", "")
	t.Setenv("ROOT_RESOURCE_ID", "")

	projectDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(projectDir, "services"), 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "services", "connection.yaml"),
		[]byte(`credentials:
  key: ${REFERENCED_API_TOKEN}
metadata:
  resourceId: ${OVERRIDDEN_RESOURCE_ID}
`),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "azure.yaml"),
		[]byte(`name: sample
services:
  connection:
    host: azure.ai.connection
    $ref: ./services/connection.yaml
    metadata:
      resourceId: ${ROOT_RESOURCE_ID}
`),
		0600,
	))

	envServer := &testEnvironmentServiceServer{}
	promptServer := &testPromptServiceServer{
		promptResponses: []string{"api-token", "resource-id"},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(
		t.Context(),
		azdClient,
		"dev",
		projectDir,
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "api-token", envServer.values["dev"]["REFERENCED_API_TOKEN"])
	require.Equal(t, "resource-id", envServer.values["dev"]["ROOT_RESOURCE_ID"])
	require.NotContains(t, envServer.values["dev"], "OVERRIDDEN_RESOURCE_ID")
	require.Len(t, promptServer.promptRequests, 2)
	require.True(t, promptServer.promptRequests[0].Options.Secret)
}

func TestConfigureAzureYamlEnvironmentVariables_PromptsAndPersistsMissingValues(t *testing.T) {
	t.Setenv("PLAYWRIGHT_SERVICE_ACCESS_TOKEN", "")
	t.Setenv("PLAYWRIGHT_SERVICE_RESOURCE_ID", "")

	projectDir := t.TempDir()
	content := `name: playwright-agent
services:
  playwright:
    host: azure.ai.connection
    credentials:
      key: '${PLAYWRIGHT_SERVICE_ACCESS_TOKEN}'
    metadata:
      resourceId: '${PLAYWRIGHT_SERVICE_RESOURCE_ID}'
      existing: '${ALREADY_SET}'
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(content), 0600))
	t.Chdir(projectDir)

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"ALREADY_SET": "existing-value",
			},
		},
	}
	promptServer := &testPromptServiceServer{
		promptResponses: []string{"access-token", "/subscriptions/sub/resourceGroups/rg/providers/test"},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(
		t.Context(),
		azdClient,
		"dev",
		".",
		false,
	)
	require.NoError(t, err)

	require.Equal(t, "access-token", envServer.values["dev"]["PLAYWRIGHT_SERVICE_ACCESS_TOKEN"])
	require.Equal(
		t,
		"/subscriptions/sub/resourceGroups/rg/providers/test",
		envServer.values["dev"]["PLAYWRIGHT_SERVICE_RESOURCE_ID"],
	)
	require.Equal(t, "existing-value", envServer.values["dev"]["ALREADY_SET"])

	require.Len(t, promptServer.promptRequests, 2)
	require.Equal(
		t,
		"Enter a value for PLAYWRIGHT_SERVICE_ACCESS_TOKEN",
		promptServer.promptRequests[0].Options.Message,
	)
	require.True(t, promptServer.promptRequests[0].Options.Required)
	require.True(t, promptServer.promptRequests[0].Options.Secret)
	require.True(t, promptServer.promptRequests[0].Options.ClearOnCompletion)

	require.Equal(
		t,
		"Enter a value for PLAYWRIGHT_SERVICE_RESOURCE_ID",
		promptServer.promptRequests[1].Options.Message,
	)
	require.True(t, promptServer.promptRequests[1].Options.Required)
	require.False(t, promptServer.promptRequests[1].Options.Secret)
	require.False(t, promptServer.promptRequests[1].Options.ClearOnCompletion)
}

func TestConfigureAzureYamlEnvironmentVariables_NoPromptSkipsPrompts(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	content := `name: sample
services:
  connection:
    host: azure.ai.connection
    credentials:
      key: ${API_TOKEN}
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(content), 0600))

	envServer := &testEnvironmentServiceServer{}
	promptServer := &testPromptServiceServer{}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(
		t.Context(),
		azdClient,
		"dev",
		projectDir,
		true,
	)
	require.NoError(t, err)
	require.Empty(t, promptServer.promptRequests)
	require.Empty(t, envServer.values)
}

func TestConfigureAzureYamlEnvironmentVariables_SkipsConfiguredValues(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	content := `name: sample
services:
  connection:
    host: azure.ai.connection
    credentials:
      key: ${API_TOKEN}
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(content), 0600))

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"API_TOKEN": "configured",
			},
		},
	}
	promptServer := &testPromptServiceServer{}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(
		t.Context(),
		azdClient,
		"dev",
		projectDir,
		false,
	)
	require.NoError(t, err)
	require.Empty(t, promptServer.promptRequests)
	require.Equal(t, "configured", envServer.values["dev"]["API_TOKEN"])
}

func TestConfigureAzureYamlEnvironmentVariables_PersistsProcessEnvironmentFallback(t *testing.T) {
	const envVarName = "AZD_TEST_INIT_PROCESS_ONLY_VALUE"
	t.Setenv(envVarName, "from-process")

	projectDir := t.TempDir()
	content := `name: sample
services:
  toolbox:
    host: azure.ai.toolbox
    tools:
      - name: process-value
        configuration:
          value: ${AZD_TEST_INIT_PROCESS_ONLY_VALUE}
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(content), 0600))

	envServer := &testEnvironmentServiceServer{}
	promptServer := &testPromptServiceServer{}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(
		t.Context(),
		azdClient,
		"dev",
		projectDir,
		false,
	)
	require.NoError(t, err)
	require.Empty(t, promptServer.promptRequests)
	require.Equal(t, "from-process", envServer.values["dev"][envVarName])
}

func TestConfigureAzureYamlEnvironmentVariables_EmptyAzdValueBlocksProcessFallback(t *testing.T) {
	const envVarName = "AZD_TEST_INIT_EXPLICIT_EMPTY_VALUE"
	t.Setenv(envVarName, "from-process")

	projectDir := t.TempDir()
	content := `name: sample
services:
  connection:
    host: azure.ai.connection
    metadata:
      explicitEmpty: ${AZD_TEST_INIT_EXPLICIT_EMPTY_VALUE}
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(content), 0600))

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				envVarName: "",
			},
		},
	}
	promptServer := &testPromptServiceServer{
		promptResponses: []string{"prompted-value"},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(
		t.Context(),
		azdClient,
		"dev",
		projectDir,
		false,
	)
	require.NoError(t, err)
	require.Len(t, promptServer.promptRequests, 1)
	require.Equal(t, "prompted-value", envServer.values["dev"][envVarName])
}

func TestConfigureAzureYamlEnvironmentVariables_EmptyProcessValuePrompts(t *testing.T) {
	const envVarName = "AZD_TEST_INIT_EMPTY_PROCESS_VALUE"
	t.Setenv(envVarName, "")

	projectDir := t.TempDir()
	content := `name: sample
services:
  connection:
    host: azure.ai.connection
    metadata:
      emptyProcess: ${AZD_TEST_INIT_EMPTY_PROCESS_VALUE}
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(content), 0600))

	envServer := &testEnvironmentServiceServer{}
	promptServer := &testPromptServiceServer{
		promptResponses: []string{"prompted-value"},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(
		t.Context(),
		azdClient,
		"dev",
		projectDir,
		false,
	)
	require.NoError(t, err)
	require.Len(t, promptServer.promptRequests, 1)
	require.Equal(t, "prompted-value", envServer.values["dev"][envVarName])
}
