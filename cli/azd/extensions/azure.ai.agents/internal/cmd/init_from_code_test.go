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
		{
			name:     "non-ASCII characters stripped",
			input:    "Ünö Ägent",
			expected: "n-gent",
		},
		{
			name:     "all non-ASCII falls back to default",
			input:    "日本語エージェント",
			expected: "my-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeAgentName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeAgentName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
			if err := agent_yaml.ValidateAgentName(result); err != nil {
				t.Errorf("sanitizeAgentName(%q) produced invalid agent name %q: %v", tt.input, result, err)
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

func TestWriteAgentIgnoreToSrcDir(t *testing.T) {
	t.Parallel()

	t.Run("writes .agentignore and not agent.yaml", func(t *testing.T) {
		t.Parallel()

		srcDir := filepath.Join(t.TempDir(), "src")

		action := &InitFromCodeAction{}
		if err := action.writeAgentIgnoreToSrcDir(srcDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, err := os.Stat(filepath.Join(srcDir, ".agentignore")); err != nil {
			t.Fatalf("expected .agentignore to exist: %v", err)
		}
		// The agent definition now lives in azure.yaml; no agent.yaml on disk.
		if _, err := os.Stat(filepath.Join(srcDir, "agent.yaml")); !os.IsNotExist(err) {
			t.Fatalf("agent.yaml should not be written; stat err = %v", err)
		}
	})

	t.Run("creates nested directories", func(t *testing.T) {
		t.Parallel()

		srcDir := filepath.Join(t.TempDir(), "deep", "nested", "path")
		action := &InitFromCodeAction{}
		if err := action.writeAgentIgnoreToSrcDir(srcDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(srcDir, ".agentignore")); err != nil {
			t.Fatalf("expected .agentignore to exist: %v", err)
		}
	})
}

func TestCreateDefinitionFromLocalAgent_NoPromptMissingAzureContextDefers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hello')\n"), 0600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	const envName = "agent-dev"
	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{envName: {}},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
	action := &InitFromCodeAction{
		azdClient:    azdClient,
		environment:  &azdext.Environment{Name: envName},
		azureContext: nil,
		flags: &initFlags{
			noPrompt: true,
			env:      envName,
			model:    "gpt-4o",
		},
	}

	var definition *agent_yaml.ContainerAgent
	output, err := captureStdout(t, func() error {
		var runErr error
		definition, runErr = action.createDefinitionFromLocalAgent(t.Context())
		return runErr
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if definition == nil {
		t.Fatal("expected definition")
	}
	if envServer.values[envName]["USE_EXISTING_AI_PROJECT"] != "false" {
		t.Fatalf("USE_EXISTING_AI_PROJECT = %q, want false", envServer.values[envName]["USE_EXISTING_AI_PROJECT"])
	}
	if got := envServer.values[envName][pendingProvisionEnvVar]; got != pendingReasonProject {
		t.Fatalf("%s = %q, want %q", pendingProvisionEnvVar, got, pendingReasonProject)
	}
	if len(action.deploymentDetails) != 0 {
		t.Fatalf("deploymentDetails length = %d, want 0", len(action.deploymentDetails))
	}
	if !strings.Contains(output, "Model configuration was deferred") {
		t.Fatalf("output missing deferred model warning:\n%s", output)
	}
	if definition.EnvironmentVariables != nil {
		for _, envVar := range *definition.EnvironmentVariables {
			if envVar.Name == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
				t.Fatalf("deferred model configuration should not add %s", envVar.Name)
			}
		}
	}
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
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		{
			name:          "invocations only",
			flagProtocols: []string{"invocations"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "invocations", Version: "1.0.0"},
			},
		},
		{
			name:          "both protocols",
			flagProtocols: []string{"responses", "invocations"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
				{Protocol: "invocations", Version: "1.0.0"},
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
				{Protocol: "responses", Version: "2.0.0"},
				{Protocol: "invocations", Version: "1.0.0"},
			},
		},
		{
			name:          "activity only",
			flagProtocols: []string{"activity"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "activity", Version: "1.0.0"},
			},
		},
		{
			name:          "activity coexists with other protocols",
			flagProtocols: []string{"activity", "responses"},
			wantProtocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "activity", Version: "1.0.0"},
				{Protocol: "responses", Version: "2.0.0"},
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
	if got[0].Version != "2.0.0" {
		t.Errorf("version = %q, want %q", got[0].Version, "2.0.0")
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
	if !strings.Contains(result, "activity") {
		t.Errorf("knownProtocolNames() = %q, want to contain 'activity'", result)
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
				{Protocol: "responses", Version: "2.0.0"},
				{Protocol: "invocations", Version: "1.0.0"},
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
				{Protocol: "responses", Version: "2.0.0"},
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

func TestPromptDeployMode_FlagOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		noPrompt             bool
		showCodeDeploy       bool
		flag                 string
		userProvidedManifest bool
		want                 string
		wantErr              bool
		wantErrContain       string
	}{
		{
			name:           "flag=container returns container",
			noPrompt:       true,
			showCodeDeploy: true,
			flag:           "container",
			want:           "container",
		},
		{
			name:           "flag=code returns code",
			noPrompt:       true,
			showCodeDeploy: true,
			flag:           "code",
			want:           "code",
		},
		{
			name:           "flag=code works even when showCodeDeploy=false",
			noPrompt:       true,
			showCodeDeploy: false,
			flag:           "code",
			want:           "code",
		},
		{
			name:           "invalid flag value returns error",
			noPrompt:       true,
			showCodeDeploy: true,
			flag:           "invalid",
			wantErr:        true,
			wantErrContain: "invalid --deploy-mode value",
		},
		{
			name:           "no flag + noPrompt defaults to code",
			noPrompt:       true,
			showCodeDeploy: true,
			flag:           "",
			want:           "code",
		},
		{
			name:           "no flag + showCodeDeploy=false defaults to container",
			noPrompt:       false,
			showCodeDeploy: false,
			flag:           "",
			want:           "container",
		},
		{
			name:                 "userProvidedManifest + showCodeDeploy auto-selects code",
			noPrompt:             false,
			showCodeDeploy:       true,
			flag:                 "",
			userProvidedManifest: true,
			want:                 "code",
		},
		{
			name:                 "showCodeDeploy=false returns container regardless of userProvidedManifest",
			noPrompt:             false,
			showCodeDeploy:       false,
			flag:                 "",
			userProvidedManifest: true,
			want:                 "container",
		},
		{
			name:                 "explicit flag overrides userProvidedManifest",
			noPrompt:             false,
			showCodeDeploy:       true,
			flag:                 "container",
			userProvidedManifest: true,
			want:                 "container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := promptDeployMode(t.Context(), nil, tt.noPrompt, tt.showCodeDeploy, tt.flag, tt.userProvidedManifest)
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
			if got != tt.want {
				t.Errorf("promptDeployMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPromptCodeConfig_FlagOverrides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		files                []string // files to create in temp dir
		noPrompt             bool
		userProvidedManifest bool
		opts                 codeDeployOptions
		wantRuntime          string
		wantEntry            string
		wantDepRes           string
	}{
		{
			name:        "all opts provided",
			noPrompt:    true,
			opts:        codeDeployOptions{runtime: "python_3_14", entryPoint: "bot.py", depResolution: "bundled"},
			wantRuntime: "python_3_14",
			wantEntry:   "bot.py",
			wantDepRes:  "bundled",
		},
		{
			name:        "noPrompt defaults for python project",
			files:       []string{"requirements.txt", "app.py"},
			noPrompt:    true,
			opts:        codeDeployOptions{},
			wantRuntime: "python_3_13",
			wantEntry:   "app.py",
			wantDepRes:  "remote_build",
		},
		{
			name:        "noPrompt defaults for dotnet project",
			files:       []string{"MyBot.csproj", "Program.cs"},
			noPrompt:    true,
			opts:        codeDeployOptions{},
			wantRuntime: "dotnet_10",
			wantEntry:   "MyBot.dll",
			wantDepRes:  "remote_build",
		},
		{
			name:        "opts override noPrompt defaults",
			files:       []string{"requirements.txt", "app.py"},
			noPrompt:    true,
			opts:        codeDeployOptions{runtime: "python_3_14", entryPoint: "serve.py", depResolution: "bundled"},
			wantRuntime: "python_3_14",
			wantEntry:   "serve.py",
			wantDepRes:  "bundled",
		},
		{
			name:        "partial opts — runtime from flag, rest from defaults",
			files:       []string{"app.py"},
			noPrompt:    true,
			opts:        codeDeployOptions{runtime: "python_3_14"},
			wantRuntime: "python_3_14",
			wantEntry:   "app.py",
			wantDepRes:  "remote_build",
		},
		{
			name:                 "userProvidedManifest auto-detects python defaults",
			files:                []string{"requirements.txt", "app.py"},
			noPrompt:             false,
			userProvidedManifest: true,
			opts:                 codeDeployOptions{},
			wantRuntime:          "python_3_13",
			wantEntry:            "app.py",
			wantDepRes:           "remote_build",
		},
		{
			name:                 "userProvidedManifest auto-detects dotnet defaults",
			files:                []string{"MyAgent.csproj"},
			noPrompt:             false,
			userProvidedManifest: true,
			opts:                 codeDeployOptions{},
			wantRuntime:          "dotnet_10",
			wantEntry:            "MyAgent.dll",
			wantDepRes:           "remote_build",
		},
		{
			name:                 "opts override userProvidedManifest defaults",
			files:                []string{"requirements.txt", "app.py"},
			noPrompt:             false,
			userProvidedManifest: true,
			opts:                 codeDeployOptions{runtime: "python_3_14", entryPoint: "bot.py", depResolution: "bundled"},
			wantRuntime:          "python_3_14",
			wantEntry:            "bot.py",
			wantDepRes:           "bundled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := promptCodeConfig(t.Context(), nil, dir, tt.noPrompt, tt.opts, tt.userProvidedManifest)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Runtime != tt.wantRuntime {
				t.Errorf("Runtime = %q, want %q", got.Runtime, tt.wantRuntime)
			}
			if got.EntryPoint != tt.wantEntry {
				t.Errorf("EntryPoint = %q, want %q", got.EntryPoint, tt.wantEntry)
			}
			if got.DependencyResolution == nil {
				t.Fatal("DependencyResolution is nil")
			}
			if *got.DependencyResolution != tt.wantDepRes {
				t.Errorf("DependencyResolution = %q, want %q", *got.DependencyResolution, tt.wantDepRes)
			}
		})
	}
}

func TestDetectDefaultEntryPoint(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		runtime string
		want    string
	}{
		{
			name:    "dotnet with csproj",
			files:   []string{"MyAgent.csproj", "Program.cs"},
			runtime: "dotnet_9",
			want:    "MyAgent.dll",
		},
		{
			name:    "dotnet_8 with csproj",
			files:   []string{"EchoAgent.csproj", "Program.cs", "NuGet.config"},
			runtime: "dotnet_8",
			want:    "EchoAgent.dll",
		},
		{
			name:    "dotnet_10 no csproj fallback",
			files:   []string{"Program.cs"},
			runtime: "dotnet_10",
			want:    "App.dll",
		},
		{
			name:    "python with app.py",
			files:   []string{"app.py", "requirements.txt"},
			runtime: "python_3_12",
			want:    "app.py",
		},
		{
			name:    "python without app.py",
			files:   []string{"requirements.txt"},
			runtime: "python_3_12",
			want:    "main.py",
		},
		{
			name:    "python with main.py",
			files:   []string{"main.py", "requirements.txt"},
			runtime: "python_3_11",
			want:    "main.py",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got := detectDefaultEntryPoint(dir, tt.runtime)
			if got != tt.want {
				t.Errorf("detectDefaultEntryPoint() = %q, want %q", got, tt.want)
			}
		})
	}
}
