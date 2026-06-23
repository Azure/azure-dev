// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostdeployHandler_NonHostedAgent_NoOp verifies postdeployHandler returns nil
// without any RPC calls when the service is an agent but not a hosted agent (no agent.yaml
// with kind: hostedAgent). With service-level event handlers, the core filters by host type,
// so this handler is only invoked for azure.ai.agent services.
func TestPostdeployHandler_NonHostedAgent_NoOp(t *testing.T) {
	t.Parallel()

	// Use a temp dir + explicit RelativePath so isHostedAgentService deterministically
	// returns false (no agent.yaml present) regardless of the test working directory.
	args := &azdext.ServiceEventArgs{
		Project: &azdext.ProjectConfig{
			Path: t.TempDir(),
		},
		Service: &azdext.ServiceConfig{Name: "my-agent", Host: AiAgentHost, RelativePath: "."},
	}

	// nil azdClient — the early return must fire before any RPC call.
	if err := postdeployHandler(t.Context(), nil, args); err != nil {
		t.Fatalf("expected no error for non-hosted agent service, got: %v", err)
	}
}

func TestIsHostedAgentServiceRejectsTraversal(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(projectRoot, 0o750); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatalf("failed to create outside directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "agent.yaml"), []byte("kind: hostedAgent\n"), 0o600); err != nil {
		t.Fatalf("failed to write outside agent.yaml: %v", err)
	}

	svc := &azdext.ServiceConfig{Name: "echo", Host: AiAgentHost, RelativePath: "../outside"}
	proj := &azdext.ProjectConfig{Path: projectRoot}

	if isHostedAgentService(svc, proj) {
		t.Fatal("expected traversal service path to be rejected")
	}
}

func TestKindEnvUpdateRejectsTraversal(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(projectRoot, 0o750); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatalf("failed to create outside directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "agent.yaml"), []byte("kind: hostedAgent\n"), 0o600); err != nil {
		t.Fatalf("failed to write outside agent.yaml: %v", err)
	}

	svc := &azdext.ServiceConfig{Name: "echo", Host: AiAgentHost, RelativePath: "../outside"}
	proj := &azdext.ProjectConfig{Path: projectRoot}

	err := kindEnvUpdate(t.Context(), nil, proj, svc, "dev")

	if err == nil || !strings.Contains(err.Error(), "invalid service path") {
		t.Fatalf("expected invalid service path error, got: %v", err)
	}
}

func TestParseConnectionIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		json      string
		expected  map[string]string
		wantError bool
	}{
		{
			name:     "valid array",
			json:     `[{"name":"my-conn","id":"/subscriptions/123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai/projects/proj/connections/my-conn"}]`,
			expected: map[string]string{"my-conn": "/subscriptions/123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai/projects/proj/connections/my-conn"},
		},
		{
			name:     "empty string",
			json:     "",
			expected: map[string]string{},
		},
		{
			name:     "empty array",
			json:     "[]",
			expected: map[string]string{},
		},
		{
			name:      "invalid JSON",
			json:      "not-json",
			wantError: true,
		},
		{
			name: "multiple connections",
			json: `[{"name":"conn-a","id":"id-a"},{"name":"conn-b","id":"id-b"}]`,
			expected: map[string]string{
				"conn-a": "id-a",
				"conn-b": "id-b",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseConnectionIDs(tt.json)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != len(tt.expected) {
				t.Fatalf("got %d entries, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("key %q: got %q, want %q", k, result[k], v)
				}
			}
		})
	}
}

func TestResolveToolboxConnectionIDs(t *testing.T) {
	t.Parallel()

	connIDs := map[string]string{
		"github_mcp_connection": "/subscriptions/123/connections/github_mcp_connection",
	}

	toolbox := project.Toolbox{
		Name: "test",
		Tools: []map[string]any{
			{"type": "web_search"},
			{"type": "mcp", "project_connection_id": "{{ github_mcp_connection }}"},
			{"type": "mcp", "project_connection_id": "unknown_conn"},
			{"type": "mcp", "project_connection_id": "github_mcp_connection"},
		},
	}

	resolveToolboxConnectionIDs(&toolbox, connIDs)

	// Tool without project_connection_id: unchanged
	if _, has := toolbox.Tools[0]["project_connection_id"]; has {
		t.Error("tool 0 should not have project_connection_id")
	}

	// Template ref {{ name }}: resolved to ARM ID
	if toolbox.Tools[1]["project_connection_id"] != "/subscriptions/123/connections/github_mcp_connection" {
		t.Errorf("tool 1 project_connection_id = %v, want ARM ID",
			toolbox.Tools[1]["project_connection_id"])
	}

	// Unknown connection: left as-is
	if toolbox.Tools[2]["project_connection_id"] != "unknown_conn" {
		t.Errorf("tool 2 project_connection_id = %v, want 'unknown_conn'",
			toolbox.Tools[2]["project_connection_id"])
	}

	// Bare name (no braces): also resolved
	if toolbox.Tools[3]["project_connection_id"] != "/subscriptions/123/connections/github_mcp_connection" {
		t.Errorf("tool 3 project_connection_id = %v, want ARM ID",
			toolbox.Tools[3]["project_connection_id"])
	}
}

func TestEnrichToolboxFromConnectionsUsesAllConnectionTypes(t *testing.T) {
	t.Parallel()

	config := &project.ServiceTargetAgentConfig{
		Connections: []project.Connection{
			{
				Name:   "shared-mcp",
				Target: "https://shared.example.com/mcp/",
			},
		},
		ToolConnections: []project.ToolConnection{
			{
				Name:   "tool-mcp",
				Target: "https://tool.example.com/mcp/",
			},
		},
	}
	testToolbox := project.Toolbox{
		Name: "test",
		Tools: []map[string]any{
			{"type": "mcp", "project_connection_id": "shared-mcp"},
			{"type": "mcp", "project_connection_id": "tool-mcp"},
			{"type": "mcp", "project_connection_id": "missing-mcp"},
		},
	}

	enrichToolboxFromConnections(&testToolbox, toolboxConnectionsByName(config))

	if testToolbox.Tools[0]["server_url"] != "https://shared.example.com/mcp/" {
		t.Errorf("tool 0 server_url = %v", testToolbox.Tools[0]["server_url"])
	}
	if testToolbox.Tools[0]["server_label"] != "shared-mcp" {
		t.Errorf("tool 0 server_label = %v", testToolbox.Tools[0]["server_label"])
	}
	if testToolbox.Tools[1]["server_url"] != "https://tool.example.com/mcp/" {
		t.Errorf("tool 1 server_url = %v", testToolbox.Tools[1]["server_url"])
	}
	if testToolbox.Tools[1]["server_label"] != "tool-mcp" {
		t.Errorf("tool 1 server_label = %v", testToolbox.Tools[1]["server_label"])
	}
	if _, has := testToolbox.Tools[2]["server_url"]; has {
		t.Errorf("tool 2 should not have server_url")
	}
}

func TestResolveTemplateRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"{{ my_conn }}", "my_conn"},
		{"{{my_conn}}", "my_conn"},
		{"{{  spaced  }}", "spaced"},
		{"my_conn", "my_conn"},
		{"", ""},
		{"{not_template}", "{not_template}"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := resolveTemplateRef(tt.input); got != tt.want {
				t.Errorf("resolveTemplateRef(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildConnectionCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		connections []project.Connection
		wantKeys    []string
		wantEmpty   bool
	}{
		{
			name:      "empty connections",
			wantEmpty: true,
		},
		{
			name: "connections with credentials",
			connections: []project.Connection{
				{
					Name:        "my-openai",
					Credentials: map[string]any{"key": "${OPENAI_API_KEY}"},
				},
				{
					Name:        "github-mcp",
					Credentials: map[string]any{"pat": "${GITHUB_PAT}"},
				},
			},
			wantKeys: []string{"my-openai", "github-mcp"},
		},
		{
			name: "skips connections without credentials",
			connections: []project.Connection{
				{
					Name:        "no-creds",
					Credentials: nil,
				},
				{
					Name:        "has-creds",
					Credentials: map[string]any{"secret": "val"},
				},
			},
			wantKeys: []string{"has-creds"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := buildConnectionCredentials(tt.connections)

			if tt.wantEmpty {
				if len(result) != 0 {
					t.Fatalf("expected empty map, got %v", result)
				}
				return
			}

			if len(result) != len(tt.wantKeys) {
				t.Fatalf("expected %d entries, got %d: %v",
					len(tt.wantKeys), len(result), result)
			}

			for _, key := range tt.wantKeys {
				if _, ok := result[key]; !ok {
					t.Errorf("expected key %q in result", key)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isHostedAgentService
// ---------------------------------------------------------------------------

func TestIsHostedAgentService_HostedKind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "agent.yaml"),
		[]byte("kind: hosted\nname: my-agent\n"), 0600,
	))

	svc := &azdext.ServiceConfig{Name: "svc", RelativePath: "."}
	proj := &azdext.ProjectConfig{Path: dir}

	assert.True(t, isHostedAgentService(svc, proj))
}

func TestIsHostedAgentService_NonHostedKind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "agent.yaml"),
		[]byte("kind: local\nname: my-agent\n"), 0600,
	))

	svc := &azdext.ServiceConfig{Name: "svc", RelativePath: "."}
	proj := &azdext.ProjectConfig{Path: dir}

	assert.False(t, isHostedAgentService(svc, proj))
}

func TestIsHostedAgentService_NoAgentYaml(t *testing.T) {
	t.Parallel()

	svc := &azdext.ServiceConfig{Name: "svc", RelativePath: "."}
	proj := &azdext.ProjectConfig{Path: t.TempDir()}

	assert.False(t, isHostedAgentService(svc, proj))
}

func TestIsHostedAgentService_InvalidYaml(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "agent.yaml"),
		[]byte(":::invalid yaml:::"), 0600,
	))

	svc := &azdext.ServiceConfig{Name: "svc", RelativePath: "."}
	proj := &azdext.ProjectConfig{Path: dir}

	assert.False(t, isHostedAgentService(svc, proj))
}

func TestIsHostedAgentService_MissingKindField(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "agent.yaml"),
		[]byte("name: my-agent\n"), 0600,
	))

	svc := &azdext.ServiceConfig{Name: "svc", RelativePath: "."}
	proj := &azdext.ProjectConfig{Path: dir}

	assert.False(t, isHostedAgentService(svc, proj))
}

func TestIsHostedAgentService_SubDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subDir := filepath.Join(dir, "agents", "bot")
	require.NoError(t, os.MkdirAll(subDir, 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, "agent.yaml"),
		[]byte("kind: hosted\nname: bot\n"), 0600,
	))

	svc := &azdext.ServiceConfig{Name: "bot", RelativePath: "agents/bot"}
	proj := &azdext.ProjectConfig{Path: dir}

	assert.True(t, isHostedAgentService(svc, proj))
}

// ---------------------------------------------------------------------------
// resolveEnvValue / resolveMapValues / resolveAnyValue
// ---------------------------------------------------------------------------

func TestResolveEnvValue(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"DB_HOST": "mydb.postgres.azure.com",
		"DB_PORT": "5432",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${DB_HOST}", "mydb.postgres.azure.com"},
		{"host=${DB_HOST}:${DB_PORT}", "host=mydb.postgres.azure.com:5432"},
		{"no-var", "no-var"},
		{"${UNDEFINED}", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, resolveEnvValue(tt.input, env))
		})
	}
}

func TestResolveMapValues(t *testing.T) {
	t.Parallel()

	env := map[string]string{"KEY": "val"}
	m := map[string]any{
		"a": "${KEY}",
		"b": "literal",
		"c": 42,
	}

	got := resolveMapValues(m, env)
	assert.Equal(t, "val", got["a"])
	assert.Equal(t, "literal", got["b"])
	assert.Equal(t, 42, got["c"])
}

func TestResolveAnyValue_NestedStructures(t *testing.T) {
	t.Parallel()

	env := map[string]string{"X": "resolved"}

	// Nested map
	nested := map[string]any{
		"inner": map[string]any{"key": "${X}"},
	}
	got := resolveAnyValue(nested, env)
	gotMap := got.(map[string]any)
	inner := gotMap["inner"].(map[string]any)
	assert.Equal(t, "resolved", inner["key"])

	// Slice
	slice := []any{"${X}", "plain", 99}
	gotSlice := resolveAnyValue(slice, env).([]any)
	assert.Equal(t, "resolved", gotSlice[0])
	assert.Equal(t, "plain", gotSlice[1])
	assert.Equal(t, 99, gotSlice[2])

	// Non-string type passthrough
	assert.Equal(t, true, resolveAnyValue(true, env))
}

// ---------------------------------------------------------------------------
// resolveToolboxEnvVars
// ---------------------------------------------------------------------------

func TestResolveToolboxEnvVars(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"TB_NAME": "my-toolbox",
		"TB_DESC": "A test toolbox",
		"URL":     "https://example.com",
	}

	tb := project.Toolbox{
		Name:        "${TB_NAME}",
		Description: "${TB_DESC}",
		Tools: []map[string]any{
			{"server_url": "${URL}", "type": "web_search"},
		},
	}

	resolveToolboxEnvVars(&tb, env)

	assert.Equal(t, "my-toolbox", tb.Name)
	assert.Equal(t, "A test toolbox", tb.Description)
	assert.Equal(t, "https://example.com", tb.Tools[0]["server_url"])
	assert.Equal(t, "web_search", tb.Tools[0]["type"])
}

// ---------------------------------------------------------------------------
// toolboxConnectionsByName
// ---------------------------------------------------------------------------

func TestToolboxConnectionsByName_NilConfig(t *testing.T) {
	t.Parallel()
	assert.Empty(t, toolboxConnectionsByName(nil))
}

func TestToolboxConnectionsByName_MergesBothTypes(t *testing.T) {
	t.Parallel()

	config := &project.ServiceTargetAgentConfig{
		Connections: []project.Connection{
			{Name: "conn-a", Target: "https://a.com"},
		},
		ToolConnections: []project.ToolConnection{
			{Name: "tool-b", Target: "https://b.com"},
		},
	}

	result := toolboxConnectionsByName(config)
	assert.Len(t, result, 2)
	assert.Equal(t, "https://a.com", result["conn-a"].Target)
	assert.Equal(t, "https://b.com", result["tool-b"].Target)
}

// ---------------------------------------------------------------------------
// postdeployHandler — skips non-hosted agent services
// ---------------------------------------------------------------------------

func TestPostdeployHandler_SkipsNonHostedAgentService(t *testing.T) {
	t.Parallel()

	// Service is an agent host but not a hosted agent (no agent.yaml) — handler
	// should return nil without making any RPC calls (azdClient is nil).
	args := &azdext.ServiceEventArgs{
		Project: &azdext.ProjectConfig{
			Path: t.TempDir(),
		},
		Service: &azdext.ServiceConfig{Name: "my-agent", Host: AiAgentHost, RelativePath: "."},
	}

	assert.NoError(t, postdeployHandler(t.Context(), nil, args))
}

// ---------------------------------------------------------------------------
// enrichToolboxFromConnections — server_url already set
// ---------------------------------------------------------------------------

func TestEnrichToolboxFromConnections_DoesNotOverrideExistingServerURL(t *testing.T) {
	t.Parallel()

	connByName := map[string]toolboxConnection{
		"my-conn": {Name: "my-conn", Target: "https://conn-target.com"},
	}

	tb := project.Toolbox{
		Name: "test",
		Tools: []map[string]any{
			{
				"type":                  "mcp",
				"project_connection_id": "my-conn",
				"server_url":            "https://custom-url.com",
			},
		},
	}

	enrichToolboxFromConnections(&tb, connByName)

	// server_url was already set — should not be overridden.
	assert.Equal(t, "https://custom-url.com", tb.Tools[0]["server_url"])
	// server_label should still be filled in.
	assert.Equal(t, "my-conn", tb.Tools[0]["server_label"])
}

func TestEnrichToolboxFromConnections_EmptyTarget(t *testing.T) {
	t.Parallel()

	connByName := map[string]toolboxConnection{
		"no-target": {Name: "no-target", Target: ""},
	}

	tb := project.Toolbox{
		Name: "test",
		Tools: []map[string]any{
			{"type": "mcp", "project_connection_id": "no-target"},
		},
	}

	enrichToolboxFromConnections(&tb, connByName)

	// Empty target → server_url should NOT be set.
	_, hasURL := tb.Tools[0]["server_url"]
	assert.False(t, hasURL)
	// server_label should still be set.
	assert.Equal(t, "no-target", tb.Tools[0]["server_label"])
}
