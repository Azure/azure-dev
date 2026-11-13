// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindAzureYamlEnvironmentReferencesRejectsUnsupportedForms(t *testing.T) {
	t.Parallel()

	fields := []struct {
		name       string
		host       string
		properties string
		path       string
	}{
		{
			name:       "agent value",
			host:       "azure.ai.agent",
			properties: "    kind: hosted\n    environmentVariables:\n      - value: '%s'\n",
			path:       "environmentVariables[0].value",
		},
		{
			name:       "legacy agent value",
			host:       "azure.ai.agent",
			properties: "    config:\n      kind: hosted\n      environmentVariables:\n        - value: '%s'\n",
			path:       "config.environmentVariables[0].value",
		},
		{
			name:       "connection target",
			host:       "azure.ai.connection",
			properties: "    target: '%s'\n",
			path:       "target",
		},
		{
			name:       "connection credentials",
			host:       "azure.ai.connection",
			properties: "    credentials:\n      nested:\n        - key: '%s'\n",
			path:       "credentials.nested[0].key",
		},
		{
			name:       "connection metadata",
			host:       "azure.ai.connection",
			properties: "    metadata:\n      resourceId: '%s'\n",
			path:       "metadata.resourceId",
		},
		{
			name:       "connection audience",
			host:       "azure.ai.connection",
			properties: "    audience: '%s'\n",
			path:       "audience",
		},
		{
			name:       "connection authorization URL",
			host:       "azure.ai.connection",
			properties: "    authorizationUrl: '%s'\n",
			path:       "authorizationUrl",
		},
		{
			name:       "connection token URL",
			host:       "azure.ai.connection",
			properties: "    tokenUrl: '%s'\n",
			path:       "tokenUrl",
		},
		{
			name:       "connection refresh URL",
			host:       "azure.ai.connection",
			properties: "    refreshUrl: '%s'\n",
			path:       "refreshUrl",
		},
		{
			name:       "connection connector name",
			host:       "azure.ai.connection",
			properties: "    connectorName: '%s'\n",
			path:       "connectorName",
		},
		{
			name:       "connection scope",
			host:       "azure.ai.connection",
			properties: "    scopes:\n      - '%s'\n",
			path:       "scopes[0]",
		},
		{
			name:       "agent subnet",
			host:       "azure.ai.project",
			properties: "    network:\n      agentSubnet:\n        vnet: '%s'\n",
			path:       "network.agentSubnet.vnet",
		},
		{
			name:       "private endpoint subnet",
			host:       "azure.ai.project",
			properties: "    network:\n      peSubnet:\n        vnet: '%s'\n",
			path:       "network.peSubnet.vnet",
		},
		{
			name:       "DNS subscription",
			host:       "azure.ai.project",
			properties: "    network:\n      dns:\n        subscription: '%s'\n",
			path:       "network.dns.subscription",
		},
		{
			name:       "deprecated Foundry host",
			host:       "microsoft.foundry",
			properties: "    network:\n      dns:\n        subscription: '%s'\n",
			path:       "network.dns.subscription",
		},
		{
			name:       "routine input",
			host:       "azure.ai.routine",
			properties: "    action:\n      input:\n        items:\n          - value: '%s'\n",
			path:       "action.input.items[0].value",
		},
		{
			name:       "legacy routine input",
			host:       "azure.ai.routine",
			properties: "    config:\n      action:\n        input: '%s'\n",
			path:       "config.action.input",
		},
		{
			name:       "toolbox endpoint",
			host:       "azure.ai.toolbox",
			properties: "    endpoint: '%s'\n",
			path:       "endpoint",
		},
		{
			name:       "toolbox tool",
			host:       "azure.ai.toolbox",
			properties: "    tools:\n      - configuration:\n          endpoint: '%s'\n",
			path:       "tools[0].configuration.endpoint",
		},
		{
			name:       "legacy toolbox endpoint",
			host:       "azure.ai.toolbox",
			properties: "    config:\n      endpoint: '%s'\n",
			path:       "config.endpoint",
		},
	}
	values := []string{
		"${BAR:=x}",
		"${BAR:+alt}",
		"${BAR:?message}",
		"${BAR#prefix}",
		"${BAR:0:3}",
		"${OUTER:-${NESTED}}",
		"${OUTER:-$${NESTED}}",
		"${OUTER:-${BAR:=x}}",
		"${UNCLOSED",
		"${{event.body}",
	}

	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			t.Parallel()
			for _, value := range values {
				t.Run(value, func(t *testing.T) {
					t.Parallel()
					content := fmt.Sprintf(
						"services:\n  sample:\n    host: %s\n%s",
						field.host,
						fmt.Sprintf(field.properties, value),
					)
					references, err := findAzureYamlEnvironmentReferences([]byte(content), ".")
					require.ErrorContains(t, err, "unsupported environment variable reference")
					require.ErrorContains(t, err, `service "sample"`)
					require.ErrorContains(t, err, field.path+":")
					require.Nil(t, references)
				})
			}
		})
	}
}

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
      - name: MIXED_ESCAPE
        value: ${MIXED_REF} literal-$${MIXED_REF}
      - name: MIXED_SERVER
        value: ${SERVER_SHARED} ${{ event.body ?? '${SERVER_SHARED}' }}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "EXPANDED_AFTER_LITERAL_DOLLAR"},
				{Name: "MIXED_REF"},
				{Name: "SERVER_SHARED"},
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
    run: echo ${PROJECT_HOOK_VALUE:=x}
services:
  web:
    host: containerapp
    env:
      WEB_VALUE: ${WEB_VALUE:=x}
  agent:
    host: azure.ai.agent
    kind: hosted
    hooks:
      predeploy:
        shell: sh
        run: echo ${SERVICE_HOOK_VALUE:=x}
    environmentVariables:
      - name: AGENT_VALUE
        value: ${AGENT_VALUE}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "AGENT_VALUE"},
			},
		},
		{
			name: "project scans only expanded network fields and honors escaping",
			content: `name: sample
services:
  project:
    host: azure.ai.project
    endpoint: ${RAW_PROJECT_ENDPOINT:=x}
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
			// The network fields expand through foundry.ExpandEnv, so an escaped
			// $${VAR} stays literal and needs no environment value; only the
			// unescaped references are required.
			want: []azureYamlEnvironmentReference{
				{Name: "PE_VNET_ID"},
				{Name: "DNS_SUBSCRIPTION_ID"},
			},
		},
		{
			name: "connection scans only provider-expanded fields",
			content: `name: sample
services:
  connection:
    host: azure.ai.connection
    category: ${RAW_CATEGORY:=x}
    authType: ${RAW_AUTH_TYPE:=x}
    target: ${CONNECTION_TARGET}
    credentials:
      key: ${CONNECTION_KEY}
    metadata:
      resourceId: ${CONNECTION_RESOURCE_ID}
    audience: ${CONNECTION_AUDIENCE}
    authorizationUrl: ${CONNECTION_AUTHORIZATION_URL}
    tokenUrl: ${CONNECTION_TOKEN_URL}
    refreshUrl: ${CONNECTION_REFRESH_URL}
    connectorName: ${CONNECTION_CONNECTOR_NAME}
    scopes:
      - ${CONNECTION_SCOPE}
      - ${DEFAULT_SCOPE:-read}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "CONNECTION_TARGET"},
				{Name: "CONNECTION_KEY", Secret: true},
				{Name: "CONNECTION_RESOURCE_ID"},
				{Name: "CONNECTION_AUDIENCE"},
				{Name: "CONNECTION_AUTHORIZATION_URL"},
				{Name: "CONNECTION_TOKEN_URL"},
				{Name: "CONNECTION_REFRESH_URL"},
				{Name: "CONNECTION_CONNECTOR_NAME"},
				{Name: "CONNECTION_SCOPE"},
			},
		},
		{
			name: "toolbox scans endpoint and tools but not description",
			content: `name: sample
services:
  toolbox:
    host: azure.ai.toolbox
    description: ${RAW_TOOLBOX_DESCRIPTION:=x}
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
    description: ${RAW_ROUTINE_DESCRIPTION:=x}
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
    description: ${RAW_SKILL_DESCRIPTION:=x}
    instructions: ${RAW_SKILL_INSTRUCTIONS:=x}
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
			name: "deprecated Foundry host refs are scanned",
			content: `name: sample
services:
  project:
    host: microsoft.foundry
    network:
      agentSubnet:
        vnet: ${FOUNDRY_HOST_NETWORK}
`,
			want: []azureYamlEnvironmentReference{
				{Name: "FOUNDRY_HOST_NETWORK"},
			},
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
          value: ${STALE_AGENT_VALUE:=x}
  routine:
    host: azure.ai.routine
    description: inline routine
    action:
      input:
        value: ${INLINE_ROUTINE_INPUT}
    config:
      action:
        input:
          value: ${STALE_ROUTINE_INPUT:=x}
  toolbox:
    host: azure.ai.toolbox
    description: inline toolbox
    endpoint: ${INLINE_TOOLBOX_ENDPOINT}
    config:
      endpoint: ${STALE_TOOLBOX_ENDPOINT:=x}
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

func TestFindAzureYamlEnvironmentReferencesValidatesResolvedValues(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		referenced string
		override   string
		wantErr    bool
	}{
		{"invalid reference", "${BAR:=x}", "", true},
		{"invalid override", "${VALID}", "${OUTER:-${NESTED}}", true},
		{"valid override replaces invalid reference", "${BAR:=x}", "${VALID}", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(root, "connection.yaml"),
				fmt.Appendf(nil, "credentials:\n  key: '%s'\n", tt.referenced),
				0o600,
			))
			content := `services:
  sample:
    host: azure.ai.connection
    $ref: ./connection.yaml
`
			if tt.override != "" {
				content += fmt.Sprintf("    credentials:\n      key: '%s'\n", tt.override)
			}
			references, err := findAzureYamlEnvironmentReferences([]byte(content), root)
			if tt.wantErr {
				require.ErrorContains(t, err, `service "sample"`)
				require.ErrorContains(t, err, "credentials.key:")
				require.Nil(t, references)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []azureYamlEnvironmentReference{{Name: "VALID", Secret: true}}, references)
		})
	}
}

func TestConfigureAzureYamlEnvironmentVariablesRejectsBeforeChanges(t *testing.T) {
	const processVariable = "AZD_TEST_INIT_INVALID_REFERENCE"
	t.Setenv(processVariable, "from-process")

	for _, noPrompt := range []bool{false, true} {
		t.Run(fmt.Sprintf("noPrompt=%t", noPrompt), func(t *testing.T) {
			root := t.TempDir()
			content := []byte(`services:
  sample:
    host: azure.ai.connection
    target: ${AZD_TEST_INIT_INVALID_REFERENCE}
    credentials:
      key: ${BAR:=x}
`)
			manifestPath := filepath.Join(root, "azure.yaml")
			require.NoError(t, os.WriteFile(manifestPath, content, 0o600))
			env := &testEnvironmentServiceServer{values: map[string]map[string]string{
				"dev": {"UNCHANGED": "yes"},
			}}
			prompts := &testPromptServiceServer{}
			client := newTestAzdClient(t, env, &testWorkflowServiceServer{}, prompts)

			err := configureAzureYamlEnvironmentVariables(t.Context(), client, "dev", root, noPrompt)
			require.ErrorContains(t, err, `service "sample"`)
			require.ErrorContains(t, err, "credentials.key:")
			require.Zero(t, env.getValuesCalls)
			require.Empty(t, env.setKeys)
			require.Equal(t, map[string]string{"UNCHANGED": "yes"}, env.values["dev"])
			require.Empty(t, prompts.promptRequests)
			after, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			require.Equal(t, content, after)
		})
	}
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

	// '?' must stay literal input for every prompt, so no help message is sent:
	// it could never be opened and would only advertise "[Type ? for hint]".
	for _, req := range promptServer.promptRequests {
		require.True(t, req.Options.IgnoreHintKeys)
		require.Empty(t, req.Options.HelpMessage)
		require.Empty(t, req.Options.Hint)
	}
}

func TestConfigureAzureYamlEnvironmentVariables_PersistsValuesContainingQuestionMark(t *testing.T) {
	t.Setenv("AZURE_LANGUAGE_MCP_ENDPOINT", "")

	projectDir := t.TempDir()
	content := `name: toolbox-agent
services:
  langmcpconn:
    host: azure.ai.connection
    target: '${AZURE_LANGUAGE_MCP_ENDPOINT}'
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(content), 0600))
	t.Chdir(projectDir)

	const endpoint = "https://example.test/language/mcp?api-version=2025-11-15-preview"
	envServer := &testEnvironmentServiceServer{values: map[string]map[string]string{"dev": {}}}
	promptServer := &testPromptServiceServer{promptResponses: []string{endpoint}}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, promptServer)

	err := configureAzureYamlEnvironmentVariables(t.Context(), azdClient, "dev", ".", false)
	require.NoError(t, err)

	require.Len(t, promptServer.promptRequests, 1)
	opts := promptServer.promptRequests[0].Options
	require.Equal(t, "Enter a value for AZURE_LANGUAGE_MCP_ENDPOINT", opts.Message)
	require.False(t, opts.Secret)
	require.True(t, opts.IgnoreHintKeys)
	require.Empty(t, opts.HelpMessage)
	require.Equal(t, endpoint, envServer.values["dev"]["AZURE_LANGUAGE_MCP_ENDPOINT"])
}

func TestConfigureAzureYamlEnvironmentVariables_NoPromptSkipsPrompts(t *testing.T) {
	t.Setenv("API_TOKEN", "")

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

func TestConfigureAzureYamlEnvironmentVariables_NoPromptPersistsProcessEnvironmentFallback(t *testing.T) {
	const envVarName = "AZD_TEST_INIT_NO_PROMPT_PROCESS_VALUE"
	t.Setenv(envVarName, "from-process")

	projectDir := t.TempDir()
	content := `name: sample
services:
  toolbox:
    host: azure.ai.toolbox
    tools:
      - name: process-value
        configuration:
          value: ${AZD_TEST_INIT_NO_PROMPT_PROCESS_VALUE}
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
	require.Equal(t, "from-process", envServer.values["dev"][envVarName])
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
