// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

const toolboxTestProject = "https://acct.services.ai.azure.com/api/projects/proj"
const toolboxTestURL = toolboxTestProject + "/toolboxes/research/versions/1/mcp?api-version=v1"

func TestPlanToolboxAttachments(t *testing.T) {
	for _, tt := range []struct {
		name       string
		tools      []any
		refs       []string
		endpoints  []string
		wantLabels []string
		wantError  string
	}{
		{name: "new attachment", refs: []string{"tools"}, endpoints: []string{toolboxTestURL}, wantLabels: []string{"tools"}},
		{name: "same resource different versions", refs: []string{"one", "two"},
			endpoints:  []string{toolboxTestURL, strings.Replace(toolboxTestURL, "/versions/1/", "/versions/2/", 1)},
			wantLabels: []string{"one", "two"}},
		{name: "same resource different projects", refs: []string{"one", "two"},
			endpoints:  []string{toolboxTestURL, strings.Replace(toolboxTestURL, "/projects/proj/", "/projects/other/", 1)},
			wantLabels: []string{"one", "two"}},
		{name: "duplicate endpoint", refs: []string{"one", "two"}, endpoints: []string{toolboxTestURL, toolboxTestURL},
			wantLabels: []string{"one"}},
		{name: "duplicate endpoint with later placeholder", refs: []string{"one", "two"},
			endpoints: []string{toolboxTestURL, toolboxTestURL},
			tools: []any{map[string]any{"type": "mcp", "server_label": "two",
				"require_approval": "never", "allowed_tools": []string{"web_search"}}}, wantLabels: []string{"two"}},
		{name: "duplicate endpoint with first placeholder", refs: []string{"two", "one"},
			endpoints:  []string{toolboxTestURL, toolboxTestURL},
			tools:      []any{map[string]any{"type": "mcp", "server_label": "two", "require_approval": "never"}},
			wantLabels: []string{"two"}},
		{name: "duplicate endpoint with two placeholders", refs: []string{"one", "two"},
			endpoints: []string{toolboxTestURL, toolboxTestURL},
			tools: []any{map[string]any{"type": "mcp", "server_label": "one", "require_approval": "always"},
				map[string]any{"type": "mcp", "server_label": "two", "require_approval": "never"}},
			wantError: "multiple authored MCP entries"},
		{name: "exact URL and separate placeholder", refs: []string{"one", "two"},
			endpoints: []string{toolboxTestURL, toolboxTestURL},
			tools: []any{map[string]any{"type": "mcp", "server_label": "search", "server_url": toolboxTestURL},
				map[string]any{"type": "mcp", "server_label": "two", "require_approval": "never"}},
			wantError: "multiple authored MCP entries"},
		{name: "preserve authored alias and options", refs: []string{"tools"}, endpoints: []string{toolboxTestURL},
			tools: []any{map[string]any{"type": "mcp", "server_label": "search", "server_url": toolboxTestURL,
				"require_approval": "never", "allowed_tools": []string{"web_search"}}}, wantLabels: []string{"search"}},
		{name: "service-key options placeholder", refs: []string{"tools"}, endpoints: []string{toolboxTestURL},
			tools:      []any{map[string]any{"type": "mcp", "server_label": "tools", "require_approval": "never"}},
			wantLabels: []string{"tools"}},
		{name: "do not repin explicit URL", refs: []string{"tools"}, endpoints: []string{toolboxTestURL},
			tools: []any{map[string]any{"type": "mcp", "server_label": "tools",
				"server_url": strings.Replace(toolboxTestURL, "/versions/1/", "/versions/2/", 1)}}, wantError: "conflicts"},
		{name: "preserved alias collision", refs: []string{"research", "support"},
			endpoints: []string{toolboxTestURL, strings.Replace(toolboxTestURL, "/toolboxes/research/", "/toolboxes/support/", 1)},
			tools:     []any{map[string]any{"type": "mcp", "server_label": "support", "server_url": toolboxTestURL}},
			wantError: "conflicts"},
		{name: "untrusted host", refs: []string{"tools"}, endpoints: []string{"https://untrusted.example/mcp"},
			wantError: "HTTPS Foundry"},
		{name: "missing deployment", refs: []string{"tools"}, endpoints: []string{""}, wantError: "has not been deployed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{}
			var refs []agent_yaml.ToolboxReference
			for i, ref := range tt.refs {
				refs = append(refs, agent_yaml.ToolboxReference{Name: ref})
				env[envkey.ToolboxMCPEndpoint(ref)] = tt.endpoints[i]
			}
			before := make([]any, len(tt.tools))
			for i, tool := range tt.tools {
				before[i] = maps.Clone(tool.(map[string]any))
			}
			tools, attachments, err := planToolboxAttachments(tt.tools, refs, toolboxTestProject, env)
			if len(tt.tools) > 0 {
				require.Equal(t, before, tt.tools, "planning must not mutate authored maps")
			}
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.Len(t, attachments, len(tt.wantLabels))
			for i, label := range tt.wantLabels {
				tool, ok := tools[attachments[i].toolIndex].(map[string]any)
				require.True(t, ok)
				require.Equal(t, label, tool["server_label"])
				require.Equal(t, attachments[i].endpoint, tool["server_url"])
				if len(tt.tools) > 0 {
					for _, key := range []string{"require_approval", "allowed_tools"} {
						if value, exists := tt.tools[0].(map[string]any)[key]; exists {
							require.Equal(t, value, tool[key])
						}
					}
				} else {
					require.Equal(t, "always", tool["require_approval"])
				}
			}
		})
	}
}

func TestToolboxNodeSharedEndpointOverrides(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "later placeholder", true: "two placeholders"}[ambiguous], func(t *testing.T) {
			managed := &agent_yaml.PromptAgent{Tools: []any{
				map[string]any{"type": "mcp", "server_label": "two", "require_approval": "never"},
			}}
			if ambiguous {
				managed.Tools = append(managed.Tools, map[string]any{"type": "mcp", "server_label": "one"})
			}
			client := &fakeToolboxConnectionsClient{endpoint: toolboxTestProject,
				connections: map[string]azure.ToolboxConnectionProperties{}}
			g := &promptGraph{managed: managed, env: map[string]string{
				envkey.ToolboxMCPEndpoint("one"): toolboxTestURL,
				envkey.ToolboxMCPEndpoint("two"): toolboxTestURL,
			}}
			factoryCalls := 0
			node := toolboxNode(g, []agent_yaml.ToolboxReference{{Name: "one"}, {Name: "two"}},
				func() (toolboxConnectionEnsurer, error) {
					factoryCalls++
					return &foundryToolboxConnections{client: client, endpoint: toolboxTestProject}, nil
				})
			err := node.Resolve(t.Context())
			if ambiguous {
				require.ErrorContains(t, err, "multiple authored MCP entries")
				require.Zero(t, factoryCalls, "ambiguous overrides must fail before connection setup")
				require.Zero(t, client.puts)
				return
			}
			require.NoError(t, err)
			require.Len(t, managed.Tools, 1)
			tool, ok := managed.Tools[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "two", tool["server_label"])
			require.Equal(t, "never", tool["require_approval"])
			require.Equal(t, toolboxTestURL, tool["server_url"])
			require.NotEmpty(t, tool["project_connection_id"])
			require.Equal(t, 1, client.puts)
			require.NoError(t, node.Resolve(t.Context()))
			require.Len(t, managed.Tools, 1)
			require.Equal(t, 1, client.puts, "redeployment must reuse the shared connection")
		})
	}
}

type fakeToolboxConnectionsClient struct {
	mu            sync.Mutex
	endpoint      string
	readError     error
	connections   map[string]azure.ToolboxConnectionProperties
	puts          int
	verifications int
}

func (c *fakeToolboxConnectionsClient) ProjectEndpoint(context.Context) (string, error) {
	c.verifications++
	return c.endpoint, c.readError
}
func (c *fakeToolboxConnectionsClient) GetConnection(_ context.Context, name string) (*azure.ToolboxConnectionProperties, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, exists := c.connections[name]; exists {
		return &p, nil
	}
	return nil, nil
}
func (c *fakeToolboxConnectionsClient) PutConnection(_ context.Context, name string, p azure.ToolboxConnectionProperties) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.puts++
	c.connections[name] = p
	return nil
}

func TestToolboxNodePlanBeforeIOAndVerifyOnce(t *testing.T) {
	for _, tt := range []struct {
		name, advertised string
		invalid          bool
		readError        error
	}{
		{"success", toolboxTestProject, false, nil},
		{"local validation failure", toolboxTestProject, true, nil},
		{"same project name other account", "https://other.services.ai.azure.com/api/projects/proj", false, nil},
		{"unverifiable identity", "", false, errors.New("read denied")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeToolboxConnectionsClient{endpoint: tt.advertised, readError: tt.readError,
				connections: map[string]azure.ToolboxConnectionProperties{}}
			connections := &foundryToolboxConnections{client: client, endpoint: toolboxTestProject}
			managed := &agent_yaml.PromptAgent{ToolChoice: map[string]any{"type": "mcp", "server_label": "search"},
				Skills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
				Tools: []any{map[string]any{"type": "mcp", "server_url": toolboxTestURL, "server_label": "search",
					"project_connection_id": "stale", "require_approval": "never"}, map[string]any{"type": "web_search"}}}
			env := map[string]string{envkey.ToolboxMCPEndpoint("a"): toolboxTestURL,
				envkey.ToolboxMCPEndpoint("b"): strings.Replace(toolboxTestURL, "/toolboxes/research/", "/toolboxes/support/", 1)}
			if tt.invalid {
				env[envkey.ToolboxMCPEndpoint("b")] = ""
			}
			g := &promptGraph{managed: managed, env: env, settings: &PromptAgentSettings{ProjectEndpoint: toolboxTestProject}}
			factoryCalls := 0
			node := toolboxNode(g, []agent_yaml.ToolboxReference{{Name: "a"}, {Name: "b"}}, func() (toolboxConnectionEnsurer, error) {
				factoryCalls++
				return connections, nil
			})
			if tt.invalid {
				require.NoError(t, node.Validate())
				require.Error(t, node.Prepare())
				require.Zero(t, factoryCalls)
				return
			}
			require.NoError(t, node.Validate())
			err := node.Resolve(t.Context())
			if tt.invalid || tt.advertised != toolboxTestProject || tt.readError != nil {
				require.Error(t, err)
				require.Zero(t, client.puts)
				if tt.invalid {
					require.Zero(t, factoryCalls)
				}
				require.Equal(t, "stale", managed.Tools[0].(map[string]any)["project_connection_id"])
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, client.verifications)
			require.Len(t, managed.Tools, 3)
			require.Equal(t, 2, client.puts)
			tool := managed.Tools[0].(map[string]any)
			require.Equal(t, "search", tool["server_label"])
			require.Equal(t, "never", tool["require_approval"])
			require.NotEqual(t, "stale", tool["project_connection_id"])
			require.Equal(t, "search", managed.ToolChoice.(map[string]any)["server_label"])
			require.Len(t, managed.Skills, 1)
			require.NoError(t, node.Resolve(t.Context()))
			require.Equal(t, 2, client.puts)
			require.Len(t, managed.Tools, 3)
		})
	}
}

func TestToolboxConnectionReuseAndConflicts(t *testing.T) {
	attachment := toolboxAttachment{name: "research", version: "1", endpoint: toolboxTestURL}
	hashed := toolboxVersionConnectionName(attachment.name, attachment.version, attachment.endpoint)
	matching := azure.ToolboxConnectionProperties{Category: "RemoteTool", AuthType: "UserEntraToken",
		Audience: "https://ai.azure.com", Target: toolboxTestURL}
	for _, tt := range []struct {
		name      string
		stored    map[string]azure.ToolboxConnectionProperties
		explicit  bool
		wantError bool
		wantName  string
	}{
		{"new", nil, false, false, hashed},
		{"legacy reuse", map[string]azure.ToolboxConnectionProperties{"research-mcp": matching}, false, false, "research-mcp"},
		{"hashed reuse", map[string]azure.ToolboxConnectionProperties{hashed: matching}, false, false, hashed},
		{"incompatible legacy untouched", map[string]azure.ToolboxConnectionProperties{"research-mcp": {AuthType: "ApiKey"}}, false, false, hashed},
		{"hashed conflict", map[string]azure.ToolboxConnectionProperties{hashed: {AuthType: "ApiKey"}}, false, true, ""},
		{"explicit missing", nil, true, true, ""},
		{"explicit compatible", map[string]azure.ToolboxConnectionProperties{hashed: matching}, true, false, hashed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stored := map[string]azure.ToolboxConnectionProperties{}
			maps.Copy(stored, tt.stored)
			client := &fakeToolboxConnectionsClient{connections: stored}
			c := &foundryToolboxConnections{client: client, explicitNames: map[string]bool{hashed: tt.explicit}}
			name, err := c.EnsureConnection(t.Context(), attachment)
			if tt.wantError {
				require.Error(t, err)
				require.Zero(t, client.puts)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantName, name)
			for key, value := range tt.stored {
				require.Equal(t, value, stored[key], "existing connections must not be rewritten")
			}
		})
	}
}

func TestToolboxEndpointScopedNamesAndConcurrentReuse(t *testing.T) {
	client := &fakeToolboxConnectionsClient{connections: map[string]azure.ToolboxConnectionProperties{}}
	c := &foundryToolboxConnections{client: client}
	endpoints := []string{toolboxTestURL, strings.Replace(toolboxTestURL, "/projects/proj/", "/projects/other/", 1),
		strings.Replace(toolboxTestURL, "/versions/1/", "/versions/2/", 1)}
	var wg sync.WaitGroup
	for _, endpoint := range endpoints {
		wg.Go(func() {
			name, version, err := parseToolboxMCPEndpoint(endpoint)
			if err != nil {
				t.Error(err)
				return
			}
			_, err = c.EnsureConnection(t.Context(), toolboxAttachment{name: name, version: version, endpoint: endpoint})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	require.Len(t, client.connections, 3)
	for _, endpoint := range endpoints {
		name, version, err := parseToolboxMCPEndpoint(endpoint)
		require.NoError(t, err)
		id, err := c.EnsureConnection(t.Context(), toolboxAttachment{name: name, version: version, endpoint: endpoint})
		require.NoError(t, err)
		require.Equal(t, endpoint, client.connections[id].Target)
	}
	require.Equal(t, 3, client.puts)
	for _, name := range []string{"research", "research-v2", strings.Repeat("a", 100)} {
		id := toolboxVersionConnectionName(name, strings.Repeat("123", 40), toolboxTestURL)
		require.Regexp(t, `^[a-zA-Z0-9][a-zA-Z0-9_-]{2,32}$`, id)
	}
}

func TestToolboxDiagnosticsRedactCredentials(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:password@untrusted.example/mcp?sig=secret#private-fragment-value",
		"https://user:password@acct.services.ai.azure.com/api/projects/proj/bad?sig=secret#private-fragment-value",
	} {
		_, _, err := planToolboxAttachments(nil, []agent_yaml.ToolboxReference{{Name: "tools"}}, toolboxTestProject,
			map[string]string{envkey.ToolboxMCPEndpoint("tools"): endpoint})
		require.Error(t, err)
		for _, secret := range []string{"password", "secret", "private-fragment-value"} {
			require.NotContains(t, err.Error(), secret)
		}
	}
}

func TestToolboxProjectIdentityAndOwnership(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/arm-account/projects/proj"
	settings := &PromptAgentSettings{ProjectEndpoint: "https://custom-subdomain.services.ai.azure.com/api/projects/proj"}
	actual, err := toolboxProjectResourceID(map[string]string{"AZURE_AI_PROJECT_ID": id}, settings)
	require.NoError(t, err)
	require.Equal(t, id, actual, "ARM identity must not be inferred from a custom subdomain")
	_, err = toolboxProjectResourceID(map[string]string{"AZURE_AI_PROJECT_ID": strings.Replace(id, "/projects/proj", "/projects/other", 1)}, settings)
	require.ErrorContains(t, err, "different projects")
	_, err = toolboxProjectResourceID(map[string]string{"AZURE_AI_PROJECT_ID": "invalid"}, settings)
	require.Error(t, err)

	// Connection names come from effective resource names, not YAML service keys.
	g := &promptGraph{env: map[string]string{"AZURE_AI_PROJECT_ID": id}, projectServices: map[string]*azdext.ServiceConfig{
		"research-mcp": promptConnectionService(t, "research-mcp", "other-connection"),
	}}
	// Ownership resolution precedes any network access. Use a credential that
	// can be constructed offline; no token is requested by the constructor.
	credential := promptCredential("")
	require.NotNil(t, credential)
	raw, err := newFoundryToolboxConnections(g, settings, credential)
	require.NoError(t, err)
	c, ok := raw.(*foundryToolboxConnections)
	require.True(t, ok)
	require.True(t, c.explicitNames["other-connection"])
	require.False(t, c.explicitNames["research-mcp"])
	g.projectRoot = t.TempDir()
	g.projectServices["broken"] = &azdext.ServiceConfig{Name: "broken", Host: foundryConnectionHost,
		AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./missing-connection.yaml"})}
	_, err = newFoundryToolboxConnections(g, settings, credential)
	require.ErrorContains(t, err, "missing-connection.yaml")
}

func TestToolboxDependencies(t *testing.T) {
	for _, tt := range []struct {
		name, host string
		uses       []string
		disabled   bool
	}{
		{"missing", "", nil, false}, {"wrong host", "other", nil, false},
		{"missing uses", foundryToolboxHost, nil, false}, {"disabled", foundryToolboxHost, []string{"tools"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := &promptGraph{agentService: &azdext.ServiceConfig{Name: "agent", Uses: tt.uses},
				projectServices:   map[string]*azdext.ServiceConfig{},
				dependencyEnabled: func(context.Context, string) (bool, error) { return !tt.disabled, nil }}
			if tt.host != "" {
				g.projectServices["tools"] = &azdext.ServiceConfig{Name: "tools", Host: tt.host}
			}
			require.Error(t, validateToolboxDependencies(t.Context(), g, []agent_yaml.ToolboxReference{{Name: "tools"}}))
		})
	}
}

func TestPromptAgentRejectsToolboxesWithoutHarness(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.PromptAgent{Toolboxes: []agent_yaml.ToolboxReference{{Name: "tools"}}}}
	require.ErrorContains(t, g.agentNode().Validate(), "harness")
}

func TestToolboxEndpointPayloadValidation(t *testing.T) {
	for _, tt := range []struct {
		name, endpoint string
		valid          bool
	}{
		{"api version", toolboxTestURL, true},
		{"no query", strings.Split(toolboxTestURL, "?")[0], true},
		{"userinfo", strings.Replace(toolboxTestURL, "https://", "https://user:password@", 1), false},
		{"fragment", toolboxTestURL + "#secret", false},
		{"empty fragment", toolboxTestURL + "#", false},
		{"signature", toolboxTestURL + "&sig=secret", false},
		{"unknown parameter", toolboxTestURL + "&other=value", false},
		{"malformed query", toolboxTestURL + "&other=%zz", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeToolboxConnectionsClient{endpoint: toolboxTestProject,
				connections: map[string]azure.ToolboxConnectionProperties{}}
			g := &promptGraph{managed: &agent_yaml.PromptAgent{}, env: map[string]string{
				envkey.ToolboxMCPEndpoint("tools"): tt.endpoint}}
			factoryCalls := 0
			node := toolboxNode(g, []agent_yaml.ToolboxReference{{Name: "tools"}},
				func() (toolboxConnectionEnsurer, error) {
					factoryCalls++
					return &foundryToolboxConnections{client: client, endpoint: toolboxTestProject}, nil
				})
			require.NoError(t, node.Validate())
			err := node.Prepare()
			if !tt.valid {
				require.Error(t, err)
				require.Zero(t, factoryCalls)
				require.Empty(t, g.managed.Tools)
				require.Empty(t, client.connections)
				return
			}
			require.NoError(t, err)
			require.NoError(t, node.Resolve(t.Context()))
			require.Equal(t, 1, client.puts)
			tool, ok := g.managed.Tools[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tt.endpoint, tool["server_url"])
			connection, ok := tool["project_connection_id"].(string)
			require.True(t, ok)
			require.Equal(t, tt.endpoint, client.connections[connection].Target)
			payload, err := json.Marshal(struct {
				Tools       []any
				Connections map[string]azure.ToolboxConnectionProperties
			}{g.managed.Tools, client.connections})
			require.NoError(t, err)
			require.NotContains(t, string(payload), "secret")
			require.NotContains(t, string(payload), "password")
		})
	}
}

func TestToolboxMarkerCollisions(t *testing.T) {
	for _, names := range [][]string{{"my-tool", "my_tool"}, {"tools", "TOOLS"}, {"my--tool", "my-tool"}} {
		t.Run(strings.Join(names, "/"), func(t *testing.T) {
			refs := []agent_yaml.ToolboxReference{{Name: names[0]}, {Name: names[1]}}
			// Reject before looking up deployment markers, including an empty environment.
			for _, env := range []map[string]string{nil, {envkey.ToolboxMCPEndpoint(names[0]): toolboxTestURL}} {
				_, _, err := planToolboxAttachments(nil, refs, toolboxTestProject, env)
				require.ErrorContains(t, err, "share environment key")
			}
		})
	}
	_, attachments, err := planToolboxAttachments(nil,
		[]agent_yaml.ToolboxReference{{Name: "tools"}, {Name: "tools"}}, toolboxTestProject,
		map[string]string{envkey.ToolboxMCPEndpoint("tools"): toolboxTestURL})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
}

func TestToolboxPlanPrecedesMemoryCreation(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid plan preserves injected memory tool", true: "ambiguity blocks memory"}[ambiguous],
			func(t *testing.T) {
				memory := &agent_yaml.PromptMemory{Store: "store", ChatModel: "chat", EmbeddingModel: "embedding"}
				g, client, memoryDependency := newMemoryTestGraph(memory)
				g.managed.Tools = []any{map[string]any{"type": "mcp", "server_label": "two", "require_approval": "never"}}
				if ambiguous {
					g.managed.Tools = append(g.managed.Tools, map[string]any{"type": "mcp", "server_label": "one"})
				}
				g.env = map[string]string{envkey.ToolboxMCPEndpoint("one"): toolboxTestURL,
					envkey.ToolboxMCPEndpoint("two"):   toolboxTestURL,
					envkey.ToolboxMCPEndpoint("three"): strings.Replace(toolboxTestURL, "/research/", "/support/", 1)}
				connections := &fakeToolboxConnectionsClient{endpoint: toolboxTestProject,
					connections: map[string]azure.ToolboxConnectionProperties{}}
				node := toolboxNode(g, []agent_yaml.ToolboxReference{{Name: "one"}, {Name: "two"}, {Name: "three"}},
					func() (toolboxConnectionEnsurer, error) {
						return &foundryToolboxConnections{client: connections, endpoint: toolboxTestProject}, nil
					})
				g.nodes = []promptNode{*memoryDependency, *node}
				err := g.resolve(t.Context(), nil)
				if ambiguous {
					require.ErrorContains(t, err, "multiple authored MCP entries")
					require.Nil(t, client.request)
					require.Zero(t, connections.verifications)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, client.request)
				require.Len(t, g.managed.Tools, 3)
				require.Equal(t, memorySearchToolType, g.managed.Tools[1].(map[string]any)["type"])
				require.Equal(t, "three", g.managed.Tools[2].(map[string]any)["server_label"])
				require.NoError(t, g.resolve(t.Context(), nil))
				require.Len(t, g.managed.Tools, 3)
				require.Equal(t, 2, connections.puts)
			})
	}
}

func TestToolboxSharedEndpointToolChoice(t *testing.T) {
	for _, names := range [][]string{{"one", "two"}, {"two", "one"}} {
		for _, authoredAlias := range []bool{false, true} {
			t.Run(strings.Join(names, "/")+map[bool]string{false: " generated", true: " authored alias"}[authoredAlias],
				func(t *testing.T) {
					choice := map[string]any{"type": "mcp", "server_label": "two", "name": "web_search"}
					managed := &agent_yaml.PromptAgent{ToolChoice: choice}
					want := names[0]
					if authoredAlias {
						managed.Tools = []any{map[string]any{"type": "mcp", "server_label": "search", "server_url": toolboxTestURL}}
						want = "search"
					}
					g := &promptGraph{managed: managed, env: map[string]string{
						envkey.ToolboxMCPEndpoint("one"): toolboxTestURL, envkey.ToolboxMCPEndpoint("two"): toolboxTestURL}}
					client := &fakeToolboxConnectionsClient{endpoint: toolboxTestProject,
						connections: map[string]azure.ToolboxConnectionProperties{}}
					node := toolboxNode(g, []agent_yaml.ToolboxReference{{Name: names[0]}, {Name: names[1]}},
						func() (toolboxConnectionEnsurer, error) {
							return &foundryToolboxConnections{client: client, endpoint: toolboxTestProject}, nil
						})
					require.NoError(t, node.Validate())
					require.NoError(t, node.Resolve(t.Context()))
					require.Equal(t, map[string]any{"type": "mcp", "server_label": want, "name": "web_search"}, managed.ToolChoice)
					require.Equal(t, "two", choice["server_label"], "authored selector must not be mutated")
				})
		}
	}
}
