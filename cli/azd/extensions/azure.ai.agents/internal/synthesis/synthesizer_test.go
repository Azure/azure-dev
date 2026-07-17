// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"errors"
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
			name: "legacy configured agent with image => no ACR",
			yaml: `
services:
  assistant:
    host: azure.ai.agent
    config:
      kind: hosted
      image: myprivacr.azurecr.io/agents/assistant:v1
  ai-project:
    host: azure.ai.project
`,
			serviceName:    "ai-project",
			wantIncludeAcr: false,
		},
		{
			name: "legacy configured agent with codeConfiguration => no ACR",
			yaml: `
services:
  assistant:
    host: azure.ai.agent
    config:
      kind: hosted
      codeConfiguration:
        runtime: python_3_13
        entryPoint: app.py
  ai-project:
    host: azure.ai.project
`,
			serviceName:    "ai-project",
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

			connections, ok := res.Parameters["connections"].([]Connection)
			require.True(t, ok, "connections param should be []Connection, got %T", res.Parameters["connections"])
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
    credentials:
      keys:
        x-api-key: ${MCP_KEY}
    metadata:
      owner: ${MCP_OWNER}
`
	env := map[string]string{
		"MCP_URL":   "https://mcp.example.com/mcp",
		"MCP_KEY":   "secret-value",
		"MCP_OWNER": "team-ai",
	}

	getConn := func(t *testing.T, res *Result) Connection {
		t.Helper()
		conns, ok := res.Parameters["connections"].([]Connection)
		require.True(t, ok)
		require.Len(t, conns, 1)
		return conns[0]
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
		keys, ok := c.Credentials["keys"].(map[string]any)
		require.True(t, ok, "keys should be a nested map, got %T", c.Credentials["keys"])
		assert.Equal(t, "secret-value", keys["x-api-key"])
		assert.Equal(t, "team-ai", c.Metadata["owner"])
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
		keys, ok := c.Credentials["keys"].(map[string]any)
		require.True(t, ok)
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
		keys := c.Credentials["keys"].(map[string]any)
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
		keys := c.Credentials["keys"].(map[string]any)
		assert.Equal(t, "", keys["x-api-key"])
	})
}

func TestSynthesizeConnectionsAtRootResolvesFileRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "connection.yaml"),
		[]byte(`host: azure.ai.connection
category: CognitiveSearch
target: https://search.example
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
`),
		0o600,
	))
	raw := []byte(`services:
  project:
    host: azure.ai.project
  search:
    uses: [project]
    $ref: ./connection.yaml
`)

	result, err := Synthesize(Input{
		RawAzureYAML:  raw,
		ServiceName:   "project",
		AcceptedHosts: []string{"azure.ai.project"},
		ProjectRoot:   root,
		Env:           map[string]string{"SEARCH_KEY": "secret"},
	})

	require.NoError(t, err)
	connections, ok := result.Parameters["connections"].([]Connection)
	require.True(t, ok)
	require.Len(t, connections, 1)
	assert.Equal(t, "search", connections[0].Name)
	assert.Equal(t, "CognitiveSearch", connections[0].Category)
	assert.Equal(t, "https://search.example", connections[0].Target)
	assert.Equal(t, "secret", connections[0].Credentials["key"])
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
			"",
			map[string]string{"SEARCH_API_KEY": "secret"},
		)
		require.NoError(t, err)
		require.Len(t, conns, 2)
		assert.Equal(t, "bing-conn", conns[0].Name)
		assert.Equal(t, "search-conn", conns[1].Name)
		assert.Equal(t, "CognitiveSearch", conns[1].Category)
		assert.Equal(t, "secret", conns[1].Credentials["key"])
	})

	t.Run("no connection services yields empty slice", func(t *testing.T) {
		const noConns = `
services:
  my-project:
    host: azure.ai.project
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
`
		conns, err := BrownfieldConnections([]byte(noConns), "", nil)
		require.NoError(t, err)
		assert.Empty(t, conns)
	})

	t.Run("empty raw errors", func(t *testing.T) {
		_, err := BrownfieldConnections(nil, "", nil)
		require.Error(t, err)
	})

	t.Run("resolves connection file references", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "connection.yaml"),
			[]byte(`category: CognitiveSearch
target: https://search.example
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
`),
			0o600,
		))
		raw := []byte(`services:
  project:
    host: azure.ai.project
    endpoint: https://existing.example/api/projects/p1
  search:
    host: azure.ai.connection
    uses: [project]
    $ref: ./connection.yaml
`)

		connections, err := BrownfieldConnections(
			raw,
			root,
			map[string]string{"SEARCH_KEY": "secret"},
		)

		require.NoError(t, err)
		require.Len(t, connections, 1)
		assert.Equal(t, "CognitiveSearch", connections[0].Category)
		assert.Equal(t, "secret", connections[0].Credentials["key"])
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
			got, err := BrownfieldDeployments(
				[]byte(tt.yaml),
				"",
				tt.serviceName,
			)

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
	_, err := BrownfieldDeployments(nil, "", "my-project")
	require.Error(t, err)
}

func TestBrownfieldDeployments_ResolvesFileRef(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "deployment.yaml"),
		[]byte(
			"name: gpt-4o\n"+
				"model:\n"+
				"  format: OpenAI\n"+
				"  name: gpt-4o\n"+
				"  version: \"2024-08-06\"\n"+
				"sku:\n"+
				"  capacity: 10\n"+
				"  name: GlobalStandard\n",
		),
		0o600,
	))
	raw := []byte(`services:
  my-project:
    host: azure.ai.project
    endpoint: https://example
    deployments:
      - $ref: ./deployment.yaml
`)

	deployments, err := BrownfieldDeployments(
		raw,
		root,
		"my-project",
	)

	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)
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

func TestSynthesize_RequiresAgentImageInline(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte(
			"kind: hosted\n"+
				"name: referenced-agent\n",
		),
		0o600,
	))
	raw := []byte(`services:
  my-project:
    host: azure.ai.project
  referenced-agent:
    host: azure.ai.agent
    image: registry.example/agent:v1
    $ref: ./agent.yaml
`)

	result, err := Synthesize(Input{
		RawAzureYAML:  raw,
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
		ProjectRoot:   root,
	})

	require.NoError(t, err)
	includeAcr, ok := result.Parameters["includeAcr"].(bool)
	require.True(t, ok)
	assert.False(t, includeAcr)
}

func TestSynthesize_RejectsCoreFieldsFromAgentRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte(
			"kind: hosted\n"+
				"name: referenced-agent\n"+
				"image: registry.example/agent:v1\n",
		),
		0o600,
	))
	raw := []byte(`services:
  my-project:
    host: azure.ai.project
  referenced-agent:
    host: azure.ai.agent
    $ref: ./agent.yaml
`)

	_, err := Synthesize(Input{
		RawAzureYAML:  raw,
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
		ProjectRoot:   root,
	})

	require.ErrorContains(t, err, `core field "image"`)
}

func TestSynthesize_ResolvesAgentHostFromRefForAcr(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte(
			"host: azure.ai.agent\n"+
				"kind: hosted\n"+
				"name: referenced-agent\n",
		),
		0o600,
	))
	raw := []byte(`services:
  my-project:
    host: azure.ai.project
  referenced-agent:
    $ref: ./agent.yaml
`)

	result, err := Synthesize(Input{
		RawAzureYAML:  raw,
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
		ProjectRoot:   root,
	})

	require.NoError(t, err)
	includeAcr, ok := result.Parameters["includeAcr"].(bool)
	require.True(t, ok)
	assert.True(t, includeAcr)
}

func TestSynthesize_SkipsRefsOnUnrelatedServices(t *testing.T) {
	t.Parallel()

	raw := []byte(`services:
  my-project:
    host: azure.ai.project
  unrelated:
    host: containerapp
    api:
      $ref: ./missing-openapi.json
`)

	result, err := Synthesize(Input{
		RawAzureYAML:  raw,
		ServiceName:   "my-project",
		AcceptedHosts: []string{"azure.ai.project"},
		ProjectRoot:   t.TempDir(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, false, result.Parameters["includeAcr"])
	assert.Empty(t, result.Parameters["connections"])
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
		"templates/terraform/acr.tf",
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

	// connections carries the synthesized host: azure.ai.connection services so
	// the connections module can create them at provision time.
	assert.Contains(t, params, "connections", "connections param must be declared in the ARM template")

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
