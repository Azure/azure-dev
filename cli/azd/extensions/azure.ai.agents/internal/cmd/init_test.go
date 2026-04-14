// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
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
						// Built-in tool -- no connection
						"type": "bing_grounding",
					},
					map[string]any{
						// External tool with name -- connection name from Name field
						"type":     "mcp",
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
	if conn.Credentials["clientId"] != "${PARAM_GITHUB_COPILOT_CLIENTID}" {
		t.Errorf("Expected env var ref for clientId, got '%v'", conn.Credentials["clientId"])
	}
	if conn.Credentials["clientSecret"] != "${PARAM_GITHUB_COPILOT_CLIENTSECRET}" {
		t.Errorf("Expected env var ref for clientSecret, got '%v'", conn.Credentials["clientSecret"])
	}

	// Raw values should be in the credEnvVars map
	if credEnvVars["PARAM_GITHUB_COPILOT_CLIENTID"] != "my-client-id" {
		t.Errorf("Expected env var value 'my-client-id', got '%s'",
			credEnvVars["PARAM_GITHUB_COPILOT_CLIENTID"])
	}
	if credEnvVars["PARAM_GITHUB_COPILOT_CLIENTSECRET"] != "my-secret" {
		t.Errorf("Expected env var value 'my-secret', got '%s'",
			credEnvVars["PARAM_GITHUB_COPILOT_CLIENTSECRET"])
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
	// No server_url or server_label in init output -- deploy enriches from connections
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
						"type":                  "mcp",
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
						"type":        "mcp",
						"name":        "custom-api",
						"target":      "https://example.com/mcp",
						"authType":    "CustomKeys",
						"credentials": map[string]any{"key": "my-api-key"},
					},
					map[string]any{
						"type":        "mcp",
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

	// CustomKeys: credentials stored as-is (no "keys" wrapper)
	customConn := connections[0]
	if customConn.Credentials["key"] != "${PARAM_CUSTOM_API_KEY}" {
		t.Errorf("Expected env var ref for key, got '%v'", customConn.Credentials["key"])
	}
	if _, hasKeys := customConn.Credentials["keys"]; hasKeys {
		t.Error("CustomKeys connection should not have 'keys' wrapper")
	}

	// OAuth2: credentials should be flat (no "keys" wrapper)
	oauthConn := connections[1]
	if _, hasKeys := oauthConn.Credentials["keys"]; hasKeys {
		t.Error("OAuth2 connection should not have 'keys' wrapper")
	}
	if oauthConn.Credentials["clientId"] != "${PARAM_OAUTH_TOOL_CLIENTID}" {
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
				{Protocol: "responses", Version: "1.0.0"},
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
					map[string]any{"type": "bing_grounding"},
				},
			},
		},
	}

	if err := injectToolboxEnvVarsIntoDefinition(manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
	if envVars[1].Name != "TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT" {
		t.Errorf("Expected injected env var name, got %s", envVars[1].Name)
	}
	if envVars[1].Value != "${TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT}" {
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
				{Protocol: "responses", Version: "1.0.0"},
			},
			EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
				{Name: "TOOLBOX_MY_TOOLS_MCP_ENDPOINT", Value: "custom-value"},
			},
		},
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{
					Name: "my-tools",
					Kind: agent_yaml.ResourceKindToolbox,
				},
				Tools: []any{
					map[string]any{"type": "bing_grounding"},
				},
			},
		},
	}

	err := injectToolboxEnvVarsIntoDefinition(manifest)

	if err == nil {
		t.Fatal("expected error for duplicate env var, got nil")
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
				{Protocol: "responses", Version: "1.0.0"},
			},
		},
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{Name: "search-tools", Kind: agent_yaml.ResourceKindToolbox},
				Tools:    []any{map[string]any{"type": "bing_grounding"}},
			},
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{Name: "github-tools", Kind: agent_yaml.ResourceKindToolbox},
				Tools:    []any{map[string]any{"type": "mcp", "target": "https://example.com"}},
			},
		},
	}

	if err := injectToolboxEnvVarsIntoDefinition(manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	containerAgent := manifest.Template.(agent_yaml.ContainerAgent)
	envVars := *containerAgent.EnvironmentVariables

	if len(envVars) != 2 {
		t.Fatalf("Expected 2 env vars, got %d", len(envVars))
	}
	if envVars[0].Name != "TOOLBOX_SEARCH_TOOLS_MCP_ENDPOINT" {
		t.Errorf("Expected first toolbox env var, got %s", envVars[0].Name)
	}
	if envVars[1].Name != "TOOLBOX_GITHUB_TOOLS_MCP_ENDPOINT" {
		t.Errorf("Expected second toolbox env var, got %s", envVars[1].Name)
	}
}

func TestInjectToolboxEnvVarsIntoDefinition_NoopForNilManifest(t *testing.T) {
	t.Parallel()

	// Should not panic or error
	if err := injectToolboxEnvVarsIntoDefinition(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
				Tools:    []any{map[string]any{"type": "bing_grounding"}},
			},
		},
	}

	if err := injectToolboxEnvVarsIntoDefinition(manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	if err := injectToolboxEnvVarsIntoDefinition(manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
		{"simple", "my-tools", "TOOLBOX_MY_TOOLS_MCP_ENDPOINT"},
		{"spaces", "my tools", "TOOLBOX_MY_TOOLS_MCP_ENDPOINT"},
		{"mixed", "agent-tools v2", "TOOLBOX_AGENT_TOOLS_V2_MCP_ENDPOINT"},
		{"already upper", "TOOLS", "TOOLBOX_TOOLS_MCP_ENDPOINT"},
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

func TestExtractConnectionConfigs_SurfacesCredentialsType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		connResource     agent_yaml.ConnectionResource
		wantAuthType     string
		wantCredHasType  bool
		wantCredKeyCount int
		wantEnvVarCount  int
	}{
		{
			name: "surfaces credentials.type to authType when authType is empty",
			connResource: agent_yaml.ConnectionResource{
				Resource: agent_yaml.Resource{
					Name: "my-conn",
					Kind: agent_yaml.ResourceKindConnection,
				},
				Target: "https://example.com",
				Credentials: map[string]any{
					"type": "CustomKeys",
					"key":  "secret-value",
				},
			},
			wantAuthType:     "CustomKeys",
			wantCredHasType:  false,
			wantCredKeyCount: 1,
			wantEnvVarCount:  1, // only "key" externalized; "type" was lifted out
		},
		{
			name: "preserves explicit authType even if credentials.type differs",
			connResource: agent_yaml.ConnectionResource{
				Resource: agent_yaml.Resource{
					Name: "my-conn",
					Kind: agent_yaml.ResourceKindConnection,
				},
				Target:   "https://example.com",
				AuthType: agent_yaml.AuthTypeAAD,
				Credentials: map[string]any{
					"type": "CustomKeys",
					"key":  "val",
				},
			},
			wantAuthType:     string(agent_yaml.AuthTypeAAD),
			wantCredHasType:  true,
			wantCredKeyCount: 2,
			wantEnvVarCount:  2, // both "type" and "key" externalized
		},
		{
			name: "no credentials.type and no authType stays empty",
			connResource: agent_yaml.ConnectionResource{
				Resource: agent_yaml.Resource{
					Name: "my-conn",
					Kind: agent_yaml.ResourceKindConnection,
				},
				Target:      "https://example.com",
				Credentials: map[string]any{"key": "val"},
			},
			wantAuthType:     "",
			wantCredHasType:  false,
			wantCredKeyCount: 1,
			wantEnvVarCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &agent_yaml.AgentManifest{
				Resources: []any{tt.connResource},
			}
			conns, envVars, err := extractConnectionConfigs(manifest)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(conns) != 1 {
				t.Fatalf("expected 1 connection, got %d", len(conns))
			}
			conn := conns[0]
			if conn.AuthType != tt.wantAuthType {
				t.Errorf("AuthType = %q, want %q", conn.AuthType, tt.wantAuthType)
			}
			_, hasType := conn.Credentials["type"]
			if hasType != tt.wantCredHasType {
				t.Errorf("credentials has 'type' = %v, want %v",
					hasType, tt.wantCredHasType)
			}
			if len(conn.Credentials) != tt.wantCredKeyCount {
				t.Errorf("credentials key count = %d, want %d",
					len(conn.Credentials), tt.wantCredKeyCount)
			}
			if len(envVars) != tt.wantEnvVarCount {
				t.Errorf("env var count = %d, want %d",
					len(envVars), tt.wantEnvVarCount)
			}
			// Verify credentials are externalized (contain ${...} references)
			for k, v := range conn.Credentials {
				vStr, ok := v.(string)
				if !ok || !strings.HasPrefix(vStr, "${") {
					t.Errorf("credential %q should be externalized, got %v", k, v)
				}
			}
		})
	}
}

func TestCheckNotDirectory_ReturnsNilForFile(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(file, []byte("name: test"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := checkNotDirectory(file); err != nil {
		t.Fatalf("expected nil for a regular file, got: %v", err)
	}
}

func TestCheckNotDirectory_ReturnsNilForNonexistentPath(t *testing.T) {
	t.Parallel()

	if err := checkNotDirectory(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("expected nil for nonexistent path, got: %v", err)
	}
}

func TestCheckNotDirectory_ErrorForDirectoryWithManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifest := filepath.Join(dir, "agent.manifest.yaml")
	// Must include a "template" key so looksLikeManifest recognises it as a manifest.
	content := "name: test\ntemplate:\n  kind: hosted\n"
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
		t.Fatalf("write agent.manifest.yaml: %v", err)
	}

	err := checkNotDirectory(dir)
	if err == nil {
		t.Fatal("expected error for directory containing agent.manifest.yaml")
	}

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}

	if localErr.Code != exterrors.CodeInvalidManifestPointer {
		t.Errorf("expected code %q, got %q", exterrors.CodeInvalidManifestPointer, localErr.Code)
	}

	if !strings.Contains(localErr.Message, "directory") {
		t.Errorf("message should mention 'directory', got: %s", localErr.Message)
	}

	if !strings.Contains(localErr.Suggestion, "-m") {
		t.Errorf("suggestion should include '-m' flag, got: %s", localErr.Suggestion)
	}

	if !strings.Contains(localErr.Suggestion, "agent.manifest.yaml") {
		t.Errorf("suggestion should include candidate path, got: %s", localErr.Suggestion)
	}
}

func TestCheckNotDirectory_NoSuggestionForAgentDefinition(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// An AgentDefinition has "kind" at root but no "template" — should NOT
	// be suggested as a manifest file.
	defContent := "kind: hosted\nname: my-agent\n"
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(defContent), 0644); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}

	err := checkNotDirectory(dir)
	if err == nil {
		t.Fatal("expected error for directory")
	}

	// The error should NOT suggest the agent.yaml since it's a definition, not a manifest.
	errMsg := err.Error()
	if strings.Contains(errMsg, "agent.yaml") {
		t.Errorf("should not suggest AgentDefinition file, got: %s", errMsg)
	}
}

func TestCheckNotDirectory_ErrorForDirectoryWithoutManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := checkNotDirectory(dir)
	if err == nil {
		t.Fatal("expected error for empty directory")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "directory") {
		t.Errorf("error should mention 'directory', got: %s", errMsg)
	}
}

func TestManifestHasModelResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest *agent_yaml.AgentManifest
		expected bool
	}{
		{
			name: "prompt agent always has model resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-prompt",
				Template: agent_yaml.PromptAgent{},
			},
			expected: true,
		},
		{
			name: "hosted agent with model resource",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted",
				Template: agent_yaml.ContainerAgent{},
				Resources: []any{
					agent_yaml.ModelResource{
						Resource: agent_yaml.Resource{
							Name: "my-model",
							Kind: agent_yaml.ResourceKindModel,
						},
						Id: "gpt-4o",
					},
				},
			},
			expected: true,
		},
		{
			name: "hosted agent with only tool resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted-tools",
				Template: agent_yaml.ContainerAgent{},
				Resources: []any{
					agent_yaml.ToolResource{
						Resource: agent_yaml.Resource{
							Name: "my-tool",
							Kind: agent_yaml.ResourceKindTool,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "hosted agent with no resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted-empty",
				Template: agent_yaml.ContainerAgent{},
			},
			expected: false,
		},
		{
			name: "hosted agent with nil resources",
			manifest: &agent_yaml.AgentManifest{
				Name:      "test-hosted-nil",
				Template:  agent_yaml.ContainerAgent{},
				Resources: nil,
			},
			expected: false,
		},
		{
			name: "hosted agent with empty resources slice",
			manifest: &agent_yaml.AgentManifest{
				Name:      "test-hosted-empty-slice",
				Template:  agent_yaml.ContainerAgent{},
				Resources: []any{},
			},
			expected: false,
		},
		{
			name: "hosted agent with mixed model and tool resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted-mixed",
				Template: agent_yaml.ContainerAgent{},
				Resources: []any{
					agent_yaml.ToolResource{
						Resource: agent_yaml.Resource{
							Name: "my-tool",
							Kind: agent_yaml.ResourceKindTool,
						},
					},
					agent_yaml.ModelResource{
						Resource: agent_yaml.Resource{
							Name: "my-model",
							Kind: agent_yaml.ResourceKindModel,
						},
						Id: "gpt-4o",
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manifestHasModelResources(tt.manifest)
			if result != tt.expected {
				t.Errorf("manifestHasModelResources() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestResolvePositionalArg(t *testing.T) {
	t.Parallel()

	// Create a temp directory with a manifest file for testing
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "agent.yaml")
	if err := os.WriteFile(manifestPath, []byte("name: test\n"), 0600); err != nil {
		t.Fatalf("failed to create test manifest: %v", err)
	}

	tests := []struct {
		name       string
		arg        string
		isManifest bool
		isSrc      bool
	}{
		{
			name:       "https URL is manifest",
			arg:        "https://github.com/org/repo/blob/main/agent.yaml",
			isManifest: true,
		},
		{
			name:       "http URL is manifest",
			arg:        "http://example.com/agent.yaml",
			isManifest: true,
		},
		{
			name:       "azureml registry URL is manifest",
			arg:        "azureml://registries/myReg/agentmanifests/myManifest",
			isManifest: true,
		},
		{
			name:       "custom scheme URL is manifest",
			arg:        "custom://some/resource",
			isManifest: true,
		},
		{
			name:       "existing file is manifest",
			arg:        manifestPath,
			isManifest: true,
		},
		{
			name:  "existing directory is src",
			arg:   tmpDir,
			isSrc: true,
		},
		{
			name:       "non-existent yaml path is manifest",
			arg:        filepath.Join(tmpDir, "does-not-exist.yaml"),
			isManifest: true,
		},
		{
			name:       "non-existent yml path is manifest",
			arg:        filepath.Join(tmpDir, "does-not-exist.yml"),
			isManifest: true,
		},
		{
			name:  "non-existent path without extension is src",
			arg:   filepath.Join(tmpDir, "new-project-dir"),
			isSrc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			isManifest, isSrc, err := resolvePositionalArg(tt.arg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if isManifest != tt.isManifest {
				t.Errorf("isManifest = %v, want %v", isManifest, tt.isManifest)
			}
			if isSrc != tt.isSrc {
				t.Errorf("isSrc = %v, want %v", isSrc, tt.isSrc)
			}
		})
	}
}

func TestApplyPositionalArg_ConflictWithManifestFlag(t *testing.T) {
	t.Parallel()

	manifestPath := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(manifestPath, []byte("name: test\n"), 0600); err != nil {
		t.Fatalf("failed to create test manifest: %v", err)
	}

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	// Simulate the user having set --manifest explicitly
	if err := cmd.Flags().Set("manifest", "other.yaml"); err != nil {
		t.Fatalf("failed to set flag: %v", err)
	}

	err := applyPositionalArg(manifestPath, flags, cmd)
	if err == nil {
		t.Fatal("expected error for conflicting positional arg and --manifest flag")
	}

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}
	if localErr.Code != exterrors.CodeConflictingArguments {
		t.Errorf("code = %q, want %q", localErr.Code, exterrors.CodeConflictingArguments)
	}
	if !strings.Contains(localErr.Suggestion, "azd ai agent init") {
		t.Errorf("suggestion should include usage example, got: %s", localErr.Suggestion)
	}
}

func TestApplyPositionalArg_ConflictWithSrcFlag(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	// Simulate the user having set --src explicitly
	if err := cmd.Flags().Set("src", "other-dir"); err != nil {
		t.Fatalf("failed to set flag: %v", err)
	}

	err := applyPositionalArg(tmpDir, flags, cmd)
	if err == nil {
		t.Fatal("expected error for conflicting positional arg and --src flag")
	}

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}
	if localErr.Code != exterrors.CodeConflictingArguments {
		t.Errorf("code = %q, want %q", localErr.Code, exterrors.CodeConflictingArguments)
	}
	if !strings.Contains(localErr.Suggestion, "azd ai agent init") {
		t.Errorf("suggestion should include usage example, got: %s", localErr.Suggestion)
	}
}

func TestApplyPositionalArg_SetsManifestPointer(t *testing.T) {
	t.Parallel()

	manifestPath := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(manifestPath, []byte("name: test\n"), 0600); err != nil {
		t.Fatalf("failed to create test manifest: %v", err)
	}

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(manifestPath, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.manifestPointer != manifestPath {
		t.Errorf("manifestPointer = %q, want %q", flags.manifestPointer, manifestPath)
	}
}

func TestApplyPositionalArg_SetsSrcDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(tmpDir, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.src != tmpDir {
		t.Errorf("src = %q, want %q", flags.src, tmpDir)
	}
}

func TestApplyPositionalArg_NonExistentDirSetsSrc(t *testing.T) {
	t.Parallel()

	newDir := filepath.Join(t.TempDir(), "new-project")

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(newDir, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.src != newDir {
		t.Errorf("src = %q, want %q", flags.src, newDir)
	}
}

func TestApplyPositionalArg_NonExistentYamlSetsManifest(t *testing.T) {
	t.Parallel()

	yamlPath := filepath.Join(t.TempDir(), "agent.yaml")

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(yamlPath, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.manifestPointer != yamlPath {
		t.Errorf("manifestPointer = %q, want %q", flags.manifestPointer, yamlPath)
	}
}

// ---------------------------------------------------------------------------
// validateRenameInput (covers PR review - input validation for user-provided
// rename names in resolveCollisions)
// ---------------------------------------------------------------------------

func TestValidateRenameInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		wantDir    string
		wantSvc    string
		wantErr    bool
		errContain string
	}{
		{
			name:    "simple valid name",
			input:   "my-agent",
			wantDir: filepath.Join("src", "my-agent"),
			wantSvc: "my-agent",
		},
		{
			name:    "name with spaces produces valid svc",
			input:   "my agent",
			wantDir: filepath.Join("src", "my agent"),
			wantSvc: "myagent",
		},
		{
			name:       "path separator forward slash rejected",
			input:      "../escape",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "path separator backslash rejected",
			input:      `sub\dir`,
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "single dot rejected",
			input:      ".",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "double dot rejected",
			input:      "..",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "absolute path rejected",
			input:      "/etc/passwd",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "empty name fails service validation",
			input:      "",
			wantErr:    true,
			errContain: "invalid service name",
		},
		{
			name:       "invalid characters fail service validation",
			input:      "agent@name!",
			wantErr:    true,
			errContain: "invalid service name",
		},
		{
			name:    "name with dots and hyphens is valid",
			input:   "agent.v2-beta",
			wantDir: filepath.Join("src", "agent.v2-beta"),
			wantSvc: "agent.v2-beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotDir, gotSvc, err := validateRenameInput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContain != "" &&
					!strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error = %q, want containing %q",
						err.Error(), tt.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotDir != tt.wantDir {
				t.Errorf("dir = %q, want %q", gotDir, tt.wantDir)
			}
			if gotSvc != tt.wantSvc {
				t.Errorf("svc = %q, want %q", gotSvc, tt.wantSvc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildCollisionMessage (covers PR review - tailored collision messages)
// ---------------------------------------------------------------------------

func TestBuildCollisionMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dirExists      bool
		serviceExists  bool
		targetDir      string
		serviceName    string
		wantContain    string
		wantNotContain string
	}{
		{
			name:          "both collisions mentions service and directory",
			dirExists:     true,
			serviceExists: true,
			targetDir:     "src/agent",
			serviceName:   "agent",
			wantContain:   "src/agent",
		},
		{
			name:          "service-only collision mentions azure.yaml",
			dirExists:     false,
			serviceExists: true,
			targetDir:     "src/agent",
			serviceName:   "agent",
			wantContain:   "azure.yaml",
		},
		{
			name:           "dir-only collision does not mention azure.yaml",
			dirExists:      true,
			serviceExists:  false,
			targetDir:      "src/agent",
			serviceName:    "agent",
			wantContain:    "src/agent",
			wantNotContain: "azure.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := buildCollisionMessage(
				tt.dirExists, tt.serviceExists,
				tt.targetDir, tt.serviceName,
			)
			if !strings.Contains(msg, tt.wantContain) {
				t.Errorf("message = %q, want containing %q",
					msg, tt.wantContain)
			}
			if tt.wantNotContain != "" &&
				strings.Contains(msg, tt.wantNotContain) {
				t.Errorf("message = %q, should NOT contain %q",
					msg, tt.wantNotContain)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// nextAvailableName (covers PR review - collision-resolution naming logic)
// ---------------------------------------------------------------------------

func TestNextAvailableName(t *testing.T) {
	tests := []struct {
		name          string
		agentId       string
		existingDirs  []string // dirs to create under src/
		existingSvcs  []string // service names in projectConfig
		wantCandidate string
		wantDir       string
		wantSvc       string
		wantErr       bool
	}{
		{
			name:          "no collisions picks -2",
			agentId:       "my-agent",
			wantCandidate: "my-agent-2",
			wantDir:       filepath.Join("src", "my-agent-2"),
			wantSvc:       "my-agent-2",
		},
		{
			name:          "dir collision skips to -3",
			agentId:       "my-agent",
			existingDirs:  []string{"my-agent-2"},
			wantCandidate: "my-agent-3",
			wantDir:       filepath.Join("src", "my-agent-3"),
			wantSvc:       "my-agent-3",
		},
		{
			name:          "service collision skips to -3",
			agentId:       "my-agent",
			existingSvcs:  []string{"my-agent-2"},
			wantCandidate: "my-agent-3",
			wantDir:       filepath.Join("src", "my-agent-3"),
			wantSvc:       "my-agent-3",
		},
		{
			name:          "both dir and svc collisions skip",
			agentId:       "my-agent",
			existingDirs:  []string{"my-agent-2"},
			existingSvcs:  []string{"my-agent-3"},
			wantCandidate: "my-agent-4",
			wantDir:       filepath.Join("src", "my-agent-4"),
			wantSvc:       "my-agent-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)

			for _, d := range tt.existingDirs {
				dirPath := filepath.Join("src", d)
				//nolint:gosec // test fixture directory permissions are intentional
				if err := os.MkdirAll(dirPath, 0o755); err != nil {
					t.Fatalf("setup: MkdirAll(%q): %v", dirPath, err)
				}
			}

			var projectCfg *azdext.ProjectConfig
			if len(tt.existingSvcs) > 0 {
				svcs := make(map[string]*azdext.ServiceConfig, len(tt.existingSvcs))
				for _, svcName := range tt.existingSvcs {
					svcs[svcName] = &azdext.ServiceConfig{Name: svcName}
				}
				projectCfg = &azdext.ProjectConfig{Services: svcs}
			}

			action := &InitAction{projectConfig: projectCfg}
			candidate, dir, svc, err := action.nextAvailableName(tt.agentId)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if candidate != tt.wantCandidate {
				t.Errorf("candidate = %q, want %q",
					candidate, tt.wantCandidate)
			}
			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
			if svc != tt.wantSvc {
				t.Errorf("svc = %q, want %q", svc, tt.wantSvc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveCollisions — no collision / no-prompt paths
// (covers PR review — collision resolution unit tests)
// ---------------------------------------------------------------------------

func TestResolveCollisions_NoCollision(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	action := &InitAction{
		flags: &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}},
	}

	dir, svc, err := action.resolveCollisions(
		t.Context(), "agent",
		filepath.Join("src", "agent"), "agent",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != filepath.Join("src", "agent") {
		t.Errorf("dir = %q, want %q",
			dir, filepath.Join("src", "agent"))
	}
	if svc != "agent" {
		t.Errorf("svc = %q, want %q", svc, "agent")
	}
}

func TestResolveCollisions_NoPrompt(t *testing.T) {
	tests := []struct {
		name         string
		agentId      string
		existingDirs []string
		existingSvcs []string
		wantDir      string
		wantSvc      string
	}{
		{
			name:         "dir-only collision auto-suffixes",
			agentId:      "agent",
			existingDirs: []string{"agent"},
			wantDir:      filepath.Join("src", "agent-2"),
			wantSvc:      "agent-2",
		},
		{
			name:         "service-only collision auto-suffixes",
			agentId:      "agent",
			existingSvcs: []string{"agent"},
			wantDir:      filepath.Join("src", "agent-2"),
			wantSvc:      "agent-2",
		},
		{
			name:         "both collisions auto-suffix",
			agentId:      "agent",
			existingDirs: []string{"agent"},
			existingSvcs: []string{"agent"},
			wantDir:      filepath.Join("src", "agent-2"),
			wantSvc:      "agent-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)

			for _, d := range tt.existingDirs {
				dirPath := filepath.Join("src", d)
				//nolint:gosec // test fixture directory permissions are intentional
				if err := os.MkdirAll(dirPath, 0o755); err != nil {
					t.Fatalf("setup: MkdirAll(%q): %v", dirPath, err)
				}
			}

			var projectCfg *azdext.ProjectConfig
			svcs := make(map[string]*azdext.ServiceConfig, len(tt.existingSvcs))
			for _, svcName := range tt.existingSvcs {
				svcs[svcName] = &azdext.ServiceConfig{Name: svcName}
			}
			if len(svcs) > 0 {
				projectCfg = &azdext.ProjectConfig{Services: svcs}
			}

			action := &InitAction{
				projectConfig: projectCfg,
				flags: &initFlags{
					rootFlagsDefinition: &rootFlagsDefinition{
						NoPrompt: true,
					},
				},
			}

			targetDir := filepath.Join("src", tt.agentId)
			dir, svc, err := action.resolveCollisions(
				t.Context(), tt.agentId, targetDir, tt.agentId,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
			if svc != tt.wantSvc {
				t.Errorf("svc = %q, want %q", svc, tt.wantSvc)
			}
		})
	}
}
