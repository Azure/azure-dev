// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSanitizeAgentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple lowercase name",
			input:    "my-agent",
			expected: "my-agent",
		},
		{
			name:     "uppercase converted to lowercase",
			input:    "My-Agent",
			expected: "my-agent",
		},
		{
			name:     "spaces replaced with hyphens",
			input:    "my agent name",
			expected: "my-agent-name",
		},
		{
			name:     "special characters replaced with hyphens",
			input:    "my_agent@name!",
			expected: "my-agent-name",
		},
		{
			name:     "consecutive hyphens collapsed",
			input:    "my---agent",
			expected: "my-agent",
		},
		{
			name:     "leading and trailing hyphens stripped",
			input:    "-my-agent-",
			expected: "my-agent",
		},
		{
			name:     "mixed special chars become single hyphen",
			input:    "My Agent!!Name",
			expected: "my-agent-name",
		},
		{
			name:     "empty string returns default",
			input:    "",
			expected: "my-agent",
		},
		{
			name:     "all special characters returns default",
			input:    "!!!@@@",
			expected: "my-agent",
		},
		{
			name:     "numeric name preserved",
			input:    "agent123",
			expected: "agent123",
		},
		{
			name:     "truncate to 63 chars",
			input:    "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz",
			expected: "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefghi",
		},
		{
			name:     "truncate strips trailing hyphen",
			input:    "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefgh-extra-long-stuff",
			expected: "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefgh",
		},
		{
			name:     "dots replaced with hyphens",
			input:    "my.agent.name",
			expected: "my-agent-name",
		},
		{
			name:     "underscores replaced with hyphens",
			input:    "my_agent_name",
			expected: "my-agent-name",
		},
		{
			name:     "only hyphens returns default",
			input:    "---",
			expected: "my-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeAgentName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeAgentName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizeForFuzzyMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase passthrough",
			input:    "gpt4o",
			expected: "gpt4o",
		},
		{
			name:     "uppercase converted to lowercase",
			input:    "GPT4O",
			expected: "gpt4o",
		},
		{
			name:     "hyphens removed",
			input:    "gpt-4o",
			expected: "gpt4o",
		},
		{
			name:     "dots removed",
			input:    "gpt.4o",
			expected: "gpt4o",
		},
		{
			name:     "underscores removed",
			input:    "gpt_4o",
			expected: "gpt4o",
		},
		{
			name:     "spaces removed",
			input:    "gpt 4o",
			expected: "gpt4o",
		},
		{
			name:     "multiple separators removed",
			input:    "gpt-4.o_mini",
			expected: "gpt4omini",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only separators",
			input:    "---...__",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeForFuzzyMatch(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeForFuzzyMatch(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFuzzyFilterModels(t *testing.T) {
	t.Parallel()

	models := []string{
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4",
		"gpt-3.5-turbo",
		"claude-3-opus",
		"llama-3-70b",
		"text-embedding-ada-002",
	}

	tests := []struct {
		name       string
		searchTerm string
		expected   []string
	}{
		{
			name:       "empty search returns all",
			searchTerm: "",
			expected:   models,
		},
		{
			name:       "exact match",
			searchTerm: "gpt-4o",
			expected:   []string{"gpt-4o", "gpt-4o-mini"},
		},
		{
			name:       "case insensitive search",
			searchTerm: "GPT-4O",
			expected:   []string{"gpt-4o", "gpt-4o-mini"},
		},
		{
			name:       "fuzzy without separators",
			searchTerm: "gpt4o",
			expected:   []string{"gpt-4o", "gpt-4o-mini"},
		},
		{
			name:       "partial match",
			searchTerm: "llama",
			expected:   []string{"llama-3-70b"},
		},
		{
			name:       "no match",
			searchTerm: "nonexistent",
			expected:   nil,
		},
		{
			name:       "match across separator boundaries",
			searchTerm: "embedding",
			expected:   []string{"text-embedding-ada-002"},
		},
		{
			name:       "match with dot separator in search",
			searchTerm: "3.5",
			expected:   []string{"gpt-3.5-turbo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fuzzyFilterModels(models, tt.searchTerm)
			if !stringSlicesEqual(result, tt.expected) {
				t.Errorf("fuzzyFilterModels(%v, %q) = %v, want %v", models, tt.searchTerm, result, tt.expected)
			}
		})
	}
}

func TestFindDefaultModelIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modelNames []string
		expected   int32
	}{
		{
			name:       "gpt-4o present",
			modelNames: []string{"claude-3", "gpt-4o", "llama-3"},
			expected:   1,
		},
		{
			name:       "gpt-4o first",
			modelNames: []string{"gpt-4o", "gpt-4o-mini", "gpt-4"},
			expected:   0,
		},
		{
			name:       "no gpt-4o but gpt-4 prefix present",
			modelNames: []string{"claude-3", "gpt-4", "llama-3"},
			expected:   1,
		},
		{
			name:       "no gpt-4o but gpt-4-turbo present",
			modelNames: []string{"claude-3", "gpt-4-turbo", "llama-3"},
			expected:   1,
		},
		{
			name:       "no gpt models returns 0",
			modelNames: []string{"claude-3", "llama-3", "phi-3"},
			expected:   0,
		},
		{
			name:       "empty list returns 0",
			modelNames: []string{},
			expected:   0,
		},
		{
			name:       "gpt-4o preferred over gpt-4",
			modelNames: []string{"gpt-4", "gpt-4o", "gpt-4-turbo"},
			expected:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findDefaultModelIndex(tt.modelNames)
			if result != tt.expected {
				t.Errorf("findDefaultModelIndex(%v) = %d, want %d", tt.modelNames, result, tt.expected)
			}
		})
	}
}

func TestExtractSubscriptionId(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		resourceId string
		expected   string
	}{
		{
			name:       "standard resource id",
			resourceId: "/subscriptions/12345-abcde/resourceGroups/myRg/providers/Microsoft.CognitiveServices/accounts/myAccount",
			expected:   "12345-abcde",
		},
		{
			name:       "project resource id",
			resourceId: "/subscriptions/sub-id-123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
			expected:   "sub-id-123",
		},
		{
			name:       "case insensitive subscriptions",
			resourceId: "/Subscriptions/CASE-ID/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct",
			expected:   "CASE-ID",
		},
		{
			name:       "empty string",
			resourceId: "",
			expected:   "",
		},
		{
			name:       "no subscriptions segment",
			resourceId: "/resourceGroups/myRg/providers/Microsoft.CognitiveServices/accounts/myAccount",
			expected:   "",
		},
		{
			name:       "subscriptions at end with no value",
			resourceId: "/subscriptions",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSubscriptionId(tt.resourceId)
			if result != tt.expected {
				t.Errorf("extractSubscriptionId(%q) = %q, want %q", tt.resourceId, result, tt.expected)
			}
		})
	}
}

func TestExtractResourceGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		resourceId string
		expected   string
	}{
		{
			name:       "standard resource id",
			resourceId: "/subscriptions/sub-123/resourceGroups/myResourceGroup/providers/Microsoft.CognitiveServices/accounts/myAccount",
			expected:   "myResourceGroup",
		},
		{
			name:       "case insensitive resourceGroups",
			resourceId: "/subscriptions/sub-123/ResourceGroups/MyRG/providers/Microsoft.CognitiveServices/accounts/acct",
			expected:   "MyRG",
		},
		{
			name:       "empty string",
			resourceId: "",
			expected:   "",
		},
		{
			name:       "no resourceGroups segment",
			resourceId: "/subscriptions/sub-123/providers/Microsoft.CognitiveServices/accounts/acct",
			expected:   "",
		},
		{
			name:       "resourceGroups at end with no value",
			resourceId: "/subscriptions/sub-123/resourceGroups",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractResourceGroup(tt.resourceId)
			if result != tt.expected {
				t.Errorf("extractResourceGroup(%q) = %q, want %q", tt.resourceId, result, tt.expected)
			}
		})
	}
}

func TestWriteDefinitionToSrcDir(t *testing.T) {
	t.Parallel()

	t.Run("writes agent.yaml to directory", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		srcDir := filepath.Join(dir, "src")

		definition := &agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Name: "test-agent",
				Kind: agent_yaml.AgentKindHosted,
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
			},
			EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
				{Name: "AZURE_AI_MODEL_DEPLOYMENT_NAME", Value: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}"},
			},
		}

		action := &InitFromCodeAction{}
		resultPath, err := action.writeDefinitionToSrcDir(definition, srcDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedPath := filepath.Join(srcDir, "agent.yaml")
		if resultPath != expectedPath {
			t.Errorf("path = %q, want %q", resultPath, expectedPath)
		}

		//nolint:gosec // test fixture path is created within test temp directory
		content, err := os.ReadFile(resultPath)
		if err != nil {
			t.Fatalf("failed to read written file: %v", err)
		}

		contentStr := string(content)
		// Verify key content is present in the YAML
		if !containsAll(contentStr, "name: test-agent", "kind: hosted", "responses", "AZURE_AI_MODEL_DEPLOYMENT_NAME") {
			t.Errorf("written content missing expected fields:\n%s", contentStr)
		}
		// AZURE_OPENAI_ENDPOINT and AZURE_AI_PROJECT_ENDPOINT should NOT be written to agent.yaml.
		// Hosted agents receive platform-provided FOUNDRY_* variables such as FOUNDRY_PROJECT_ENDPOINT instead.
		if strings.Contains(contentStr, "AZURE_OPENAI_ENDPOINT") || strings.Contains(contentStr, "AZURE_AI_PROJECT_ENDPOINT") {
			t.Errorf("agent.yaml should not contain AZURE_OPENAI_ENDPOINT or AZURE_AI_PROJECT_ENDPOINT:\n%s", contentStr)
		}
	})

	t.Run("creates nested directories", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		srcDir := filepath.Join(dir, "deep", "nested", "path")

		definition := &agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Name: "nested-agent",
				Kind: agent_yaml.AgentKindHosted,
			},
		}

		action := &InitFromCodeAction{}
		_, err := action.writeDefinitionToSrcDir(definition, srcDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, err := os.Stat(filepath.Join(srcDir, "agent.yaml")); err != nil {
			t.Fatalf("expected file to exist: %v", err)
		}
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		existingFile := filepath.Join(dir, "agent.yaml")
		//nolint:gosec // test fixture file permissions are intentional
		if err := os.WriteFile(existingFile, []byte("old content"), 0644); err != nil {
			t.Fatalf("write existing file: %v", err)
		}

		definition := &agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Name: "new-agent",
				Kind: agent_yaml.AgentKindHosted,
			},
		}

		action := &InitFromCodeAction{}
		_, err := action.writeDefinitionToSrcDir(definition, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		//nolint:gosec // test fixture path is created within test temp directory
		content, err := os.ReadFile(existingFile)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		if string(content) == "old content" {
			t.Error("expected file to be overwritten, but old content remains")
		}
		if !containsAll(string(content), "name: new-agent") {
			t.Errorf("written content missing expected fields:\n%s", string(content))
		}
	})
}

func TestFoundryDeploymentInfo(t *testing.T) {
	t.Parallel()

	t.Run("zero value", func(t *testing.T) {
		info := FoundryDeploymentInfo{}
		if info.Name != "" || info.ModelName != "" || info.SkuCapacity != 0 {
			t.Error("expected zero values for uninitialized struct")
		}
	})

	t.Run("populated values", func(t *testing.T) {
		info := FoundryDeploymentInfo{
			Name:        "my-deployment",
			ModelName:   "gpt-4o",
			ModelFormat: "OpenAI",
			Version:     "2024-05-13",
			SkuName:     "GlobalStandard",
			SkuCapacity: 10,
		}
		if info.Name != "my-deployment" {
			t.Errorf("Name = %q, want %q", info.Name, "my-deployment")
		}
		if info.ModelName != "gpt-4o" {
			t.Errorf("ModelName = %q, want %q", info.ModelName, "gpt-4o")
		}
		if info.SkuCapacity != 10 {
			t.Errorf("SkuCapacity = %d, want %d", info.SkuCapacity, 10)
		}
	})
}

func TestFoundryProjectInfo(t *testing.T) {
	t.Parallel()

	info := FoundryProjectInfo{
		SubscriptionId:    "sub-123",
		ResourceGroupName: "my-rg",
		AccountName:       "my-account",
		ProjectName:       "my-project",
		Location:          "eastus",
		ResourceId:        "/subscriptions/sub-123/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
	}

	if info.SubscriptionId != "sub-123" {
		t.Errorf("SubscriptionId = %q, want %q", info.SubscriptionId, "sub-123")
	}
	if info.ResourceGroupName != "my-rg" {
		t.Errorf("ResourceGroupName = %q, want %q", info.ResourceGroupName, "my-rg")
	}
	if info.Location != "eastus" {
		t.Errorf("Location = %q, want %q", info.Location, "eastus")
	}
}

// stringSlicesEqual compares two string slices for equality.
func stringSlicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// containsAll checks that s contains all the given substrings.
func containsAll(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestPromptProtocols_FlagValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		flagProtocols  []string
		wantProtocols  []agent_yaml.ProtocolVersionRecord
		wantErr        bool
		wantErrContain string
	}{
		{
			name:          "responses only",
			flagProtocols: []string{"responses"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
			},
		},
		{
			name:          "invocations only",
			flagProtocols: []string{"invocations"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "invocations", Version: "v0.0.1"},
			},
		},
		{
			name:          "both protocols",
			flagProtocols: []string{"responses", "invocations"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
				{Protocol: "invocations", Version: "v0.0.1"},
			},
		},
		{
			name:           "unknown protocol",
			flagProtocols:  []string{"unknown_proto"},
			wantErr:        true,
			wantErrContain: "unknown protocol",
		},
		{
			name:          "duplicates are removed",
			flagProtocols: []string{"responses", "responses", "invocations"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
				{Protocol: "invocations", Version: "v0.0.1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := promptProtocols(t.Context(), nil, false, tt.flagProtocols)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.wantErrContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.wantProtocols) {
				t.Fatalf("got %d protocols, want %d", len(got), len(tt.wantProtocols))
			}
			for i := range got {
				if got[i].Protocol != tt.wantProtocols[i].Protocol {
					t.Errorf("protocol[%d] = %q, want %q", i, got[i].Protocol, tt.wantProtocols[i].Protocol)
				}
				if got[i].Version != tt.wantProtocols[i].Version {
					t.Errorf("version[%d] = %q, want %q", i, got[i].Version, tt.wantProtocols[i].Version)
				}
			}
		})
	}
}

func TestPromptProtocols_NoPromptDefault(t *testing.T) {
	t.Parallel()

	got, err := promptProtocols(t.Context(), nil, true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d protocols, want 1", len(got))
	}
	if got[0].Protocol != "responses" {
		t.Errorf("protocol = %q, want %q", got[0].Protocol, "responses")
	}
	if got[0].Version != "v1" {
		t.Errorf("version = %q, want %q", got[0].Version, "v1")
	}
}

func TestKnownProtocolNames(t *testing.T) {
	t.Parallel()

	result := knownProtocolNames()
	if !strings.Contains(result, "responses") {
		t.Errorf("knownProtocolNames() = %q, want to contain 'responses'", result)
	}
	if !strings.Contains(result, "invocations") {
		t.Errorf("knownProtocolNames() = %q, want to contain 'invocations'", result)
	}
}

// fakePromptClient is a lightweight test double for azdext.PromptServiceClient.
type fakePromptClient struct {
	azdext.PromptServiceClient
	multiSelectFn func(
		ctx context.Context,
		in *azdext.MultiSelectRequest,
		opts ...grpc.CallOption,
	) (*azdext.MultiSelectResponse, error)
}

func (f *fakePromptClient) MultiSelect(
	ctx context.Context,
	in *azdext.MultiSelectRequest,
	opts ...grpc.CallOption,
) (*azdext.MultiSelectResponse, error) {
	return f.multiSelectFn(ctx, in, opts...)
}

func TestPromptProtocols_Interactive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		multiSelectFn  func(context.Context, *azdext.MultiSelectRequest, ...grpc.CallOption) (*azdext.MultiSelectResponse, error)
		wantProtocols  []agent_yaml.ProtocolVersionRecord
		wantErr        bool
		wantErrContain string
	}{
		{
			name: "both protocols selected",
			multiSelectFn: func(_ context.Context, _ *azdext.MultiSelectRequest, _ ...grpc.CallOption) (*azdext.MultiSelectResponse, error) {
				return &azdext.MultiSelectResponse{
					Values: []*azdext.MultiSelectChoice{
						{Value: "responses", Label: "responses", Selected: true},
						{Value: "invocations", Label: "invocations", Selected: true},
					},
				}, nil
			},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
				{Protocol: "invocations", Version: "v0.0.1"},
			},
		},
		{
			name: "single protocol selected",
			multiSelectFn: func(_ context.Context, _ *azdext.MultiSelectRequest, _ ...grpc.CallOption) (*azdext.MultiSelectResponse, error) {
				return &azdext.MultiSelectResponse{
					Values: []*azdext.MultiSelectChoice{
						{Value: "responses", Label: "responses", Selected: true},
						{Value: "invocations", Label: "invocations", Selected: false},
					},
				}, nil
			},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
			},
		},
		{
			name: "user cancellation",
			multiSelectFn: func(_ context.Context, _ *azdext.MultiSelectRequest, _ ...grpc.CallOption) (*azdext.MultiSelectResponse, error) {
				return nil, status.Error(codes.Canceled, "cancelled by user")
			},
			wantErr:        true,
			wantErrContain: "cancelled",
		},
		{
			name: "empty selection returns validation error",
			multiSelectFn: func(_ context.Context, _ *azdext.MultiSelectRequest, _ ...grpc.CallOption) (*azdext.MultiSelectResponse, error) {
				return &azdext.MultiSelectResponse{
					Values: []*azdext.MultiSelectChoice{
						{Value: "responses", Label: "responses", Selected: false},
						{Value: "invocations", Label: "invocations", Selected: false},
					},
				}, nil
			},
			wantErr:        true,
			wantErrContain: "at least one protocol must be selected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &fakePromptClient{multiSelectFn: tt.multiSelectFn}
			got, err := promptProtocols(t.Context(), client, false, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" &&
					!strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error = %q, want containing %q",
						err.Error(), tt.wantErrContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.wantProtocols) {
				t.Fatalf("got %d protocols, want %d",
					len(got), len(tt.wantProtocols))
			}
			for i := range got {
				if got[i].Protocol != tt.wantProtocols[i].Protocol {
					t.Errorf("protocol[%d] = %q, want %q",
						i, got[i].Protocol, tt.wantProtocols[i].Protocol)
				}
				if got[i].Version != tt.wantProtocols[i].Version {
					t.Errorf("version[%d] = %q, want %q",
						i, got[i].Version, tt.wantProtocols[i].Version)
				}
			}
		})
	}
}
