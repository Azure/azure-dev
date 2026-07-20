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
    metadata:
      resourceId: '${PLAYWRIGHT_SERVICE_RESOURCE_ID}'
`,
			want: []azureYamlEnvironmentReference{
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
			name: "uses variable name to identify secrets outside credential paths",
			content: `name: sample
services:
  agent:
    host: azure.ai.agent
    environmentVariables:
      - name: API_TOKEN
        value: ${SERVICE_API_TOKEN}
      - name: ENDPOINT
        value: ${SERVICE_ENDPOINT}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "SERVICE_API_TOKEN", Secret: true},
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
	t.Parallel()

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
