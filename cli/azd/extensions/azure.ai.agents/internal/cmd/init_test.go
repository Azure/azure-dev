// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsRecoverableDeploymentSelectionError_StructuredReason(t *testing.T) {
	t.Parallel()

	st := status.New(codes.FailedPrecondition, "no valid SKUs for selected model")
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: azdext.AiErrorReasonNoValidSkus,
		Domain: azdext.AiErrorDomain,
	})
	if err != nil {
		t.Fatalf("failed to attach grpc error details: %v", err)
	}

	if !isRecoverableDeploymentSelectionError(withDetails.Err()) {
		t.Fatalf("expected structured AI reason to be recoverable")
	}
}

func TestIsRecoverableDeploymentSelectionError_NonRecoverableStructuredReason(t *testing.T) {
	t.Parallel()

	st := status.New(codes.InvalidArgument, "quota location is required")
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: azdext.AiErrorReasonQuotaLocation,
		Domain: azdext.AiErrorDomain,
	})
	if err != nil {
		t.Fatalf("failed to attach grpc error details: %v", err)
	}

	if isRecoverableDeploymentSelectionError(withDetails.Err()) {
		t.Fatalf("expected structured quota-location error to be non-recoverable")
	}
}

func TestIsRecoverableDeploymentSelectionError_UnstructuredError(t *testing.T) {
	t.Parallel()

	if isRecoverableDeploymentSelectionError(
		status.Error(codes.Internal, "no deployment found for model \"foo\" with the specified options"),
	) {
		t.Fatalf("expected unstructured error to be non-recoverable")
	}
}

func TestHasAiErrorReason(t *testing.T) {
	t.Parallel()

	st := status.New(codes.NotFound, "no locations with sufficient quota")
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: azdext.AiErrorReasonNoLocationsWithQuota,
		Domain: azdext.AiErrorDomain,
	})
	if err != nil {
		t.Fatalf("failed to attach grpc error details: %v", err)
	}

	if !hasAiErrorReason(withDetails.Err(), azdext.AiErrorReasonNoLocationsWithQuota) {
		t.Fatalf("expected reason to be detected")
	}
	if hasAiErrorReason(withDetails.Err(), azdext.AiErrorReasonNoValidSkus) {
		t.Fatalf("expected non-matching reason to be false")
	}
}

func TestCopyDirectory_RefusesToCopyIntoSubtree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(src, "child")

	//nolint:gosec // test fixture directory permissions are intentional
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	if err := copyDirectory(src, dst); err == nil {
		t.Fatalf("expected error when destination is inside source")
	}
}

func TestCopyDirectory_NoOpWhenSamePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := copyDirectory(dir, dir); err != nil {
		t.Fatalf("expected no error when src==dst: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "file.txt")); err != nil {
		t.Fatalf("expected file to still exist: %v", err)
	}
}

func TestValidateLocalContainerAgentCopy_AllowsReinitInPlace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPointer := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(manifestPointer, []byte("name: test"), 0644); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}

	// InitAction with nil azdClient is safe here because isSamePath returns early
	// before any prompting code is reached.
	a := &InitAction{}
	if err := a.validateLocalContainerAgentCopy(context.Background(), manifestPointer, dir); err != nil {
		t.Fatalf("expected no error for re-init in place: %v", err)
	}
}

func TestIsSubpath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		child    string
		parent   string
		expected bool
	}{
		{"child inside parent", "/a/b/c", "/a/b", true},
		{"child equals parent", "/a/b", "/a/b", true},
		{"child outside parent", "/a/b", "/a/b/c", false},
		{"sibling directories", "/a/b", "/a/c", false},
		{"parent with trailing slash", "/a/b/c", "/a/b/", true},
		{"relative same", ".", ".", true},
		{"relative child", "a/b", "a", true},
		{"relative outside", "a", "a/b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSubpath(tt.child, tt.parent)
			if result != tt.expected {
				t.Errorf("isSubpath(%q, %q) = %v, want %v", tt.child, tt.parent, result, tt.expected)
			}
		})
	}
}

func TestIsSamePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{"identical paths", "/a/b/c", "/a/b/c", true},
		{"trailing slash difference", "/a/b/c", "/a/b/c/", true},
		{"with dot segments", "/a/b/../b/c", "/a/b/c", true},
		{"different paths", "/a/b", "/a/c", false},
		{"relative same", "a/b", "a/b", true},
		{"relative with dots", "a/b/../b", "a/b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSamePath(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("isSamePath(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                { return m.isDir }
func (m mockDirEntry) Type() os.FileMode          { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func TestFormatDirectoryPreview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entries    []os.DirEntry
		maxEntries int
		expected   string
	}{
		{
			name:       "empty entries",
			entries:    []os.DirEntry{},
			maxEntries: 5,
			expected:   "",
		},
		{
			name: "fewer than max",
			entries: []os.DirEntry{
				mockDirEntry{name: "file.txt", isDir: false},
				mockDirEntry{name: "dir", isDir: true},
			},
			maxEntries: 5,
			expected:   "dir/, file.txt",
		},
		{
			name: "exactly max",
			entries: []os.DirEntry{
				mockDirEntry{name: "a.txt", isDir: false},
				mockDirEntry{name: "b.txt", isDir: false},
			},
			maxEntries: 2,
			expected:   "a.txt, b.txt",
		},
		{
			name: "more than max",
			entries: []os.DirEntry{
				mockDirEntry{name: "c.txt", isDir: false},
				mockDirEntry{name: "a.txt", isDir: false},
				mockDirEntry{name: "b.txt", isDir: false},
				mockDirEntry{name: "d.txt", isDir: false},
			},
			maxEntries: 2,
			expected:   "a.txt, b.txt, ... (+2 more)",
		},
		{
			name: "directories get trailing slash",
			entries: []os.DirEntry{
				mockDirEntry{name: "mydir", isDir: true},
				mockDirEntry{name: "myfile", isDir: false},
			},
			maxEntries: 5,
			expected:   "mydir/, myfile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := formatDirectoryPreview(tt.entries, tt.maxEntries)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("formatDirectoryPreview() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestParseGitHubUrlNaive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected *GitHubUrlInfo
	}{
		{
			name: "github.com blob URL",
			url:  "https://github.com/owner/repo/blob/main/path/to/file.yaml",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "github.com blob URL with fragment",
			url:  "https://github.com/owner/repo/blob/main/path/to/file.yaml#L10",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "github.com blob URL with query parameter",
			url:  "https://github.com/owner/repo/blob/main/path/to/file.yaml?plain=1",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "github.com blob URL with both fragment and query",
			url:  "https://github.com/owner/repo/blob/develop/path/file.yaml?plain=1#L20-L30",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "develop",
				FilePath: "path/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "raw.githubusercontent.com URL",
			url:  "https://raw.githubusercontent.com/owner/repo/refs/heads/main/path/to/file.yaml",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "raw.githubusercontent.com URL with query parameter",
			url:  "https://raw.githubusercontent.com/owner/repo/refs/heads/main/path/to/file.yaml?token=abc123",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "URL with branch containing slash (naive parsing treats first part as branch)",
			url:  "https://github.com/owner/repo/blob/feature/my-branch/file.yaml",
			// This is a known limitation - the naive parser will incorrectly treat "feature" as the branch
			// and "my-branch/file.yaml" as the file path. This is acceptable since the function is designed
			// to handle simple cases and fall back to full parsing for complex branch names.
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "feature",
				FilePath: "my-branch/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name:     "invalid URL",
			url:      "not a url",
			expected: nil,
		},
		{
			name:     "non-github URL",
			url:      "https://gitlab.com/owner/repo/blob/main/file.yaml",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &InitAction{}
			result := a.parseGitHubUrlNaive(tt.url)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatalf("expected non-nil result, got nil")
			}

			if result.RepoSlug != tt.expected.RepoSlug {
				t.Errorf("RepoSlug = %q, want %q", result.RepoSlug, tt.expected.RepoSlug)
			}
			if result.Branch != tt.expected.Branch {
				t.Errorf("Branch = %q, want %q", result.Branch, tt.expected.Branch)
			}
			if result.FilePath != tt.expected.FilePath {
				t.Errorf("FilePath = %q, want %q", result.FilePath, tt.expected.FilePath)
			}
			if result.Hostname != tt.expected.Hostname {
				t.Errorf("Hostname = %q, want %q", result.Hostname, tt.expected.Hostname)
			}
		})
	}
}

func TestExtractToolboxAndConnectionConfigs_TypedTools(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{
					Name: "platform-tools",
					Kind: agent_yaml.ResourceKindToolbox,
				},
				Description: "Platform tools",
				Tools: []any{
					map[string]any{
						// Built-in tool — no connection
						"id": "bing_grounding",
					},
					map[string]any{
						// External tool with name — connection name from Name field
						"id":       "mcp",
						"name":     "github-copilot",
						"target":   "https://api.githubcopilot.com/mcp",
						"authType": "OAuth2",
						"credentials": map[string]any{
							"clientId":     "my-client-id",
							"clientSecret": "my-secret",
						},
					},
				},
			},
		},
	}

	toolboxes, connections, credEnvVars, err := extractToolboxAndConnectionConfigs(manifest)
	if err != nil {
		t.Fatalf("extractToolboxAndConnectionConfigs failed: %v", err)
	}

	// Only the external tool creates a connection (not bing_grounding)
	if len(connections) != 1 {
		t.Fatalf("Expected 1 connection, got %d", len(connections))
	}
	conn := connections[0]
	if conn.Name != "github-copilot" {
		t.Errorf("Expected connection name 'github-copilot', got '%s'", conn.Name)
	}
	if conn.Category != "RemoteTool" {
		t.Errorf("Expected category 'RemoteTool', got '%s'", conn.Category)
	}
	if conn.Target != "https://api.githubcopilot.com/mcp" {
		t.Errorf("Expected target, got '%s'", conn.Target)
	}
	if conn.AuthType != "OAuth2" {
		t.Errorf("Expected authType 'OAuth2', got '%s'", conn.AuthType)
	}

	// Credentials should be ${VAR} references, not raw values
	if conn.Credentials["clientId"] != "${FOUNDRY_TOOL_GITHUB_COPILOT_CLIENTID}" {
		t.Errorf("Expected env var ref for clientId, got '%v'", conn.Credentials["clientId"])
	}
	if conn.Credentials["clientSecret"] != "${FOUNDRY_TOOL_GITHUB_COPILOT_CLIENTSECRET}" {
		t.Errorf("Expected env var ref for clientSecret, got '%v'", conn.Credentials["clientSecret"])
	}

	// Raw values should be in the credEnvVars map
	if credEnvVars["FOUNDRY_TOOL_GITHUB_COPILOT_CLIENTID"] != "my-client-id" {
		t.Errorf("Expected env var value 'my-client-id', got '%s'",
			credEnvVars["FOUNDRY_TOOL_GITHUB_COPILOT_CLIENTID"])
	}
	if credEnvVars["FOUNDRY_TOOL_GITHUB_COPILOT_CLIENTSECRET"] != "my-secret" {
		t.Errorf("Expected env var value 'my-secret', got '%s'",
			credEnvVars["FOUNDRY_TOOL_GITHUB_COPILOT_CLIENTSECRET"])
	}

	// Verify toolbox has both tools
	if len(toolboxes) != 1 {
		t.Fatalf("Expected 1 toolbox, got %d", len(toolboxes))
	}
	tb := toolboxes[0]
	if tb.Name != "platform-tools" {
		t.Errorf("Expected toolbox name 'platform-tools', got '%s'", tb.Name)
	}
	if tb.Description != "Platform tools" {
		t.Errorf("Expected description 'Platform tools', got '%s'", tb.Description)
	}
	if len(tb.Tools) != 2 {
		t.Fatalf("Expected 2 tools, got %d", len(tb.Tools))
	}

	// First tool: built-in (no project_connection_id)
	if tb.Tools[0]["type"] != "bing_grounding" {
		t.Errorf("Expected tool[0] type 'bing_grounding', got '%v'", tb.Tools[0]["type"])
	}
	if _, hasConn := tb.Tools[0]["project_connection_id"]; hasConn {
		t.Errorf("Built-in tool should not have project_connection_id")
	}

	// Second tool: minimal (type + project_connection_id only)
	if tb.Tools[1]["project_connection_id"] != "github-copilot" {
		t.Errorf("Expected project_connection_id 'github-copilot', got '%v'",
			tb.Tools[1]["project_connection_id"])
	}
	if tb.Tools[1]["type"] != "mcp" {
		t.Errorf("Expected tool type 'mcp', got '%v'", tb.Tools[1]["type"])
	}
	// No server_url or server_label in init output — deploy enriches from connections
	if _, has := tb.Tools[1]["server_url"]; has {
		t.Errorf("Toolbox tool should not have server_url (deploy enriches it)")
	}
	if _, has := tb.Tools[1]["server_label"]; has {
		t.Errorf("Toolbox tool should not have server_label (deploy enriches it)")
	}
}

func TestExtractToolboxAndConnectionConfigs_RawToolsFallback(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{
					Name: "raw-toolbox",
					Kind: agent_yaml.ResourceKindToolbox,
				},
				Description: "Raw tools",
				Tools: []any{
					map[string]any{
						"id":                    "mcp",
						"name":                  "existing",
						"project_connection_id": "existing-conn",
					},
				},
			},
		},
	}

	toolboxes, connections, credEnvVars, err := extractToolboxAndConnectionConfigs(manifest)
	if err != nil {
		t.Fatalf("extractToolboxAndConnectionConfigs failed: %v", err)
	}

	// No connections or env vars extracted from raw tools
	if len(connections) != 0 {
		t.Errorf("Expected 0 connections, got %d", len(connections))
	}
	if len(credEnvVars) != 0 {
		t.Errorf("Expected 0 env vars, got %d", len(credEnvVars))
	}

	if len(toolboxes) != 1 {
		t.Fatalf("Expected 1 toolbox, got %d", len(toolboxes))
	}
	if toolboxes[0].Tools[0]["project_connection_id"] != "existing-conn" {
		t.Errorf("Expected 'existing-conn', got '%v'", toolboxes[0].Tools[0]["project_connection_id"])
	}
}

func TestExtractToolboxAndConnectionConfigs_NilManifest(t *testing.T) {
	t.Parallel()

	toolboxes, connections, credEnvVars, err := extractToolboxAndConnectionConfigs(nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if toolboxes != nil {
		t.Errorf("Expected nil toolboxes, got %v", toolboxes)
	}
	if connections != nil {
		t.Errorf("Expected nil connections, got %v", connections)
	}
	if credEnvVars != nil {
		t.Errorf("Expected nil env vars, got %v", credEnvVars)
	}
}

func TestExtractToolboxAndConnectionConfigs_CustomKeysCredentials(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{
					Name: "my-tools",
					Kind: agent_yaml.ResourceKindToolbox,
				},
				Tools: []any{
					map[string]any{
						"id":          "mcp",
						"name":        "custom-api",
						"target":      "https://example.com/mcp",
						"authType":    "CustomKeys",
						"credentials": map[string]any{"key": "my-api-key"},
					},
					map[string]any{
						"id":          "mcp",
						"name":        "oauth-tool",
						"target":      "https://example.com/oauth",
						"authType":    "OAuth2",
						"credentials": map[string]any{"clientId": "id", "clientSecret": "secret"},
					},
				},
			},
		},
	}

	_, connections, _, err := extractToolboxAndConnectionConfigs(manifest)
	if err != nil {
		t.Fatalf("extractToolboxAndConnectionConfigs failed: %v", err)
	}

	if len(connections) != 2 {
		t.Fatalf("Expected 2 connections, got %d", len(connections))
	}

	// CustomKeys: credentials must be nested under "keys"
	customConn := connections[0]
	keysRaw, ok := customConn.Credentials["keys"]
	if !ok {
		t.Fatal("CustomKeys connection missing 'keys' wrapper in credentials")
	}
	keys, ok := keysRaw.(map[string]any)
	if !ok {
		t.Fatalf("Expected 'keys' to be map[string]any, got %T", keysRaw)
	}
	if keys["key"] != "${FOUNDRY_TOOL_CUSTOM_API_KEY}" {
		t.Errorf("Expected env var ref for key, got '%v'", keys["key"])
	}

	// OAuth2: credentials should be flat (no "keys" wrapper)
	oauthConn := connections[1]
	if _, hasKeys := oauthConn.Credentials["keys"]; hasKeys {
		t.Error("OAuth2 connection should not have 'keys' wrapper")
	}
	if oauthConn.Credentials["clientId"] != "${FOUNDRY_TOOL_OAUTH_TOOL_CLIENTID}" {
		t.Errorf("Expected flat clientId ref, got '%v'", oauthConn.Credentials["clientId"])
	}
}

func TestInjectToolboxEnvVarsIntoDefinition_AddsEnvVars(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "my-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
			},
			EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
				{Name: "AZURE_OPENAI_ENDPOINT", Value: "${AZURE_OPENAI_ENDPOINT}"},
			},
		},
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{
					Name: "agent-tools",
					Kind: agent_yaml.ResourceKindToolbox,
				},
				Tools: []any{
					map[string]any{"id": "bing_grounding"},
				},
			},
		},
	}

	injectToolboxEnvVarsIntoDefinition(manifest)

	containerAgent := manifest.Template.(agent_yaml.ContainerAgent)
	envVars := *containerAgent.EnvironmentVariables

	if len(envVars) != 2 {
		t.Fatalf("Expected 2 env vars, got %d", len(envVars))
	}

	// Original env var is preserved
	if envVars[0].Name != "AZURE_OPENAI_ENDPOINT" {
		t.Errorf("Expected first env var to be AZURE_OPENAI_ENDPOINT, got %s", envVars[0].Name)
	}

	// Toolbox env var is injected
	if envVars[1].Name != "FOUNDRY_TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT" {
		t.Errorf("Expected injected env var name, got %s", envVars[1].Name)
	}
	if envVars[1].Value != "${FOUNDRY_TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT}" {
		t.Errorf("Expected env var reference value, got %s", envVars[1].Value)
	}
}

func TestInjectToolboxEnvVarsIntoDefinition_SkipsExisting(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "my-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
			},
			EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
				{Name: "FOUNDRY_TOOLBOX_MY_TOOLS_MCP_ENDPOINT", Value: "custom-value"},
			},
		},
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{
					Name: "my-tools",
					Kind: agent_yaml.ResourceKindToolbox,
				},
				Tools: []any{
					map[string]any{"id": "bing_grounding"},
				},
			},
		},
	}

	injectToolboxEnvVarsIntoDefinition(manifest)

	containerAgent := manifest.Template.(agent_yaml.ContainerAgent)
	envVars := *containerAgent.EnvironmentVariables

	// Should not add a duplicate — user's value is preserved
	if len(envVars) != 1 {
		t.Fatalf("Expected 1 env var (no duplicate), got %d", len(envVars))
	}
	if envVars[0].Value != "custom-value" {
		t.Errorf("Expected user value preserved, got %s", envVars[0].Value)
	}
}

func TestInjectToolboxEnvVarsIntoDefinition_MultipleToolboxes(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "my-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
			},
		},
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{Name: "search-tools", Kind: agent_yaml.ResourceKindToolbox},
				Tools:    []any{map[string]any{"id": "bing_grounding"}},
			},
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{Name: "github-tools", Kind: agent_yaml.ResourceKindToolbox},
				Tools:    []any{map[string]any{"id": "mcp", "target": "https://example.com"}},
			},
		},
	}

	injectToolboxEnvVarsIntoDefinition(manifest)

	containerAgent := manifest.Template.(agent_yaml.ContainerAgent)
	envVars := *containerAgent.EnvironmentVariables

	if len(envVars) != 2 {
		t.Fatalf("Expected 2 env vars, got %d", len(envVars))
	}
	if envVars[0].Name != "FOUNDRY_TOOLBOX_SEARCH_TOOLS_MCP_ENDPOINT" {
		t.Errorf("Expected first toolbox env var, got %s", envVars[0].Name)
	}
	if envVars[1].Name != "FOUNDRY_TOOLBOX_GITHUB_TOOLS_MCP_ENDPOINT" {
		t.Errorf("Expected second toolbox env var, got %s", envVars[1].Name)
	}
}

func TestInjectToolboxEnvVarsIntoDefinition_NoopForNilManifest(t *testing.T) {
	t.Parallel()

	// Should not panic
	injectToolboxEnvVarsIntoDefinition(nil)
}

func TestInjectToolboxEnvVarsIntoDefinition_NoopForPromptAgent(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Template: agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindPrompt,
				Name: "prompt-agent",
			},
		},
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{Name: "tools", Kind: agent_yaml.ResourceKindToolbox},
				Tools:    []any{map[string]any{"id": "bing_grounding"}},
			},
		},
	}

	injectToolboxEnvVarsIntoDefinition(manifest)

	// Template should be unchanged (still a PromptAgent, no EnvironmentVariables field)
	if _, ok := manifest.Template.(agent_yaml.PromptAgent); !ok {
		t.Error("Expected template to remain a PromptAgent")
	}
}

func TestInjectToolboxEnvVarsIntoDefinition_NoopWithoutToolboxes(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "my-agent",
			},
			EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
				{Name: "AZURE_OPENAI_ENDPOINT", Value: "${AZURE_OPENAI_ENDPOINT}"},
			},
		},
		Resources: []any{},
	}

	injectToolboxEnvVarsIntoDefinition(manifest)

	containerAgent := manifest.Template.(agent_yaml.ContainerAgent)
	if len(*containerAgent.EnvironmentVariables) != 1 {
		t.Errorf("Expected env vars unchanged, got %d", len(*containerAgent.EnvironmentVariables))
	}
}

func TestToolboxMCPEndpointEnvKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "my-tools", "FOUNDRY_TOOLBOX_MY_TOOLS_MCP_ENDPOINT"},
		{"spaces", "my tools", "FOUNDRY_TOOLBOX_MY_TOOLS_MCP_ENDPOINT"},
		{"mixed", "agent-tools v2", "FOUNDRY_TOOLBOX_AGENT_TOOLS_V2_MCP_ENDPOINT"},
		{"already upper", "TOOLS", "FOUNDRY_TOOLBOX_TOOLS_MCP_ENDPOINT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolboxMCPEndpointEnvKey(tt.input)
			if got != tt.expected {
				t.Errorf("toolboxMCPEndpointEnvKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
