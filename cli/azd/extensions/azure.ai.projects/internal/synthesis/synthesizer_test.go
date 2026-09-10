// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthesize(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		serviceName string

		wantErr        error
		wantDeployLen  int
		wantIncludeAcr bool
		// wantDeployName0, if non-empty, asserts the name of the first deployment.
		wantDeployName0 string
		// wantConnectionNames, if non-nil, asserts the exact names (sorted) of
		// the synthesized connections.
		wantConnectionNames []string
	}{
		{
			name: "greenfield hosted agent with docker",
			yaml: `
name: my-foundry-agent
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        docker:
          path: Dockerfile
          remoteBuild: true
`,
			serviceName:     "my-project",
			wantDeployLen:   1,
			wantIncludeAcr:  true,
			wantDeployName0: "gpt-4.1-mini",
		},
		{
			name: "split project with sibling docker agent => ACR on",
			yaml: `
name: my-foundry-agent
services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - my-project
    docker:
      path: Dockerfile
      remoteBuild: true
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
`,
			serviceName:     "my-project",
			wantDeployLen:   1,
			wantIncludeAcr:  true,
			wantDeployName0: "gpt-4.1-mini",
		},
		{
			name: "split project with sibling docker agent and image => no ACR",
			yaml: `
services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - my-project
    image: myprivacr.azurecr.io/agents/my-agent:v1
    docker:
      path: Dockerfile
      remoteBuild: true
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "legacy inline docker agent with image => no ACR",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    agents:
      - name: my-agent
        kind: hosted
        image: myprivacr.azurecr.io/agents/my-agent:v1
        docker:
          path: Dockerfile
`,
			serviceName:    "my-project",
			wantDeployLen:  0,
			wantIncludeAcr: false,
		},
		{
			name: "greenfield hosted agent runtime-only (no docker) => ACR on",
			yaml: `
name: my-foundry-agent
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        runtime:
          stack: python
          version: "3.12"
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: true,
		},
		{
			// Schema-conformant hand-authored shape (see schemas/examples/simple.azure.yaml):
			// hosted agent built from source with no docker:/image:/codeConfiguration:.
			name: "schema-conformant hosted agent, no docker => ACR on",
			yaml: `
services:
  assistant:
    host: azure.ai.agent
    project: ./agents/assistant
    kind: hosted
    name: assistant
    uses:
      - ai-project
  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o-mini
        model: {format: OpenAI, name: gpt-4o-mini, version: "2024-07-18"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName:    "ai-project",
			wantDeployLen:  1,
			wantIncludeAcr: true,
		},
		{
			name: "sibling hosted agent, kind omitted defaults hosted => ACR on",
			yaml: `
services:
  assistant:
    host: azure.ai.agent
    project: ./agents/assistant
    name: assistant
  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o-mini
        model: {format: OpenAI, name: gpt-4o-mini, version: "2024-07-18"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName:    "ai-project",
			wantDeployLen:  1,
			wantIncludeAcr: true,
		},
		{
			name: "sibling hosted agent with codeConfiguration => no ACR",
			yaml: `
services:
  assistant:
    host: azure.ai.agent
    project: ./agents/assistant
    kind: hosted
    name: assistant
    codeConfiguration:
      runtime: python_3_13
      entryPoint: app.py
  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o-mini
        model: {format: OpenAI, name: gpt-4o-mini, version: "2024-07-18"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName:    "ai-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "sibling hosted agent with image, no docker => no ACR",
			yaml: `
services:
  assistant:
    host: azure.ai.agent
    project: ./agents/assistant
    kind: hosted
    name: assistant
    image: myprivacr.azurecr.io/agents/assistant:v1
  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o-mini
        model: {format: OpenAI, name: gpt-4o-mini, version: "2024-07-18"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName:    "ai-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "inline hosted agent with codeConfiguration => no ACR",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        codeConfiguration:
          runtime: dotnet_10
          entryPoint: MyAgent.dll
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "prompt-only agent (no project/runtime/docker)",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: triage-agent
        kind: prompt
        instructions: route the user
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "mixed: one runtime agent and one docker agent => ACR on",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1
        model:
          format: OpenAI
          name: gpt-4.1
          version: "2025-04-14"
        sku:
          capacity: 50
          name: GlobalStandard
    agents:
      - name: support-agent
        kind: hosted
        project: src/support-agent
        runtime: {stack: python, version: "3.12"}
      - name: research-agent
        kind: hosted
        project: src/research-agent
        docker: {path: Dockerfile, remoteBuild: true}
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: true,
		},
		{
			name: "no deployments declared => empty array, not nil",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    agents:
      - name: prompt-agent
        kind: prompt
        instructions: hi
`,
			serviceName:    "my-project",
			wantDeployLen:  0,
			wantIncludeAcr: false,
		},
		{
			name: "ignores inline connections/toolboxes/skills on the project (deploy-time concerns)",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
    connections:
      - name: github-mcp-conn
        category: CustomKeys
        target: https://api.githubcopilot.com/mcp
        authType: CustomKeys
    toolboxes:
      - name: t1
        tools: [{type: web_search}]
    skills:
      - name: s1
        instructions: hi
    routines:
      - name: r1
        agent: prompt-agent
        trigger: {type: schedule, cron: "0 8 * * *"}
    agents:
      - name: prompt-agent
        kind: prompt
        instructions: hi
`,
			serviceName:         "my-project",
			wantDeployLen:       1,
			wantIncludeAcr:      false,
			wantConnectionNames: []string{},
		},
		{
			name: "collects sibling azure.ai.connection services (sorted by name)",
			yaml: `
services:
  my-project:
    host: azure.ai.project
  search-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: CognitiveSearch
    target: https://my-search.search.windows.net
    authType: ApiKey
    credentials:
      key: static-key
  bing-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: ApiKey
    target: https://api.bing.microsoft.com
    authType: ApiKey
`,
			serviceName:         "my-project",
			wantDeployLen:       0,
			wantIncludeAcr:      false,
			wantConnectionNames: []string{"bing-conn", "search-conn"},
		},
		{
			name: "no connections yields empty slice",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName:         "my-project",
			wantDeployLen:       1,
			wantConnectionNames: []string{},
		},
		{
			name: "brownfield: endpoint set => ErrEndpointBrownfield",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName: "my-project",
			wantErr:     ErrEndpointBrownfield,
		},
		{
			name: "brownfield: endpoint + network => network ignored, still ErrEndpointBrownfield",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
    network:
      peSubnet: {vnet: /subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v, name: pe}
`,
			serviceName: "my-project",
			wantErr:     ErrEndpointBrownfield,
		},
		{
			name: "blank endpoint is treated as greenfield",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    endpoint: "   "
`,
			serviceName: "my-project",
		},
		{
			name: "service not found",
			yaml: `
services:
  my-project:
    host: azure.ai.project
`,
			serviceName: "nope",
			wantErr:     ErrServiceNotFound,
		},
		{
			name: "wrong host treated as not found",
			yaml: `
services:
  webapp:
    host: containerapp
    project: src/web
`,
			serviceName: "webapp",
			wantErr:     ErrServiceNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Synthesize(Input{
				RawAzureYAML:  []byte(tt.yaml),
				ServiceName:   tt.serviceName,
				AcceptedHosts: []string{"azure.ai.project"},
			})

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "got %v, want %v", err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, res)

			deployments, ok := res.Parameters["deployments"].([]Deployment)
			require.True(t, ok, "deployments param should be []Deployment, got %T", res.Parameters["deployments"])
			assert.Len(t, deployments, tt.wantDeployLen)
			if tt.wantDeployName0 != "" {
				require.NotEmpty(t, deployments)
				assert.Equal(t, tt.wantDeployName0, deployments[0].Name)
			}

			includeAcr, ok := res.Parameters["includeAcr"].(bool)
			require.True(t, ok, "includeAcr param should be bool")
			assert.Equal(t, tt.wantIncludeAcr, includeAcr)

			connections := resultConnections(t, res)
			if tt.wantConnectionNames != nil {
				gotNames := make([]string, len(connections))
				for i, c := range connections {
					gotNames[i] = c.Name
				}
				assert.Equal(t, tt.wantConnectionNames, gotNames)
			}
		})
	}
}

// TestSynthesize_Connections covers the ${VAR} resolve-vs-preserve behavior for
// connection target and credential values, mirroring the network path.
func TestSynthesize_Connections(t *testing.T) {
	const yaml = `
services:
  my-project:
    host: azure.ai.project
  mcp-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: RemoteTool
    target: ${MCP_URL}
    authType: CustomKeys
    audience: ${MCP_AUDIENCE}
    connectorName: ${MCP_CONNECTOR}
    credentials:
      keys:
        x-api-key: ${MCP_KEY}
    metadata:
      owner: ${MCP_OWNER}
`
	env := map[string]string{
		"MCP_URL":       "https://mcp.example.com/mcp",
		"MCP_AUDIENCE":  "https://mcp.example.com",
		"MCP_CONNECTOR": "mcp-connector",
		"MCP_KEY":       "secret-value",
		"MCP_OWNER":     "team-ai",
	}

	getConn := func(t *testing.T, res *Result) Connection {
		t.Helper()
		conns := resultConnections(t, res)
		require.Len(t, conns, 1)
		return conns[0]
	}
	getKeys := func(t *testing.T, c Connection) map[string]any {
		t.Helper()
		value, found := c.Credentials["keys"]
		require.True(t, found, "credentials should contain keys")
		keys, ok := value.(map[string]any)
		require.True(t, ok, "keys should be a map, got %T", value)
		return keys
	}

	t.Run("provision path resolves ${VAR}", func(t *testing.T) {
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(yaml),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env:           env,
		})
		require.NoError(t, err)

		c := getConn(t, res)
		assert.Equal(t, "https://mcp.example.com/mcp", c.Target)
		assert.Equal(t, "https://mcp.example.com", c.Audience)
		assert.Equal(t, "mcp-connector", c.ConnectorName)
		keys := getKeys(t, c)
		assert.Equal(t, "secret-value", keys["x-api-key"])
		assert.Equal(t, "team-ai", c.Metadata["owner"])

		publicValue, found := res.Parameters["connections"]
		require.True(t, found)
		publicConnections, ok := publicValue.([]Connection)
		require.True(t, ok, "connections should be []Connection")
		require.Len(t, publicConnections, 1)
		assert.Nil(t, publicConnections[0].Credentials)
		secureValue, found := res.Parameters["connectionCredentials"]
		require.True(t, found)
		secureCredentials, ok := secureValue.(map[string]map[string]any)
		require.True(t, ok, "connectionCredentials should be a map")
		connectionCredentials, found := secureCredentials["mcp-conn"]
		require.True(t, found)
		keyValue, found := connectionCredentials["keys"]
		require.True(t, found)
		secureKeys, ok := keyValue.(map[string]any)
		require.True(t, ok, "secure keys should be a map")
		assert.Equal(t, "secret-value", secureKeys["x-api-key"])
	})

	t.Run("service env takes precedence and isolates lookup", func(t *testing.T) {
		const serviceEnvYAML = `
services:
  my-project:
    host: azure.ai.project
  mcp-conn:
    host: azure.ai.connection
    uses: [my-project]
    env:
      ENDPOINT: ${MCP_URL}
      KEY: ${MCP_KEY}
    category: RemoteTool
    target: ${ENDPOINT}
    authType: CustomKeys
    credentials:
      keys:
        x-api-key: ${KEY}
    metadata:
      owner: ${OWNER:-service-default}
`
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(serviceEnvYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env: map[string]string{
				"ENDPOINT": "https://wrong.example/mcp",
				"KEY":      "wrong-secret",
				"OWNER":    "wrong-owner",
			},
			ServiceEnvironments: map[string]map[string]string{
				"mcp-conn": {
					"ENDPOINT": "https://service.example/mcp",
					"KEY":      "service-secret",
				},
			},
		})
		require.NoError(t, err)

		c := getConn(t, res)
		assert.Equal(t, "https://service.example/mcp", c.Target)
		keys := getKeys(t, c)
		assert.Equal(t, "service-secret", keys["x-api-key"])
		assert.Equal(t, "service-default", c.Metadata["owner"])
	})

	t.Run("explicit empty env isolates the connection", func(t *testing.T) {
		const emptyEnvYAML = `
services:
  my-project:
    host: azure.ai.project
  mcp-conn:
    host: azure.ai.connection
    uses: [my-project]
    env: {}
    category: RemoteTool
    target: ${MCP_URL}
    authType: CustomKeys
    credentials:
      keys:
        x-api-key: ${MCP_KEY}
`
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(emptyEnvYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env: map[string]string{
				"MCP_URL": "https://leak.example/mcp",
				"MCP_KEY": "leaked-secret",
			},
		})
		require.NoError(t, err)

		c := getConn(t, res)
		assert.Equal(t, "", c.Target)
		assert.Empty(t, c.Audience)
		assert.Empty(t, c.ConnectorName)
		keys := getKeys(t, c)
		assert.Equal(t, "", keys["x-api-key"])
	})

	t.Run("reports declared connection environment scopes", func(t *testing.T) {
		const scopesYAML = `
services:
  my-project:
    host: azure.ai.project
  populated:
    host: azure.ai.connection
    env:
      ENDPOINT: ${MCP_URL}
  empty:
    host: azure.ai.connection
    env: {}
  legacy:
    host: azure.ai.connection
`
		scopes, err := ConnectionEnvironmentScopes(
			[]byte(scopesYAML),
			"",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{
			"populated": true,
			"empty":     true,
		}, scopes)
	})

	t.Run("eject path preserves ${VAR} verbatim", func(t *testing.T) {
		res, err := Synthesize(Input{
			RawAzureYAML:    []byte(yaml),
			ServiceName:     "my-project",
			AcceptedHosts:   []string{"azure.ai.project"},
			Env:             env,
			PreserveVarRefs: true,
		})
		require.NoError(t, err)

		c := getConn(t, res)
		assert.Equal(t, "${MCP_URL}", c.Target)
		assert.Equal(t, "${MCP_AUDIENCE}", c.Audience)
		assert.Equal(t, "${MCP_CONNECTOR}", c.ConnectorName)
		keys := getKeys(t, c)
		assert.Equal(t, "${MCP_KEY}", keys["x-api-key"])
		assert.Equal(t, "${MCP_OWNER}", c.Metadata["owner"])
	})

	t.Run("Foundry ${{...}} expressions survive provision-path expansion", func(t *testing.T) {
		const serverSideYAML = `
services:
  my-project:
    host: azure.ai.project
  mcp-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: RemoteTool
    target: https://mcp.example.com/mcp
    authType: CustomKeys
    credentials:
      keys:
        x-api-key: ${{connections.other.credentials.key}}
`
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(serverSideYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env:           env,
		})
		require.NoError(t, err)

		c := getConn(t, res)
		keys := getKeys(t, c)
		assert.Equal(t, "${{connections.other.credentials.key}}", keys["x-api-key"])
	})

	t.Run("missing ${VAR} on provision path resolves to empty (matches deploy-time ExpandEnv)", func(t *testing.T) {
		// foundry.ExpandEnv (drone/envsubst) treats an unset variable as empty
		// rather than an error, matching the deploy-time azure.ai.connection
		// service target's resolveConnectionEnv. A missing secret therefore
		// yields an empty value, not a synthesis failure.
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(yaml),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env:           map[string]string{}, // nothing set
		})
		require.NoError(t, err)

		c := getConn(t, res)
		assert.Equal(t, "", c.Target)
		keys := getKeys(t, c)
		assert.Equal(t, "", keys["x-api-key"])
	})

	t.Run("disabled condition is omitted from ARM params", func(t *testing.T) {
		const conditionedYAML = `
services:
  my-project:
    host: azure.ai.project
  enabled-conn:
    host: azure.ai.connection
    category: ApiKey
    target: https://enabled.example
  disabled-conn:
    host: azure.ai.connection
    condition: false
    category: ApiKey
    target: ${MISSING_TARGET}
    env:
      KEY: ${MISSING_KEY}
  env-gated:
    host: azure.ai.connection
    condition: ${ENABLE_CONNECTION}
    category: RemoteTool
    target: https://gated.example
`
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(conditionedYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env: map[string]string{
				"ENABLE_CONNECTION": "false",
			},
		})
		require.NoError(t, err)
		names := resultConnectionNames(t, res)
		assert.Equal(t, []string{"enabled-conn"}, names)

		scopes, err := ConnectionEnvironmentScopes(
			[]byte(conditionedYAML),
			"",
			map[string]string{"ENABLE_CONNECTION": "false"},
		)
		require.NoError(t, err)
		assert.Empty(t, scopes)
	})

	t.Run("eject still evaluates condition", func(t *testing.T) {
		const ejectYAML = `
services:
  my-project:
    host: azure.ai.project
  live-conn:
    host: azure.ai.connection
    condition: true
    category: ApiKey
    target: ${LIVE_URL}
  skipped-conn:
    host: azure.ai.connection
    condition: false
    category: ApiKey
    target: ${SKIP_URL}
`
		res, err := Synthesize(Input{
			RawAzureYAML:    []byte(ejectYAML),
			ServiceName:     "my-project",
			AcceptedHosts:   []string{"azure.ai.project"},
			PreserveVarRefs: true,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"live-conn"}, resultConnectionNames(t, res))
		c := getConn(t, res)
		assert.Equal(t, "${LIVE_URL}", c.Target)
	})

	t.Run("disabled connection skips missing payload $ref", func(t *testing.T) {
		const skippedRefYAML = `
services:
  my-project:
    host: azure.ai.project
  skipped-conn:
    host: azure.ai.connection
    condition: false
    $ref: ./missing-connection.yaml
`
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(skippedRefYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			ProjectRoot:   t.TempDir(),
		})
		require.NoError(t, err)
		assert.Empty(t, resultConnectionNames(t, res))
	})

	t.Run("ref-only disabled connection skips missing payload $ref", func(t *testing.T) {
		const skippedRefYAML = `
services:
  my-project:
    host: azure.ai.project
  skipped-conn:
    condition: false
    $ref: ./missing-connection.yaml
`
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(skippedRefYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			ProjectRoot:   t.TempDir(),
		})
		require.NoError(t, err)
		assert.Empty(t, resultConnectionNames(t, res))
	})

	t.Run("whitespace condition disables connection", func(t *testing.T) {
		const whitespaceYAML = `
services:
  my-project:
    host: azure.ai.project
  whitespace-conn:
    host: azure.ai.connection
    condition: "  "
    target: ${MISSING_TARGET}
`
		res, err := Synthesize(Input{
			RawAzureYAML:  []byte(whitespaceYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env:           map[string]string{},
		})
		require.NoError(t, err)
		assert.Empty(t, resultConnectionNames(t, res))
	})

	t.Run("numeric condition preserves YAML text", func(t *testing.T) {
		const yamlTemplate = `
services:
  my-project:
    host: azure.ai.project
  numeric-conn:
    host: azure.ai.connection
    condition: %s
    target: https://example
`
		for _, condition := range []string{"1.0", "01", "0x1"} {
			t.Run(condition, func(t *testing.T) {
				res, err := Synthesize(Input{
					RawAzureYAML: fmt.Appendf(nil, yamlTemplate, condition),
					ServiceName:  "my-project",
					AcceptedHosts: []string{
						"azure.ai.project",
					},
				})
				require.NoError(t, err)
				assert.Empty(t, resultConnectionNames(t, res))
			})
		}
	})

	t.Run("root condition wins over payload condition", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "connection.yaml"),
			[]byte(`host: azure.ai.connection
condition: false
category: ApiKey
target: https://example
`),
			0o600,
		))
		raw := []byte(`services:
  my-project:
    host: azure.ai.project
  root-conditioned:
    host: azure.ai.connection
    condition: true
    $ref: ./connection.yaml
`)

		res, err := Synthesize(Input{
			RawAzureYAML:  raw,
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			ProjectRoot:   root,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"root-conditioned"}, resultConnectionNames(t, res))
	})

	t.Run("ref-only condition returns configuration error", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "connection.yaml"),
			[]byte(`host: azure.ai.connection
condition: false
category: ApiKey
target: https://example
`),
			0o600,
		))
		raw := []byte(`services:
  my-project:
    host: azure.ai.project
  ref-only:
    $ref: ./connection.yaml
`)

		_, err := Synthesize(Input{
			RawAzureYAML:  raw,
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			ProjectRoot:   root,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "put condition beside host in azure.yaml")
	})

	t.Run("invalid condition fails synthesis", func(t *testing.T) {
		const invalidYAML = `
services:
  my-project:
    host: azure.ai.project
  bad-conn:
    host: azure.ai.connection
    condition:
      nested: true
    category: ApiKey
    target: https://example
`
		_, err := Synthesize(Input{
			RawAzureYAML:  []byte(invalidYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "condition")
	})
}

func TestSynthesize_ConnectionExtendedFields(t *testing.T) {
	const inputYAML = `
services:
  my-project:
    host: azure.ai.project
  oauth-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: RemoteTool
    target: https://mcp.example.com/mcp
    authType: OAuth2
    audience: ${OAUTH_AUDIENCE}
    authorizationUrl: ${OAUTH_AUTHORIZATION_URL}
    tokenUrl: ${OAUTH_TOKEN_URL}
    refreshUrl: ${OAUTH_REFRESH_URL}
    scopes:
      - ${OAUTH_SCOPE}
      - static.scope
    connectorName: ${OAUTH_CONNECTOR_NAME}
`
	// #nosec G101 -- these are test-only OAuth endpoint and scope values, not credentials.
	env := map[string]string{
		"OAUTH_AUDIENCE":          "https://mcp.example.com",
		"OAUTH_AUTHORIZATION_URL": "https://login.example.com/authorize",
		"OAUTH_TOKEN_URL":         "https://login.example.com/token",
		"OAUTH_REFRESH_URL":       "https://login.example.com/refresh",
		"OAUTH_SCOPE":             "tool.read",
		"OAUTH_CONNECTOR_NAME":    "managed-mcp",
	}

	t.Run("provision resolves extended fields", func(t *testing.T) {
		result, err := Synthesize(Input{
			RawAzureYAML:  []byte(inputYAML),
			ServiceName:   "my-project",
			AcceptedHosts: []string{"azure.ai.project"},
			Env:           env,
		})
		require.NoError(t, err)

		connection := resultConnections(t, result)[0]
		assert.Equal(t, env["OAUTH_AUDIENCE"], connection.Audience)
		assert.Equal(t, env["OAUTH_AUTHORIZATION_URL"], connection.AuthorizationURL)
		assert.Equal(t, env["OAUTH_TOKEN_URL"], connection.TokenURL)
		assert.Equal(t, env["OAUTH_REFRESH_URL"], connection.RefreshURL)
		assert.Equal(t, []string{"tool.read", "static.scope"}, connection.Scopes)
		assert.Equal(t, env["OAUTH_CONNECTOR_NAME"], connection.ConnectorName)
	})

	t.Run("eject preserves extended field references", func(t *testing.T) {
		result, err := Synthesize(Input{
			RawAzureYAML:    []byte(inputYAML),
			ServiceName:     "my-project",
			AcceptedHosts:   []string{"azure.ai.project"},
			Env:             env,
			PreserveVarRefs: true,
		})
		require.NoError(t, err)

		connection := resultConnections(t, result)[0]
		assert.Equal(t, "${OAUTH_AUDIENCE}", connection.Audience)
		assert.Equal(t, "${OAUTH_AUTHORIZATION_URL}", connection.AuthorizationURL)
		assert.Equal(t, "${OAUTH_TOKEN_URL}", connection.TokenURL)
		assert.Equal(t, "${OAUTH_REFRESH_URL}", connection.RefreshURL)
		assert.Equal(t, []string{"${OAUTH_SCOPE}", "static.scope"}, connection.Scopes)
		assert.Equal(t, "${OAUTH_CONNECTOR_NAME}", connection.ConnectorName)
	})
}

func TestSplitConnectionCredentialsPreservesEmptyOAuth2Object(t *testing.T) {
	t.Parallel()

	public, secure := SplitConnectionCredentials([]Connection{
		{Name: "managed-oauth", AuthType: "OAuth2", Credentials: map[string]any{}},
		{Name: "oauth-without-credentials", AuthType: "OAuth2"},
		{Name: "none", AuthType: "None", Credentials: map[string]any{}},
	})
	require.Len(t, public, 3)
	assert.Nil(t, public[0].Credentials)
	assert.Contains(t, secure, "managed-oauth")
	assert.Empty(t, secure["managed-oauth"])
	assert.Contains(t, secure, "oauth-without-credentials")
	assert.Empty(t, secure["oauth-without-credentials"])
	assert.NotContains(t, secure, "none")
}

func TestSynthesize_ManagedOAuth2PreservesEmptyCredentials(t *testing.T) {
	const inputYAML = `
services:
  my-project:
    host: azure.ai.project
  managed-oauth:
    host: azure.ai.connection
    uses: [my-project]
    category: RemoteTool
    target: https://mcp.example.com/mcp
    authType: OAuth2
    connectorName: managed-mcp
`
	result, err := Synthesize(Input{
		RawAzureYAML:  []byte(inputYAML),
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
	})
	require.NoError(t, err)

	public, ok := result.Parameters["connections"].([]Connection)
	require.True(t, ok)
	require.Len(t, public, 1)
	assert.Nil(t, public[0].Credentials)
	secure, ok := result.Parameters["connectionCredentials"].(map[string]map[string]any)
	require.True(t, ok)
	assert.Contains(t, secure, "managed-oauth")
	assert.Empty(t, secure["managed-oauth"])
}

func TestConnectionJSONOmitsEmptyOptionalAuthProperties(t *testing.T) {
	data, err := json.Marshal(Connection{
		Name:     "mcp-conn",
		Category: "RemoteTool",
		Target:   "https://mcp.example.com/mcp",
		AuthType: "CustomKeys",
	})
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"audience"`)
	assert.NotContains(t, string(data), `"connectorName"`)

	data, err = json.Marshal(Connection{
		Name:          "mcp-conn",
		Category:      "RemoteTool",
		Target:        "https://mcp.example.com/mcp",
		AuthType:      "OAuth2",
		Audience:      "https://mcp.example.com",
		ConnectorName: "mcp-connector",
	})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"audience":"https://mcp.example.com"`)
	assert.Contains(t, string(data), `"connectorName":"mcp-connector"`)
}

func TestSynthesizeNormalizesLegacyAgenticIdentity(t *testing.T) {
	const yaml = `
services:
  my-project:
    host: azure.ai.project
  token-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: RemoteTool
    target: https://mcp.example.com/mcp
    authType: AgenticIdentity
    audience: https://mcp.example.com
`

	res, err := Synthesize(Input{
		RawAzureYAML:  []byte(yaml),
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
	})
	require.NoError(t, err)

	connections := resultConnections(t, res)
	require.Len(t, connections, 1)
	assert.Equal(t, "AgenticIdentityToken", connections[0].AuthType)
}

// TestBrownfieldConnections verifies connection services are collected for a
// brownfield (endpoint:) project, with ${VAR} resolved (brownfield provisions
// so references must be concrete) and Foundry ${{...}} preserved.
func TestBrownfieldConnections(t *testing.T) {
	const yaml = `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
  search-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: CognitiveSearch
    target: https://my-search.search.windows.net
    authType: ApiKey
    credentials:
      key: ${SEARCH_API_KEY}
  bing-conn:
    host: azure.ai.connection
    uses: [my-project]
    category: ApiKey
    target: https://api.bing.microsoft.com
    authType: ApiKey
`

	t.Run("collects and resolves connections (sorted)", func(t *testing.T) {
		conns, err := BrownfieldConnections(
			[]byte(yaml),
			map[string]string{"SEARCH_API_KEY": "secret"},
			nil,
			"",
		)
		require.NoError(t, err)
		require.Len(t, conns, 2)
		assert.Equal(t, "bing-conn", conns[0].Name)
		assert.Equal(t, "search-conn", conns[1].Name)
		assert.Equal(t, "CognitiveSearch", conns[1].Category)
		assert.Equal(t, "secret", conns[1].Credentials["key"])
	})

	t.Run("service environment takes precedence", func(t *testing.T) {
		conns, err := BrownfieldConnections(
			[]byte(yaml),
			map[string]string{"SEARCH_API_KEY": "global"},
			map[string]map[string]string{
				"search-conn": {"SEARCH_API_KEY": "service"},
			},
			"",
		)
		require.NoError(t, err)
		require.Len(t, conns, 2)
		assert.Equal(t, "service", conns[1].Credentials["key"])
	})

	t.Run("no connection services yields empty slice", func(t *testing.T) {
		const noConns = `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
`
		conns, err := BrownfieldConnections(
			[]byte(noConns),
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		assert.Empty(t, conns)
	})

	t.Run("empty raw errors", func(t *testing.T) {
		_, err := BrownfieldConnections(nil, nil, nil, "")
		require.Error(t, err)
	})

	t.Run("omits disabled connections", func(t *testing.T) {
		const conditioned = `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
  live-conn:
    host: azure.ai.connection
    category: ApiKey
    target: https://live.example
  skipped-conn:
    host: azure.ai.connection
    condition: false
    category: ApiKey
    target: https://skipped.example
`
		conns, err := BrownfieldConnections(
			[]byte(conditioned),
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		require.Len(t, conns, 1)
		assert.Equal(t, "live-conn", conns[0].Name)
	})
}

func TestBrownfieldDeployments(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		serviceName string

		wantErr     error
		wantLen     int
		wantName0   string
		wantVersion string
	}{
		{
			name: "endpoint set with deployments returns them",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
    deployments:
      - name: gpt-4.1-mini-new
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName: "my-project",
			wantLen:     1,
			wantName0:   "gpt-4.1-mini-new",
			wantVersion: "2025-04-14",
		},
		{
			name: "endpoint set, multiple deployments",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
    deployments:
      - name: gpt-4.1
        model: {format: OpenAI, name: gpt-4.1, version: "2025-04-14"}
        sku: {capacity: 50, name: GlobalStandard}
      - name: text-embedding-3-large
        model: {format: OpenAI, name: text-embedding-3-large, version: "1"}
        sku: {capacity: 120, name: Standard}
`,
			serviceName: "my-project",
			wantLen:     2,
			wantName0:   "gpt-4.1",
		},
		{
			name: "endpoint set, no deployments => nil",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
`,
			serviceName: "my-project",
			wantLen:     0,
		},
		{
			name: "service not found",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
`,
			serviceName: "nope",
			wantErr:     ErrServiceNotFound,
		},
		{
			name:        "empty service name",
			yaml:        "services: {}",
			serviceName: "",
			wantErr:     nil, // returns a non-typed error; asserted below
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BrownfieldDeployments([]byte(tt.yaml), tt.serviceName, "")

			if tt.serviceName == "" {
				require.Error(t, err)
				return
			}
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "got %v, want %v", err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.wantLen)
			if tt.wantName0 != "" {
				require.NotEmpty(t, got)
				assert.Equal(t, tt.wantName0, got[0].Name)
			}
			if tt.wantVersion != "" {
				require.NotEmpty(t, got)
				assert.Equal(t, tt.wantVersion, got[0].Model.Version)
			}
		})
	}
}

func TestBrownfieldDeployments_EmptyRaw(t *testing.T) {
	_, err := BrownfieldDeployments(nil, "my-project", "")
	require.Error(t, err)
}

func TestSynthesize_NetworkPreserveVarRefs(t *testing.T) {
	// Eject path: ${VAR} references must pass through verbatim (and skip the
	// format checks that cannot run on an unexpanded placeholder), so the
	// ejected main.parameters.json stays environment-portable.
	yaml := `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
      dns:
        resourceGroup: rg-dns
        subscription: "${AZURE_DNS_SUBSCRIPTION_ID}"
`
	res, err := Synthesize(Input{
		RawAzureYAML:    []byte(yaml),
		ServiceName:     "my-project",
		AcceptedHosts:   []string{"azure.ai.project"},
		PreserveVarRefs: true,
	})
	require.NoError(t, err, "unset ${VAR} must not fail on the eject path")
	require.NotNil(t, res)
	assert.Equal(t, "${AZURE_VNET_ID}", res.Parameters["vnetId"])
	assert.Equal(t, "${AZURE_DNS_SUBSCRIPTION_ID}", res.Parameters["dnsZonesSubscription"])
	assert.Equal(t, "rg-dns", res.Parameters["dnsZonesResourceGroup"])
}

func TestSynthesize_NetworkPreserveVarRefs_StillValidatesConcrete(t *testing.T) {
	// PreserveVarRefs only skips checks for unexpanded placeholders; a
	// concrete-but-malformed value still fails on the eject path.
	yaml := `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: not-an-arm-id, name: pe-subnet}
`
	_, err := Synthesize(Input{
		RawAzureYAML:    []byte(yaml),
		ServiceName:     "my-project",
		AcceptedHosts:   []string{"azure.ai.project"},
		PreserveVarRefs: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a well-formed")
}

func TestSynthesize_ResolvesDeploymentRef(t *testing.T) {
	// A deployment item authored as a $ref must be loaded so synthesis sees the
	// real deployment, not a zero-valued {"$ref": ...} placeholder.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "deployments"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "deployments", "gpt-4o.yaml"), []byte(
		"name: gpt-4o\nmodel:\n  name: gpt-4o\n  format: OpenAI\n  version: \"2024-08-06\"\nsku:\n  name: Standard\n  capacity: 10\n"),
		0600))

	yaml := `
services:
  my-project:
    host: azure.ai.project
    deployments:
      - $ref: ./deployments/gpt-4o.yaml
`
	res, err := Synthesize(Input{
		RawAzureYAML:  []byte(yaml),
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
		ProjectRoot:   root,
	})
	require.NoError(t, err)
	deployments, ok := res.Parameters["deployments"].([]Deployment)
	require.True(t, ok, "deployments param should be []Deployment, got %T", res.Parameters["deployments"])
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)
	assert.Equal(t, "gpt-4o", deployments[0].Model.Name)
	assert.Equal(t, 10, deployments[0].Sku.Capacity)
}

func TestSynthesize_ResolvesSiblingServiceRefs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "connection.yaml"),
		[]byte("category: CognitiveSearch\ntarget: https://search.example\n"+
			"authType: ApiKey\ncredentials:\n  key: ${SEARCH_KEY}\n"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte("kind: hosted\nname: referenced-agent\n"),
		0600,
	))

	yaml := `
services:
  project:
    host: azure.ai.project
  connection:
    host: azure.ai.connection
    $ref: ./connection.yaml
  agent:
    host: azure.ai.agent
    image: example.azurecr.io/agent:latest
    $ref: ./agent.yaml
`
	res, err := Synthesize(Input{
		RawAzureYAML:  []byte(yaml),
		ServiceName:   "project",
		AcceptedHosts: []string{"azure.ai.project"},
		Env:           map[string]string{"SEARCH_KEY": "secret"},
		ProjectRoot:   root,
	})
	require.NoError(t, err)
	assert.Equal(t, false, res.Parameters["includeAcr"])

	connections := resultConnections(t, res)
	require.Len(t, connections, 1)
	assert.Equal(t, "CognitiveSearch", connections[0].Category)
	assert.Equal(t, "https://search.example", connections[0].Target)
	assert.Equal(t, "secret", connections[0].Credentials["key"])
}

func resultConnections(t *testing.T, result *Result) []Connection {
	t.Helper()

	connections, ok := result.Parameters["connections"].([]Connection)
	require.True(t, ok, "connections param should be []Connection")
	credentials, ok := result.Parameters["connectionCredentials"].(map[string]map[string]any)
	require.True(t, ok, "connectionCredentials param should be a credential map")
	return JoinConnectionCredentials(connections, credentials)
}

func resultConnectionNames(t *testing.T, result *Result) []string {
	t.Helper()
	connections := resultConnections(t, result)
	names := make([]string, len(connections))
	for i, connection := range connections {
		names[i] = connection.Name
	}
	return names
}

func TestBrownfieldServiceResolversResolveRefs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "project.yaml"),
		[]byte("endpoint: https://example.services.ai.azure.com/api/projects/existing\n"+
			"deployments:\n  - $ref: ./deployment.yaml\n"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "deployment.yaml"),
		[]byte("name: gpt-4o\nmodel: {name: gpt-4o, format: OpenAI, version: '2024-08-06'}\n"+
			"sku: {name: Standard, capacity: 10}\n"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "connection.yaml"),
		[]byte("category: CognitiveSearch\ntarget: https://search.example\nauthType: None\n"),
		0600,
	))

	yaml := `
services:
  project:
    host: azure.ai.project
    $ref: ./project.yaml
  connection:
    host: azure.ai.connection
    $ref: ./connection.yaml
`
	endpoint, err := ProjectEndpoint([]byte(yaml), "project", root)
	require.NoError(t, err)
	assert.Equal(
		t,
		"https://example.services.ai.azure.com/api/projects/existing",
		endpoint,
	)

	deployments, err := BrownfieldDeployments([]byte(yaml), "project", root)
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)

	connections, err := BrownfieldConnections(
		[]byte(yaml),
		nil,
		nil,
		root,
	)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	assert.Equal(t, "CognitiveSearch", connections[0].Category)
}

func TestSynthesize_InputValidation(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want string
	}{
		{
			name: "empty yaml",
			in:   Input{ServiceName: "x"},
			want: "RawAzureYAML is empty",
		},
		{
			name: "empty service name",
			in:   Input{RawAzureYAML: []byte("services:\n  x:\n    host: azure.ai.project\n")},
			want: "ServiceName is empty",
		},
		{
			name: "malformed yaml",
			in: Input{
				RawAzureYAML: []byte("services: [this is not a map"),
				ServiceName:  "x",
			},
			want: "parse azure.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Synthesize(tt.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestTemplatesFS_Embedded(t *testing.T) {
	fs := TemplatesFS()

	wantFiles := []string{
		"templates/main.bicep",
		"templates/main.arm.json",
		"templates/abbreviations.json",
		"templates/modules/acr.bicep",
		"templates/modules/acr-pull-role-assignment.bicep",
		"templates/modules/connections.bicep",
		"templates/modules/network.bicep",
		"templates/modules/subnet.bicep",
		"templates/modules/private-endpoint-dns.bicep",
	}
	for _, p := range wantFiles {
		t.Run(p, func(t *testing.T) {
			data, err := fs.ReadFile(p)
			require.NoError(t, err)
			assert.NotEmpty(t, data, "%s should not be empty", p)
		})
	}
}

func TestTerraformTemplatesFS_Embedded(t *testing.T) {
	fs := TerraformTemplatesFS()

	wantFiles := []string{
		"templates/terraform/provider.tf",
		"templates/terraform/variables.tf",
		"templates/terraform/main.tf",
		"templates/terraform/container-registry.tf",
		"templates/terraform/connections.tf",
		"templates/terraform/outputs.tf.tmpl",
	}
	for _, p := range wantFiles {
		t.Run(p, func(t *testing.T) {
			data, err := fs.ReadFile(p)
			require.NoError(t, err)
			assert.NotEmpty(t, data, "%s should not be empty", p)
		})
	}
	// outputs.tf is rendered from outputs.tf.tmpl at eject time, and
	// main.tfvars.json is generated -- neither is embedded as a final file
	// (otherwise they would go stale).
	for _, p := range []string{
		"templates/terraform/outputs.tf",
		"templates/terraform/main.tfvars.json",
	} {
		_, err := fs.ReadFile(p)
		assert.Error(t, err, "%s must not be embedded; it is generated at eject time", p)
	}
}

func TestTerraformConnectionTemplatesIncludeExtendedFields(t *testing.T) {
	tests := []struct {
		name          string
		fs            fs.ReadFileFS
		variablesPath string
		resourcePath  string
	}{
		{
			name:          "greenfield",
			fs:            TerraformTemplatesFS(),
			variablesPath: "templates/terraform/variables.tf",
			resourcePath:  "templates/terraform/connections.tf",
		},
		{
			name:          "existing project",
			fs:            ExistingProjectTerraformTemplatesFS(),
			variablesPath: "templates/terraform-existing-project/variables.tf",
			resourcePath:  "templates/terraform-existing-project/connections.tf",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			variables, err := test.fs.ReadFile(test.variablesPath)
			require.NoError(t, err)
			resource, err := test.fs.ReadFile(test.resourcePath)
			require.NoError(t, err)

			for _, field := range []string{
				"audience", "authorizationUrl", "tokenUrl", "refreshUrl", "scopes", "connectorName",
			} {
				assert.Contains(t, string(variables), field,
					"Terraform connection variable must expose %s", field)
				assert.Contains(t, string(resource), fmt.Sprintf("%s = each.value.%s", field, field),
					"Terraform connection payload must emit %s", field)
			}
		})
	}
}

// TestTerraformModule_DerivesNamesWhenEmpty guards the regression where unset
// AZURE_AI_PROJECT_NAME / AZURE_RESOURCE_GROUP substituted to "" in
// main.tfvars.json and failed at plan time (foundry_project_name validation /
// "name cannot be blank" on the resource group). The fix: main.tf derives both
// names from environment_name when the corresponding var is empty. This asserts
// the embedded templates still carry those fallbacks so they cannot regress.
func TestTerraformModule_DerivesNamesWhenEmpty(t *testing.T) {
	fs := TerraformTemplatesFS()

	vars, err := fs.ReadFile("templates/terraform/variables.tf")
	require.NoError(t, err)
	// Empty must be accepted by the variable validation (not a hard 3-32 regex).
	assert.Contains(t, string(vars), `var.foundry_project_name == ""`,
		"variables.tf must allow an empty foundry_project_name (empty => derive from env)")

	main, err := fs.ReadFile("templates/terraform/main.tf")
	require.NoError(t, err)
	// main.tf must compute an effective project name with an env-name fallback.
	assert.Contains(t, string(main), "derived_project_name",
		"main.tf must derive a project name when foundry_project_name is empty")
	assert.Contains(t, string(main), "local.foundry_project_name",
		"the project resource must use the derived local, not the raw variable")
	// main.tf must compute an effective resource group name with a fallback.
	assert.Contains(t, string(main), "local.resource_group_name",
		"the resource group must use the derived local, not the raw variable")
	assert.Contains(t, string(main), `"rg-${local.derived_rg_suffix}"`,
		"main.tf must derive an rg-{env} name when resource_group_name is empty")

	provider, err := fs.ReadFile("templates/terraform/provider.tf")
	require.NoError(t, err)
	assert.Contains(t, string(provider), `required_version = ">= 1.3.0`,
		"provider.tf must require Terraform 1.3 for optional object attributes")
}

func TestTerraformConnectionTemplatesPreserveOptionalAuthProperties(t *testing.T) {
	readers := []struct {
		name string
		path string
		read func(string) ([]byte, error)
	}{
		{
			name: "greenfield",
			path: "templates/terraform/connections.tf",
			read: TerraformTemplatesFS().ReadFile,
		},
		{
			name: "existing project",
			path: "templates/terraform-existing-project/connections.tf",
			read: ExistingProjectTerraformTemplatesFS().ReadFile,
		},
	}

	for _, tt := range readers {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.read(tt.path)
			require.NoError(t, err)
			text := string(data)
			assert.Contains(t, text,
				"each.value.audience != null && each.value.audience != \"\"",
				"connection audience must be conditionally merged")
			assert.Contains(t, text,
				"each.value.connectorName != null && each.value.connectorName != \"\"",
				"connection connectorName must be conditionally merged")
			assert.Contains(t, text,
				`lower(each.value.authType) == "oauth2"`,
				"OAuth2 connections must include credentials when omitted")
			assert.Contains(t, text,
				"credentials = each.value.credentials != null ? each.value.credentials : {}",
				"OAuth2 connections must use an empty credentials object when needed")
		})
	}
}

func TestARMTemplate_IsValidJSONWithExpectedShape(t *testing.T) {
	data, err := ARMTemplate()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var arm map[string]any
	require.NoError(t, json.Unmarshal(data, &arm), "ARM template must be valid JSON")

	// Sanity-check the ARM document is what we expect.
	assert.Contains(t, arm, "$schema")
	assert.Contains(t, arm, "resources")
	assert.Contains(t, arm, "parameters")

	// The template is subscription-scoped so `azd provision --preview` can run
	// what-if without creating the resource group first.
	schema, _ := arm["$schema"].(string)
	assert.Contains(t, schema, "subscriptionDeploymentTemplate",
		"main.bicep must target subscription scope")

	// resourceGroupName is the parameter that drives the resource group the
	// template creates; the provider supplies it at provision time.
	params, ok := arm["parameters"].(map[string]any)
	require.True(t, ok, "parameters must be an object")
	assert.Contains(t, params, "resourceGroupName")

	// connections must remain an array so ejected templates preserve the
	// connection object shape.
	assert.Contains(t, params, "connections", "connections param must be declared in the ARM template")
	connections, ok := params["connections"].(map[string]any)
	require.True(t, ok, "connections param must be an object")
	assert.Equal(t, "#/definitions/connectionsType", connections["$ref"])
	credentials, ok := params["connectionCredentials"].(map[string]any)
	require.True(t, ok, "connectionCredentials param must be an object")
	assert.Equal(t, "secureObject", credentials["type"])
	for _, field := range []string{
		"audience", "authorizationUrl", "tokenUrl", "refreshUrl", "scopes", "connectorName",
	} {
		assert.Contains(t, string(data), fmt.Sprintf("createObject('%s'", field),
			"compiled connection resource must emit %s", field)
	}

	// Network isolation parameters must exist so the synthesizer's network
	// param set is accepted by ARM (extra params would fail the deployment).
	for _, p := range []string{
		"enableNetworkIsolation", "useManagedEgress", "vnetId",
		"agentSubnetName", "agentSubnetPrefix", "createAgentSubnet",
		"peSubnetName", "peSubnetPrefix", "createPESubnet",
		"managedIsolationMode", "dnsZonesResourceGroup", "dnsZonesSubscription",
	} {
		assert.Contains(t, params, p, "network param %q must be declared in the ARM template", p)
	}

	// The old mode-enum param must be gone; egress is driven by useManagedEgress.
	assert.NotContains(t, params, "networkMode",
		"networkMode param was replaced by useManagedEgress")

	// Secure-by-default lock: the account data plane must be private whenever
	// network isolation is on. The compiled template must gate public access on
	// enableNetworkIsolation (not on egress mode), so a network-bound account is
	// never left public. This is the regression guard for the data-plane fix.
	text := string(data)
	wantDisable := `"disablePublicDataPlaneAccess": "[parameters('enableNetworkIsolation')]"`
	wantPublic := `"publicNetworkAccess": "[if(variables('disablePublicDataPlaneAccess'), 'Disabled', 'Enabled')]"`
	assert.Contains(t, text, wantDisable,
		"public data-plane access must be disabled for every network-isolated account")
	assert.Contains(t, text, wantPublic,
		"account publicNetworkAccess must follow disablePublicDataPlaneAccess")

	// Egress injection shape: byo injects into the customer subnet
	// (useMicrosoftManagedNetwork=false), managed uses the Microsoft-managed
	// network (useMicrosoftManagedNetwork=true). Both branches must survive
	// compilation so the account gets the right networkInjections per mode.
	assert.Contains(t, text, "'useMicrosoftManagedNetwork', false()",
		"byo egress must inject the agent subnet (useMicrosoftManagedNetwork=false)")
	assert.Contains(t, text, "'useMicrosoftManagedNetwork', true()",
		"managed egress must use the Microsoft-managed network (useMicrosoftManagedNetwork=true)")
	assert.Contains(t, text, `"networkInjections": "[variables('agentNetworkInjections')]"`,
		"account must carry the computed networkInjections")

	// isolationMode must be wired to the V2 managed network child resource
	// (regression guard: it was previously a no-op echoed only to output).
	assert.Contains(t, text, `"type": "Microsoft.CognitiveServices/accounts/managedNetworks"`,
		"managed isolationMode must provision a managedNetworks child resource")
	assert.Contains(t, text, `"isolationMode": "[parameters('managedIsolationMode')]"`,
		"managedNetworks isolationMode must come from the managedIsolationMode param")
	assert.Contains(t, text,
		`"value": "[reference('network').outputs.vnetLocation.value]"`,
		"private endpoint location must come from the customer VNet")
	assert.Contains(t, text,
		"if(not(empty(tryGet(parameters('connections')[copyIndex()], 'audience'))), "+
			"createObject('audience', tryGet(parameters('connections')[copyIndex()], 'audience'))",
		"connection audience must reach the compiled ARM request")
	assert.Contains(t, text,
		"if(not(empty(tryGet(parameters('connections')[copyIndex()], 'connectorName'))), "+
			"createObject('connectorName', tryGet(parameters('connections')[copyIndex()], 'connectorName'))",
		"connection connectorName must reach the compiled ARM request")
	assert.Contains(t, text,
		"if(and(equals(toLower(parameters('connections')[copyIndex()].authType), 'oauth2'), "+
			"not(contains(parameters('connectionCredentials'), parameters('connections')[copyIndex()].name))), "+
			"createObject('credentials', createObject()), createObject())",
		"managed OAuth2 connections must include an empty credentials object")
}

func TestExistingProjectARMTemplate_SecuresConnectionCredentials(t *testing.T) {
	data, err := ExistingProjectARMTemplate()
	require.NoError(t, err)

	var arm map[string]any
	require.NoError(t, json.Unmarshal(data, &arm))
	params, ok := arm["parameters"].(map[string]any)
	require.True(t, ok, "parameters must be an object")
	connections, ok := params["connections"].(map[string]any)
	require.True(t, ok, "connections param must be an object")
	assert.Equal(t, "array", connections["type"])
	credentials, ok := params["connectionCredentials"].(map[string]any)
	require.True(t, ok, "connectionCredentials param must be an object")
	assert.Equal(t, "secureObject", credentials["type"])

	text := string(data)
	assert.Contains(t, text,
		"if(not(empty(tryGet(parameters('connections')[copyIndex()], 'audience'))), "+
			"createObject('audience', tryGet(parameters('connections')[copyIndex()], 'audience'))",
		"connection audience must reach the existing-project ARM request")
	assert.Contains(t, text,
		"if(not(empty(tryGet(parameters('connections')[copyIndex()], 'connectorName'))), "+
			"createObject('connectorName', tryGet(parameters('connections')[copyIndex()], 'connectorName'))",
		"connection connectorName must reach the existing-project ARM request")
	assert.Contains(t, text,
		"if(and(equals(toLower(parameters('connections')[copyIndex()].authType), 'oauth2'), "+
			"not(contains(parameters('connectionCredentials'), parameters('connections')[copyIndex()].name))), "+
			"createObject('credentials', createObject()), createObject())",
		"managed OAuth2 connections must include an empty credentials object")
}

func TestSynthesize_Network(t *testing.T) {
	t.Setenv("AZURE_VNET_ID",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/"+
			"providers/Microsoft.Network/virtualNetworks/my-vnet")

	const validVNet = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
		"providers/Microsoft.Network/virtualNetworks/my-vnet"

	tests := []struct {
		name     string
		yaml     string
		wantMode string
		check    func(t *testing.T, p map[string]any)
	}{
		{
			name: "no network block => public account, isolation off",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			wantMode: NetworkModeNone,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, false, p["enableNetworkIsolation"])
				assert.Equal(t, false, p["useManagedEgress"])
			},
		},
		{
			name: "byo egress (agentSubnet present) with explicit subnets => create both",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: agent-subnet, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
      dns:
        resourceGroup: rg-private-dns
        subscription: 22222222-2222-2222-2222-222222222222
`,
			wantMode: NetworkModeByo,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["enableNetworkIsolation"])
				assert.Equal(t, false, p["useManagedEgress"])
				assert.Equal(t, validVNet, p["vnetId"])
				assert.Equal(t, "agent-subnet", p["agentSubnetName"])
				assert.Equal(t, "192.168.0.0/24", p["agentSubnetPrefix"])
				assert.Equal(t, true, p["createAgentSubnet"])
				assert.Equal(t, true, p["createPESubnet"])
				assert.Equal(t, "rg-private-dns", p["dnsZonesResourceGroup"])
				assert.Equal(t, "22222222-2222-2222-2222-222222222222", p["dnsZonesSubscription"])
			},
		},
		{
			name: "subnet without prefix => reference (create=false)",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: existing-agent}
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
`,
			wantMode: NetworkModeByo,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, "existing-agent", p["agentSubnetName"])
				assert.Equal(t, false, p["createAgentSubnet"])
				assert.Equal(t, "pe-subnet", p["peSubnetName"])
				assert.Equal(t, true, p["createPESubnet"])
			},
		},
		{
			name: "subnet vnet from ${VAR}",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Contains(t, p["vnetId"], "/virtualNetworks/my-vnet")
			},
		},
		{
			name: "managed egress (agentSubnet absent) with isolation",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      isolationMode: AllowOnlyApprovedOutbound
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["enableNetworkIsolation"])
				assert.Equal(t, true, p["useManagedEgress"])
				assert.Equal(t, false, p["createAgentSubnet"])
				assert.Equal(t, "AllowOnlyApprovedOutbound", p["managedIsolationMode"])
			},
		},
		{
			name: "dns subscription normalized from /subscriptions/<guid>",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet}
      dns:
        resourceGroup: rg-dns
        subscription: /subscriptions/33333333-3333-3333-3333-333333333333
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, "33333333-3333-3333-3333-333333333333", p["dnsZonesSubscription"])
			},
		},
		{
			name: "managed egress, isolationMode unset => empty managedIsolationMode",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["useManagedEgress"])
				assert.Equal(t, "", p["managedIsolationMode"])
				assert.Equal(t, true, p["createPESubnet"])
			},
		},
		{
			name: "managed egress, AllowInternetOutbound with referenced peSubnet",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      isolationMode: AllowInternetOutbound
      peSubnet: {vnet: ` + validVNet + `, name: existing-pe}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["useManagedEgress"])
				assert.Equal(t, "AllowInternetOutbound", p["managedIsolationMode"])
				assert.Equal(t, "existing-pe", p["peSubnetName"])
				assert.Equal(t, false, p["createPESubnet"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Synthesize(Input{
				RawAzureYAML:  []byte(tt.yaml),
				ServiceName:   "my-project",
				AcceptedHosts: []string{"azure.ai.project"},
			})
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.Equal(t, tt.wantMode, res.NetworkMode)
			if tt.check != nil {
				tt.check(t, res.Parameters)
			}
		})
	}
}

func TestSynthesize_RejectsAutoCreatedAcrWithPrivateNetworking(t *testing.T) {
	const validVNet = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
		"providers/Microsoft.Network/virtualNetworks/my-vnet"
	const yaml = `
services:
  assistant:
    host: azure.ai.agent
    kind: hosted
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet}
`

	_, err := Synthesize(Input{
		RawAzureYAML:  []byte(yaml),
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
	})
	require.ErrorContains(t, err, "does not support an auto-created Azure Container Registry")
}

func TestSynthesize_NetworkValidationErrors(t *testing.T) {
	const validVNet = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
		"providers/Microsoft.Network/virtualNetworks/my-vnet"
	const validVNet2 = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
		"providers/Microsoft.Network/virtualNetworks/other-vnet"

	tests := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name: "network present but peSubnet missing",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      isolationMode: AllowInternetOutbound
`,
			wantSub: "private networking requires peSubnet",
		},
		{
			name: "isolationMode with agentSubnet present",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      isolationMode: AllowInternetOutbound
      agentSubnet: {vnet: ` + validVNet + `, name: a, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet + `, name: pe, prefix: 192.168.1.0/24}
`,
			wantSub: "only valid for managed egress",
		},
		{
			name: "agentSubnet and peSubnet in different vnets",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: a, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet2 + `, name: pe, prefix: 192.168.1.0/24}
`,
			wantSub: "same virtual network",
		},
		{
			name: "agentSubnet and peSubnet share a name in one vnet",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: shared, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet + `, name: shared, prefix: 192.168.1.0/24}
`,
			wantSub: "agentSubnet.name and peSubnet.name must differ",
		},
		{
			name: "subnet missing vnet",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {name: pe}
`,
			wantSub: "peSubnet.vnet: required",
		},
		{
			name: "subnet missing name",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: ` + validVNet + `}
`,
			wantSub: "peSubnet.name: required",
		},
		{
			name: "malformed vnet id",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: not-an-arm-id, name: pe}
`,
			wantSub: "not a well-formed",
		},
		{
			name: "subnet invalid cidr",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: ` + validVNet + `, name: pe, prefix: not-a-cidr}
`,
			wantSub: "not a valid CIDR",
		},
		{
			name: "unresolved var",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${DEFINITELY_NOT_SET_VAR_XYZ}", name: pe}
`,
			wantSub: "unresolved environment variable",
		},
		{
			name: "bad managed isolation mode",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      isolationMode: Wide
      peSubnet: {vnet: ` + validVNet + `, name: pe}
`,
			wantSub: "isolationMode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Synthesize(Input{
				RawAzureYAML:  []byte(tt.yaml),
				ServiceName:   "my-project",
				AcceptedHosts: []string{"azure.ai.project"},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantSub)
			// Errors carry the service-scoped field path.
			assert.Contains(t, err.Error(), "services.my-project.network")
		})
	}
}

// TestResolveVars_MatchesFoundryExpandEnv locks in that the three project
// network fields resolved through resolveVars use the same expander semantics
// as every other Foundry field: ${VAR:-default} falls back, $${VAR} stays
// literal, and a reference with neither a value nor a default is still a
// load-bearing error naming the variable.
func TestResolveVars_MatchesFoundryExpandEnv(t *testing.T) {
	env := map[string]string{
		"SET_VAR":   "set-value",
		"EMPTY_VAR": "",
	}

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "plain reference", in: "${SET_VAR}", want: "set-value"},
		{name: "default is unused when set", in: "${SET_VAR:-fallback}", want: "set-value"},
		{name: "default fills in when unset", in: "${MISSING_VAR_XYZ:-fallback}", want: "fallback"},
		{name: "empty default is allowed", in: "${MISSING_VAR_XYZ:-}", want: ""},
		{name: "empty env value takes the default", in: "${EMPTY_VAR:-fallback}", want: "fallback"},
		{name: "escaped reference stays literal", in: "$${MISSING_VAR_XYZ}", want: "${MISSING_VAR_XYZ}"},
		{
			// required is derived from the same scanner the expander drives, so
			// an occurrence the expander never resolves cannot make a live,
			// defaulted occurrence of the same name look unresolvable.
			name: "escaped reference does not make a defaulted one required",
			in:   "$${MISSING_VAR_XYZ} ${MISSING_VAR_XYZ:-fallback}",
			want: "${MISSING_VAR_XYZ} fallback",
		},
		{
			name: "a name in a Foundry span does not make a defaulted one required",
			in:   "${{connections.${MISSING_VAR_XYZ}.key}} ${MISSING_VAR_XYZ:-fallback}",
			want: "${{connections.${MISSING_VAR_XYZ}.key}} fallback",
		},
		{name: "no references", in: "/subscriptions/abc", want: "/subscriptions/abc"},
		{
			name: "default inside a resource id",
			in:   "${MISSING_VAR_XYZ:-/subscriptions/s/resourceGroups/rg}",
			want: "/subscriptions/s/resourceGroups/rg",
		},
		{
			name:    "unresolved reference errors",
			in:      "${MISSING_VAR_XYZ}",
			wantErr: "unresolved environment variable ${MISSING_VAR_XYZ}",
		},
		{
			name:    "first unresolved reference is named",
			in:      "${MISSING_A_XYZ}/${MISSING_B_XYZ}",
			wantErr: "unresolved environment variable ${MISSING_A_XYZ}",
		},
		{
			name:    "a default elsewhere does not excuse a bare reference",
			in:      "${MISSING_VAR_XYZ:-ok}/${MISSING_VAR_XYZ}",
			wantErr: "unresolved environment variable ${MISSING_VAR_XYZ}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveVars(tt.in, env)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestContainsVarRef_RecognizesDefaults guards the eject path: a reference with
// a default must be recognized as still-unresolved so the value is kept
// verbatim and the ARM-shape checks are deferred to provision time, instead of
// being rejected as a malformed resource id.
//
// The converse matters too. An escaped reference and a name reserved by a
// Foundry ${{...}} span are never expanded, so the value is already as concrete
// as it will ever be and the shape checks have to run on it now rather than
// being deferred to a provision that can only fail.
func TestContainsVarRef_RecognizesDefaults(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "${VNET}", want: true},
		{in: "${VNET:-/subscriptions/s}", want: true},
		{in: "/subscriptions/s/resourceGroups/rg", want: false},
		{in: "$${VNET}", want: false},
		{in: "${{connections.store.key}}", want: false},
		{in: "$${VNET} ${OTHER}", want: true},
		{in: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, containsVarRef(tt.in))
		})
	}
}

// TestValidateEnvReferences_RejectsUnsupportedForms pins the guard on
// drone/envsubst's wider grammar. Every rejected form below is one envsubst
// expands and the scanner does not report, so without this check it slips past
// the unresolved-variable guard: ${MISSING:=x} silently resolves, ${MISSING#x}
// silently becomes "", and the caller then validates the rewritten value as if
// the user had typed it. Typing ':=' for ':-' is a one character slip.
func TestValidateEnvReferences_RejectsUnsupportedForms(t *testing.T) {
	t.Parallel()

	supported := []string{
		"${VAR}",
		"${VAR:-default}",
		"${VAR:-}",
		"$${VAR}",
		"${{connections.store.credentials.key}}",
		"${{ tools.${INNER} }}",
		"${MISSING:-${{event.body}}}",
		// An escaped Foundry span: ExpandEnv masks the span starting at the
		// second '$', so nothing inside it reaches envsubst and the '$' pair is
		// never an escape.
		"$${{ tools.${INNER} }}",
		"/subscriptions/s/resourceGroups/rg",
		// Bare '$' forms survive expansion untouched: envsubst only expands the
		// braced shape, so these need no rejection.
		"$VAR",
		"costs $5 today",
		"a$b",
		"",
	}
	for _, value := range supported {
		t.Run("ok/"+value, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, ValidateEnvReferences(value))
		})
	}

	unsupported := []string{
		"${MISSING:=default}",
		"${MISSING:+alt}",
		"${MISSING:?boom}",
		"${MISSING#prefix}",
		"${MISSING%suffix}",
		"${MISSING:0:3}",
		"${MISSING-nodefault}",
		"${1BAD}",
		"prefix ${MISSING:=x} suffix",
		"${OUTER:-${INNER:=x}}",
		"${A:-${9BAD}}",
	}
	for _, value := range unsupported {
		t.Run("rejected/"+value, func(t *testing.T) {
			t.Parallel()
			err := ValidateEnvReferences(value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is not a supported environment variable reference")
			assert.Contains(t, err.Error(), "${VAR:-default}",
				"the message has to name the shape the user probably meant")
		})
	}

	// A nested reference is expanded but never discovered. Refusing it withdraws
	// a shape that works today when the nested name is set, because `required`
	// is static: reporting the nested name would fail whenever the outer one
	// resolves, and not reporting it lets ${A:-${B}} with neither set expand to
	// empty, so the field's own shape check blames the empty value.
	nested := map[string]string{
		"${OUTER:-${NESTED}}":                "${NESTED}",
		"${OUTER:-prefix-${NESTED}-suffix}":  "${NESTED}",
		"${OUTER:-${NESTED:-inner}}":         "${NESTED:-inner}",
		"${OUTER:-$${NESTED}}":               "${NESTED}",
		"${OUTER:-prefix-$${NESTED}-suffix}": "${NESTED}",
		// The quoted fragment has to be the nested reference's real span; a
		// truncate-at-the-first-'}' fragment would come out unbalanced here.
		"${A:-${B:-${C}}}": "${B:-${C}}",
	}
	for value, fragment := range nested {
		t.Run("nested/"+value, func(t *testing.T) {
			t.Parallel()
			err := ValidateEnvReferences(value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "nests an environment variable reference inside a :- default")
			assert.Contains(t, err.Error(), fmt.Sprintf("%q", fragment),
				"the message has to quote the nested reference's real span")
		})
	}

	t.Run("rejected/unterminated foundry span", func(t *testing.T) {
		t.Parallel()
		err := ValidateEnvReferences("${{connections.store.key}")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing the closing }}")
	})
}

// TestSynthesize_NetworkRejectsUnsupportedVarSyntax covers the guard end to end
// on both paths. envsubst would expand these, so on the provision path the
// value is silently rewritten before the ARM id / subscription checks see it,
// and on the eject path it is written into the template verbatim and rewritten
// at provision. Either way the user never learns their ':=' did not mean ':-'.
func TestSynthesize_NetworkRejectsUnsupportedVarSyntax(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		field string
	}{
		{
			name:  "subnet vnet",
			field: "peSubnet.vnet",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${MISSING_VNET_XYZ:=/subscriptions/s}", name: pe-subnet}
`,
		},
		{
			name:  "dns subscription",
			field: "dns.subscription",
			yaml: `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet:
        vnet: /subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v
        name: pe-subnet
      dns:
        subscription: "${MISSING_SUB_XYZ#prefix}"
`,
		},
	}

	for _, tt := range tests {
		for _, preserve := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preserveVarRefs=%v", tt.name, preserve), func(t *testing.T) {
				_, err := Synthesize(Input{
					RawAzureYAML:    []byte(tt.yaml),
					ServiceName:     "my-project",
					AcceptedHosts:   []string{"azure.ai.project"},
					PreserveVarRefs: preserve,
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "is not a supported environment variable reference")
				assert.Contains(t, err.Error(), tt.field,
					"the error has to name the offending field")
			})
		}
	}
}

// TestSynthesize_NetworkEscapedRefIsValidatedOnBothPaths pins that an escaped
// reference is final on both paths. $${VNET} resolves to the literal ${VNET},
// which is not a vnet id and never becomes one, so deferring the shape check to
// provision only moves the failure somewhere less useful — and made eject and
// provision disagree about the same azure.yaml.
func TestSynthesize_NetworkEscapedRefIsValidatedOnBothPaths(t *testing.T) {
	const yaml = `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "$${VNET_XYZ}", name: pe-subnet}
`
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprintf("preserveVarRefs=%v", preserve), func(t *testing.T) {
			_, err := Synthesize(Input{
				RawAzureYAML:    []byte(yaml),
				ServiceName:     "my-project",
				AcceptedHosts:   []string{"azure.ai.project"},
				PreserveVarRefs: preserve,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is not a well-formed Microsoft.Network/virtualNetworks id")
		})
	}
}

// TestSynthesize_NetworkVarRefDefaults covers the reported bug end to end:
// ${VAR:-default} on the three network fields previously fell through
// resolveVars unchanged and was then rejected by the ARM id / subscription
// shape checks, blaming the resource id instead of the unsupported syntax.
func TestSynthesize_NetworkVarRefDefaults(t *testing.T) {
	const (
		fallbackVNet = "/subscriptions/00000000-0000-0000-0000-000000000000" +
			"/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/default"
		fallbackSub = "11111111-1111-1111-1111-111111111111"
	)

	yaml := `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${MISSING_VNET_XYZ:-` + fallbackVNet + `}", name: pe-subnet}
      dns:
        subscription: "${MISSING_SUB_XYZ:-` + fallbackSub + `}"
`
	res, err := Synthesize(Input{
		RawAzureYAML:  []byte(yaml),
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, fallbackVNet, res.Parameters["vnetId"])
	assert.Equal(t, fallbackSub, res.Parameters["dnsZonesSubscription"])
}

// TestSynthesize_NetworkPreserveVarRefsWithDefault is the eject-path half of the
// same bug: a defaulted reference must survive verbatim rather than being
// rejected as a malformed VNet id.
func TestSynthesize_NetworkPreserveVarRefsWithDefault(t *testing.T) {
	const ref = "${AZURE_VNET_ID:-/subscriptions/s/resourceGroups/rg" +
		"/providers/Microsoft.Network/virtualNetworks/default}"

	yaml := `
services:
  my-project:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "` + ref + `", name: pe-subnet}
`
	res, err := Synthesize(Input{
		RawAzureYAML:    []byte(yaml),
		ServiceName:     "my-project",
		AcceptedHosts:   []string{"azure.ai.project"},
		PreserveVarRefs: true,
	})
	require.NoError(t, err, "a defaulted ${VAR} must not fail on the eject path")
	require.NotNil(t, res)
	assert.Equal(t, ref, res.Parameters["vnetId"])
}
