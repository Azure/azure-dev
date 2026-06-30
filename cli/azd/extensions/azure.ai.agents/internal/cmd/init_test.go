// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInitCommand_AgentNameFlag(t *testing.T) {
	cmd := newInitCommand(nil)

	flag := cmd.Flags().Lookup("agent-name")
	if flag == nil {
		t.Fatal("expected --agent-name flag to be registered")
	}
	if flag.Shorthand != "" {
		t.Fatalf("expected --agent-name to have no shorthand, got %q", flag.Shorthand)
	}
}

func TestInitCommand_ForceFlag(t *testing.T) {
	cmd := newInitCommand(nil)

	flag := cmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag to be registered")
	}
	if flag.Shorthand != "" {
		t.Fatalf("expected --force to have no shorthand (matches azd `infra generate --force`), got %q", flag.Shorthand)
	}
	if flag.DefValue != "false" {
		t.Fatalf("expected --force default false, got %q", flag.DefValue)
	}
}

// TestHasFoundryProviderDeclared covers the predicate ensureProject
// uses to suppress the "missing infra/" warning.
func TestHasFoundryProviderDeclared(t *testing.T) {
	cases := []struct {
		name string
		proj *azdext.ProjectConfig
		want bool
	}{
		{name: "nil project", proj: nil, want: false},
		{name: "missing infra block", proj: &azdext.ProjectConfig{}, want: false},
		{
			name: "different provider",
			proj: &azdext.ProjectConfig{Infra: &azdext.InfraOptions{Provider: "bicep"}},
			want: false,
		},
		{
			name: "empty provider",
			proj: &azdext.ProjectConfig{Infra: &azdext.InfraOptions{}},
			want: false,
		},
		{
			name: "matches",
			proj: &azdext.ProjectConfig{Infra: &azdext.InfraOptions{Provider: project.FoundryProviderName}},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasFoundryProviderDeclared(tc.proj); got != tc.want {
				t.Fatalf("hasFoundryProviderDeclared = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInitCommand_ImageFlag(t *testing.T) {
	t.Parallel()

	cmd := newInitCommand(nil)

	flag := cmd.Flags().Lookup("image")
	require.NotNil(t, flag, "--image flag should be registered")
	require.Empty(t, flag.DefValue, "expected --image to have empty default")
}

func TestValidateImageFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		image      string
		deployMode string
		wantErr    bool
		errContain string
	}{
		{
			name:  "empty image is valid",
			image: "",
		},
		{
			name:  "valid ACR image",
			image: "myacr.azurecr.io/agent:v1",
		},
		{
			name:  "valid Docker Hub image",
			image: "docker.io/myorg/agent:latest",
		},
		{
			name:  "valid image without tag",
			image: "myacr.azurecr.io/agent",
		},
		{
			name: "valid image with digest",
			image: "myacr.azurecr.io/agent@sha256:" +
				"76a9463463acf11d4068e8468fb232a3de0709177b6b35de95de6a34b33fa686",
		},
		{
			name:  "valid local registry with port",
			image: "localhost:5000/myorg/agent:v1",
		},
		{
			name:  "valid registry host with port",
			image: "registry:5000/myorg/agent:v1",
		},
		{
			name:       "image without registry fails",
			image:      "agent:v1",
			wantErr:    true,
			errContain: "must be in format",
		},
		{
			name:       "namespace image without registry fails",
			image:      "myorg/agent:v1",
			wantErr:    true,
			errContain: "must be in format",
		},
		{
			name:       "image with URL scheme fails",
			image:      "https://myacr.azurecr.io/agent:v1",
			wantErr:    true,
			errContain: "must be in format",
		},
		{
			name:       "image missing repository fails",
			image:      "myacr.azurecr.io/",
			wantErr:    true,
			errContain: "must be in format",
		},
		{
			name:       "image with short digest fails",
			image:      "myacr.azurecr.io/agent@sha256:abc123",
			wantErr:    true,
			errContain: "must be in format",
		},
		{
			name:       "uppercase repository fails",
			image:      "myacr.azurecr.io/MyAgent:v1",
			wantErr:    true,
			errContain: "must be in format",
		},
		{
			name:       "simple name fails",
			image:      "agent",
			wantErr:    true,
			errContain: "must be in format",
		},
		{
			name:       "image with code deploy fails",
			image:      "myacr.azurecr.io/agent:v1",
			deployMode: "code",
			wantErr:    true,
			errContain: "cannot be used with --deploy-mode code",
		},
		{
			name:       "container deploy mode with image is valid",
			image:      "myacr.azurecr.io/agent:v1",
			deployMode: "container",
		},
		{
			name:       "empty deploy mode with image is valid",
			image:      "myacr.azurecr.io/agent:v1",
			deployMode: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateImageFlag(tt.image, tt.deployMode)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContain)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPreBuiltImageForInit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest *agent_yaml.AgentManifest
		flag     string
		want     string
	}{
		{name: "empty", manifest: &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{}}, want: ""},
		{name: "nil manifest", manifest: nil, want: ""},
		{name: "non-container agent", manifest: &agent_yaml.AgentManifest{Template: agent_yaml.Workflow{}}, want: ""},
		{
			name:     "manifest image",
			manifest: &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{Image: "myacr.azurecr.io/agent:v1"}},
			want:     "myacr.azurecr.io/agent:v1",
		},
		{
			name:     "flag image wins",
			manifest: &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{Image: "myacr.azurecr.io/agent:v1"}},
			flag:     "override.azurecr.io/agent:v2",
			want:     "override.azurecr.io/agent:v2",
		},
		{
			name:     "trims flag image",
			manifest: &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{}},
			flag:     "  myacr.azurecr.io/agent:v1  ",
			want:     "myacr.azurecr.io/agent:v1",
		},
		{
			name:     "trims manifest image",
			manifest: &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{Image: "  myacr.azurecr.io/agent:v1  "}},
			want:     "myacr.azurecr.io/agent:v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, preBuiltImageForInit(tt.manifest, tt.flag))
		})
	}
}

func TestSkipACR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		isCodeDeploy bool
		image        string
		want         bool
	}{
		{
			name:         "code deploy skips ACR",
			isCodeDeploy: true,
			image:        "",
			want:         true,
		},
		{
			name:         "image flag skips ACR",
			isCodeDeploy: false,
			image:        "myacr.azurecr.io/agent:v1",
			want:         true,
		},
		{
			name:         "both set skips ACR",
			isCodeDeploy: true,
			image:        "myacr.azurecr.io/agent:v1",
			want:         true,
		},
		{
			name:         "neither set does not skip ACR",
			isCodeDeploy: false,
			image:        "",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action := &InitAction{
				isCodeDeploy: tt.isCodeDeploy,
				flags:        &initFlags{image: tt.image},
			}

			require.Equal(t, tt.want, action.skipACR())
		})
	}
}

func TestSynthesizeImageManifestFile(t *testing.T) {
	t.Parallel()

	const agentName = "my-agent"
	const image = "myacr.azurecr.io/agents/my-agent@sha256:" +
		"76a9463463acf11d4068e8468fb232a3de0709177b6b35de95de6a34b33fa686"

	manifestPath, cleanup, err := synthesizeImageManifestFile(agentName, image)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	require.FileExists(t, manifestPath)
	require.Equal(t, "agent.yaml", filepath.Base(manifestPath))

	content, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	// The synthesized file must parse through the same path the manifest flow uses.
	template, err := agent_yaml.ExtractAgentDefinition(content)
	require.NoError(t, err)

	containerAgent, ok := template.(agent_yaml.ContainerAgent)
	require.True(t, ok, "synthesized template should be a ContainerAgent, got %T", template)
	require.Equal(t, agent_yaml.AgentKindHosted, containerAgent.Kind)
	require.Equal(t, agentName, containerAgent.Name)
	require.Empty(t, containerAgent.Image)
	require.Len(t, containerAgent.Protocols, 1)
	require.Equal(t, "responses", containerAgent.Protocols[0].Protocol)
	require.Equal(t, "1.0.0", containerAgent.Protocols[0].Version)

	// cleanup removes the temp directory.
	cleanup()
	require.NoFileExists(t, manifestPath)
}

func TestAddToProjectPreBuiltImageWritesServiceImage(t *testing.T) {
	const image = "myacr.azurecr.io/agents/my-agent:v1"
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	action := &InitAction{
		azdClient:           client,
		environment:         &azdext.Environment{Name: "test-env"},
		flags:               &initFlags{image: image, noPrompt: true},
		serviceNameOverride: "my-agent",
	}
	description := "Hosted container agent using a pre-built image"
	manifest := &agent_yaml.AgentManifest{
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind:        agent_yaml.AgentKindHosted,
				Name:        "my-agent",
				Description: &description,
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "1.0.0"},
			},
		},
	}

	_, err := captureStdout(t, func() error {
		return action.addToProject(t.Context(), "src/my-agent", manifest)
	})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	var agentService *azdext.ServiceConfig
	for _, service := range server.added {
		if service.GetName() == "my-agent" {
			agentService = service
			break
		}
	}
	require.NotNil(t, agentService)
	require.Equal(t, image, agentService.GetImage())
	require.Equal(t, "docker", agentService.GetLanguage())
	require.NotNil(t, agentService.GetDocker())
	require.NotNil(t, agentService.GetAdditionalProperties())

	_, hasInlineImage := agentService.GetAdditionalProperties().GetFields()["image"]
	require.False(t, hasInlineImage, "pre-built image must ride on the top-level service image field")
}

func TestValidateInitAgentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid", input: "my-agent", want: "my-agent"},
		{name: "trims whitespace", input: "  my-agent  ", want: "my-agent"},
		{name: "empty", input: "", wantErr: true},
		{name: "underscore", input: "my_agent", wantErr: true},
		{name: "trailing hyphen", input: "my-agent-", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateInitAgentName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				var localErr *azdext.LocalError
				if !errors.As(err, &localErr) {
					t.Fatalf("expected LocalError, got %T", err)
				}
				if localErr.Code != exterrors.CodeInvalidAgentName {
					t.Fatalf("code = %q, want %q", localErr.Code, exterrors.CodeInvalidAgentName)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveInitAgentName_UsesFlagWithoutPrompt(t *testing.T) {
	t.Parallel()

	name, err := resolveInitAgentName(t.Context(), nil, &initFlags{
		agentName: "flag-agent",
		noPrompt:  true,
	}, "default-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "flag-agent" {
		t.Fatalf("name = %q, want flag-agent", name)
	}
}

func TestResolveInitAgentName_NoPromptUsesDefault(t *testing.T) {
	t.Parallel()

	name, err := resolveInitAgentName(t.Context(), nil, &initFlags{noPrompt: true}, "default-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "default-agent" {
		t.Fatalf("name = %q, want default-agent", name)
	}
}

func TestResolveInitAgentName_InvalidNameRepromptsUntilValid(t *testing.T) {
	t.Parallel()

	tooLong := strings.Repeat("a", 64) // 64 chars > 63 char limit
	prompts := &testPromptServiceServer{
		promptResponses: []string{tooLong, "valid-agent"},
	}
	azdClient := newTestAzdClient(t, &testEnvironmentServiceServer{}, &testWorkflowServiceServer{}, prompts)

	name, err := resolveInitAgentName(t.Context(), azdClient, &initFlags{}, "default-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "valid-agent" {
		t.Fatalf("name = %q, want valid-agent", name)
	}
	if len(prompts.promptRequests) != 2 {
		t.Fatalf("text prompts = %d, want 2 (first invalid, second valid)", len(prompts.promptRequests))
	}
}

func TestAgentNameTemplateHelpers(t *testing.T) {
	t.Parallel()

	t.Run("container agent", func(t *testing.T) {
		manifest := &agent_yaml.AgentManifest{
			Template: agent_yaml.ContainerAgent{
				AgentDefinition: agent_yaml.AgentDefinition{
					Kind: agent_yaml.AgentKindHosted,
					Name: "original-agent",
				},
			},
		}

		name, err := agentNameFromTemplate(manifest.Template)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "original-agent" {
			t.Fatalf("name = %q, want original-agent", name)
		}

		if err := setAgentNameOnTemplate(manifest, "renamed-agent"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		renamed := manifest.Template.(agent_yaml.ContainerAgent)
		if renamed.Name != "renamed-agent" {
			t.Fatalf("name = %q, want renamed-agent", renamed.Name)
		}
	})

	t.Run("workflow", func(t *testing.T) {
		manifest := &agent_yaml.AgentManifest{
			Template: agent_yaml.Workflow{
				AgentDefinition: agent_yaml.AgentDefinition{
					Kind: agent_yaml.AgentKindWorkflow,
					Name: "workflow-agent",
				},
			},
		}

		if err := setAgentNameOnTemplate(manifest, "renamed-workflow"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		renamed := manifest.Template.(agent_yaml.Workflow)
		if renamed.Name != "renamed-workflow" {
			t.Fatalf("name = %q, want renamed-workflow", renamed.Name)
		}
	})
}

func TestNextAgentNameSuggestion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "without numeric suffix", input: "my-agent", want: "my-agent-2"},
		{name: "increments numeric suffix", input: "my-agent-2", want: "my-agent-3"},
		{name: "carries numeric suffix", input: "my-agent-9", want: "my-agent-10"},
		{name: "preserves leading zero width", input: "my-agent-009", want: "my-agent-010"},
		{name: "truncates long base", input: strings.Repeat("a", 63), want: strings.Repeat("a", 61) + "-2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextAgentNameSuggestion(tt.input); got != tt.want {
				t.Fatalf("nextAgentNameSuggestion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

type fakeConflictAgentChecker struct {
	exists map[string]bool
	err    error
	calls  []string
}

func (f *fakeConflictAgentChecker) GetAgent(
	_ context.Context,
	agentName string,
	_ string,
) (*agent_api.AgentObject, error) {
	f.calls = append(f.calls, agentName)
	if f.err != nil {
		return nil, f.err
	}
	if f.exists[agentName] {
		return &agent_api.AgentObject{}, nil
	}

	return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
}

type testPromptServiceServer struct {
	azdext.UnimplementedPromptServiceServer

	confirmResponses []bool
	promptResponses  []string
	confirmRequests  []*azdext.ConfirmRequest
	promptRequests   []*azdext.PromptRequest
}

func (s *testPromptServiceServer) Confirm(
	_ context.Context,
	req *azdext.ConfirmRequest,
) (*azdext.ConfirmResponse, error) {
	s.confirmRequests = append(s.confirmRequests, req)
	if len(s.confirmResponses) == 0 {
		return nil, status.Error(codes.Internal, "unexpected confirm prompt")
	}

	value := s.confirmResponses[0]
	s.confirmResponses = s.confirmResponses[1:]
	return &azdext.ConfirmResponse{Value: new(value)}, nil
}

func (s *testPromptServiceServer) Prompt(
	_ context.Context,
	req *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	s.promptRequests = append(s.promptRequests, req)
	if len(s.promptResponses) == 0 {
		return nil, status.Error(codes.Internal, "unexpected text prompt")
	}

	value := s.promptResponses[0]
	s.promptResponses = s.promptResponses[1:]
	return &azdext.PromptResponse{Value: value}, nil
}

func TestResolveExistingAgentNameConflictWithChecker_NoPromptKeepsExistingName(t *testing.T) {
	t.Parallel()

	checker := &fakeConflictAgentChecker{
		exists: map[string]bool{"my-agent": true},
	}

	got, err := resolveExistingAgentNameConflictWithChecker(t.Context(), nil, checker, true, "my-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-agent" {
		t.Fatalf("name = %q, want my-agent", got)
	}
	if len(checker.calls) != 1 || checker.calls[0] != "my-agent" {
		t.Fatalf("checked names = %v, want [my-agent]", checker.calls)
	}
}

func TestResolveExistingAgentNameConflictWithChecker_AgentCheckErrorDoesNotBlockInit(t *testing.T) {
	t.Parallel()

	checker := &fakeConflictAgentChecker{err: errors.New("service unavailable")}

	got, err := resolveExistingAgentNameConflictWithChecker(t.Context(), nil, checker, false, "my-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-agent" {
		t.Fatalf("name = %q, want my-agent", got)
	}
}

func TestResolveExistingAgentNameConflictWithChecker_AgentCheckCancellationReturnsError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "deadline exceeded", err: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checker := &fakeConflictAgentChecker{err: tt.err}

			got, err := resolveExistingAgentNameConflictWithChecker(t.Context(), nil, checker, false, "my-agent")
			if got != "" {
				t.Fatalf("name = %q, want empty", got)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestResolveExistingAgentNameConflictWithChecker_ConflictAccepted(t *testing.T) {
	t.Parallel()

	checker := &fakeConflictAgentChecker{
		exists: map[string]bool{"my-agent": true},
	}
	prompts := &testPromptServiceServer{confirmResponses: []bool{true}}
	azdClient := newTestAzdClient(t, &testEnvironmentServiceServer{}, &testWorkflowServiceServer{}, prompts)

	got, err := resolveExistingAgentNameConflictWithChecker(t.Context(), azdClient, checker, false, "my-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-agent" {
		t.Fatalf("name = %q, want my-agent", got)
	}
	if len(prompts.confirmRequests) != 1 {
		t.Fatalf("confirm prompts = %d, want 1", len(prompts.confirmRequests))
	}
	if len(prompts.promptRequests) != 0 {
		t.Fatalf("text prompts = %d, want 0", len(prompts.promptRequests))
	}

	help := prompts.confirmRequests[0].Options.HelpMessage
	if help != "Choose no to enter a different Foundry agent name." {
		t.Fatalf("confirm help = %q", help)
	}
}

func TestResolveExistingAgentNameConflictWithChecker_InvalidRetryThenUniqueName(t *testing.T) {
	t.Parallel()

	checker := &fakeConflictAgentChecker{
		exists: map[string]bool{"my-agent": true},
	}
	prompts := &testPromptServiceServer{
		confirmResponses: []bool{false},
		promptResponses:  []string{"my_agent", "new-agent"},
	}
	azdClient := newTestAzdClient(t, &testEnvironmentServiceServer{}, &testWorkflowServiceServer{}, prompts)

	got, err := resolveExistingAgentNameConflictWithChecker(t.Context(), azdClient, checker, false, "my-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "new-agent" {
		t.Fatalf("name = %q, want new-agent", got)
	}
	if len(prompts.confirmRequests) != 1 {
		t.Fatalf("confirm prompts = %d, want 1", len(prompts.confirmRequests))
	}
	if len(prompts.promptRequests) != 2 {
		t.Fatalf("text prompts = %d, want 2", len(prompts.promptRequests))
	}
	if got, want := strings.Join(checker.calls, ","), "my-agent,new-agent"; got != want {
		t.Fatalf("checked names = %s, want %s", got, want)
	}
}

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
	if err := a.validateLocalContainerAgentCopy(t.Context(), manifestPointer, dir); err != nil {
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
			result := parseGitHubUrlNaive(tt.url)

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

func TestExtractToolboxAndConnectionConfigs_NormalizesAgenticIdentityAuthType(t *testing.T) {
	t.Parallel()

	manifest := &agent_yaml.AgentManifest{
		Resources: []any{
			agent_yaml.ToolboxResource{
				Resource: agent_yaml.Resource{
					Name: "platform-tools",
					Kind: agent_yaml.ResourceKindToolbox,
				},
				Tools: []any{
					map[string]any{
						"type":     "mcp",
						"name":     "agentic-tool",
						"target":   "https://example.com/mcp",
						"authType": "AgenticIdentity",
					},
				},
			},
		},
	}

	_, connections, _, err := extractToolboxAndConnectionConfigs(manifest)
	if err != nil {
		t.Fatalf("extractToolboxAndConnectionConfigs failed: %v", err)
	}

	if len(connections) != 1 {
		t.Fatalf("Expected 1 connection, got %d", len(connections))
	}

	if connections[0].AuthType != string(agent_yaml.AuthTypeAgenticIdentityToken) {
		t.Errorf(
			"Expected authType %q, got %q",
			agent_yaml.AuthTypeAgenticIdentityToken,
			connections[0].AuthType,
		)
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
			name: "normalizes explicit AgenticIdentity authType",
			connResource: agent_yaml.ConnectionResource{
				Resource: agent_yaml.Resource{
					Name: "my-conn",
					Kind: agent_yaml.ResourceKindConnection,
				},
				Target:   "https://example.com",
				AuthType: agent_yaml.AuthTypeAgenticIdentity,
				Credentials: map[string]any{
					"key": "val",
				},
			},
			wantAuthType:     string(agent_yaml.AuthTypeAgenticIdentityToken),
			wantCredHasType:  false,
			wantCredKeyCount: 1,
			wantEnvVarCount:  1,
		},
		{
			name: "normalizes credentials.type AgenticIdentity when authType is empty",
			connResource: agent_yaml.ConnectionResource{
				Resource: agent_yaml.Resource{
					Name: "my-conn",
					Kind: agent_yaml.ResourceKindConnection,
				},
				Target: "https://example.com",
				Credentials: map[string]any{
					"type": "AgenticIdentity",
					"key":  "secret-value",
				},
			},
			wantAuthType:     string(agent_yaml.AuthTypeAgenticIdentityToken),
			wantCredHasType:  false,
			wantCredKeyCount: 1,
			wantEnvVarCount:  1,
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

func TestConfigureModelChoice_NoPromptMissingAzureContextDefersModelResources(t *testing.T) {
	const envName = "test-env"

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{envName: {}},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
	manifest := &agent_yaml.AgentManifest{
		Name: "test-hosted",
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Name: "test-hosted",
				Kind: agent_yaml.AgentKindHosted,
			},
		},
		Resources: []any{
			agent_yaml.ModelResource{
				Resource: agent_yaml.Resource{
					Name: "my-model",
					Kind: agent_yaml.ResourceKindModel,
				},
				Id: "gpt-4o",
			},
		},
	}
	action := &InitAction{
		azdClient:    azdClient,
		environment:  &azdext.Environment{Name: envName},
		azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{}},
		flags:        &initFlags{noPrompt: true},
	}

	var got *agent_yaml.AgentManifest
	output, err := captureStdout(t, func() error {
		var runErr error
		got, runErr = action.configureModelChoice(t.Context(), manifest)
		return runErr
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != manifest {
		t.Fatalf("configureModelChoice returned a different manifest")
	}
	if envServer.values[envName]["USE_EXISTING_AI_PROJECT"] != "false" {
		t.Fatalf("USE_EXISTING_AI_PROJECT = %q, want false", envServer.values[envName]["USE_EXISTING_AI_PROJECT"])
	}
	if got := envServer.values[envName][pendingProvisionEnvVar]; got != pendingReasonProject {
		t.Fatalf("%s = %q, want %q", pendingProvisionEnvVar, got, pendingReasonProject)
	}
	if !strings.Contains(output, "Model resource configuration was deferred") {
		t.Fatalf("output missing deferred model warning:\n%s", output)
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

	flags := &initFlags{}
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

	flags := &initFlags{}
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

	flags := &initFlags{}
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

	flags := &initFlags{}
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

	flags := &initFlags{}
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

	flags := &initFlags{}
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
		flags: &initFlags{},
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
					noPrompt: true,
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

func TestEnsureLoggedIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		output   []byte
		runErr   error
		wantErr  bool
		wantCode string
		wantMsg  string
	}{
		{
			name:    "authenticated returns nil",
			output:  []byte(`{"status":"authenticated","type":"user","email":"user@example.com"}`),
			wantErr: false,
		},
		{
			name:     "unauthenticated returns structured auth error",
			output:   []byte(`{"status":"unauthenticated"}`),
			wantErr:  true,
			wantCode: exterrors.CodeNotLoggedIn,
			wantMsg:  "not logged in",
		},
		{
			name:     "unauthenticated with non-zero exit still detected",
			output:   []byte(`{"status":"unauthenticated"}`),
			runErr:   errors.New("exit status 1"),
			wantErr:  true,
			wantCode: exterrors.CodeNotLoggedIn,
			wantMsg:  "not logged in",
		},
		{
			name:    "command failure with no output is skipped",
			output:  nil,
			runErr:  errors.New("exec: azd not found"),
			wantErr: false,
		},
		{
			name:    "malformed JSON is skipped",
			output:  []byte(`not-json`),
			wantErr: false,
		},
		{
			name:    "empty status field is skipped",
			output:  []byte(`{"status":""}`),
			wantErr: false,
		},
		{
			name:    "missing status field is skipped",
			output:  []byte(`{"email":"user@example.com"}`),
			wantErr: false,
		},
		{
			name:    "unrecognised status value is skipped",
			output:  []byte(`{"status":"unknown-value"}`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stub := func(_ context.Context) ([]byte, error) {
				return tt.output, tt.runErr
			}

			err := ensureLoggedIn(t.Context(), stub)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) {
				t.Fatalf("expected *azdext.LocalError, got %T: %v", err, err)
			}
			if localErr.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", localErr.Code, tt.wantCode)
			}
			if tt.wantMsg != "" && !strings.Contains(localErr.Message, tt.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", localErr.Message, tt.wantMsg)
			}
		})
	}
}

func TestEnsureLoggedIn_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	stub := func(_ context.Context) ([]byte, error) {
		return nil, ctx.Err()
	}

	err := ensureLoggedIn(ctx, stub)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestParseAuthStatusJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    []byte
		want    string
		wantErr bool
	}{
		{
			name: "authenticated",
			data: []byte(`{"status":"authenticated","type":"user","email":"a@b.com"}`),
			want: "authenticated",
		},
		{
			name: "unauthenticated",
			data: []byte(`{"status":"unauthenticated"}`),
			want: "unauthenticated",
		},
		{
			name:    "invalid JSON",
			data:    []byte(`not json`),
			wantErr: true,
		},
		{
			name:    "missing status",
			data:    []byte(`{"email":"a@b.com"}`),
			wantErr: true,
		},
		{
			name:    "empty status",
			data:    []byte(`{"status":""}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseAuthStatusJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDownloadAgentYaml_NoPromptManifestInSrcWithoutForce verifies that a
// headless caller pointing --manifest at a file inside `<cwd>/src/<name>` is
// refused with a structured error whose suggestion explicitly mentions
// `--force` as the pre-consent escape hatch.
//
// The InitAction is constructed with a nil azdClient because the validation
// branch returns before any prompt is invoked. The manifest is parsed (so it
// must contain a name field) but no downstream container / GitHub paths are
// reached.
func TestDownloadAgentYaml_NoPromptManifestInSrcWithoutForce(t *testing.T) {
	// Not Parallel: changes process working directory.
	tmp := t.TempDir()
	t.Chdir(tmp)

	name := "echo-agent"
	srcDir := filepath.Join(tmp, "src", name)
	//nolint:gosec // test fixture directory permissions are intentional
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	manifestPath := filepath.Join(srcDir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(manifestPath, []byte("name: "+name+"\n"), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	a := &InitAction{flags: &initFlags{noPrompt: true, force: false}}

	_, _, err := a.downloadAgentYaml(t.Context(), manifestPath, "")
	if err == nil {
		t.Fatal("expected error for no-prompt without --force")
	}

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T: %v", err, err)
	}
	if localErr.Code != exterrors.CodeInvalidManifestPointer {
		t.Errorf("expected code %q, got %q", exterrors.CodeInvalidManifestPointer, localErr.Code)
	}
	if !strings.Contains(localErr.Suggestion, "--force") {
		t.Errorf("suggestion should mention --force, got: %s", localErr.Suggestion)
	}
}

func TestCodeDeployFlagValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		flags          initFlags
		wantErr        bool
		wantErrContain string
	}{
		{
			name:    "code deploy with all required flags passes validation",
			flags:   initFlags{noPrompt: true, deployMode: "code", runtime: "python_3_13", entryPoint: "app.py"},
			wantErr: false,
		},
		{
			name:           "code deploy without runtime fails",
			flags:          initFlags{noPrompt: true, deployMode: "code", entryPoint: "app.py"},
			wantErr:        true,
			wantErrContain: "--runtime is required",
		},
		{
			name:           "code deploy without entry-point fails",
			flags:          initFlags{noPrompt: true, deployMode: "code", runtime: "python_3_13"},
			wantErr:        true,
			wantErrContain: "--entry-point is required",
		},
		{
			name:    "container deploy without runtime/entry-point passes",
			flags:   initFlags{noPrompt: true, deployMode: "container"},
			wantErr: false,
		},
		{
			name:    "code deploy without noPrompt skips validation",
			flags:   initFlags{noPrompt: false, deployMode: "code"},
			wantErr: false,
		},
		{
			name:           "invalid deploy-mode value fails",
			flags:          initFlags{noPrompt: true, deployMode: "invalid"},
			wantErr:        true,
			wantErrContain: "--deploy-mode must be",
		},
		{
			name:           "invalid runtime value fails",
			flags:          initFlags{noPrompt: true, deployMode: "code", runtime: "node_20", entryPoint: "app.js"},
			wantErr:        true,
			wantErrContain: "--runtime must be one of",
		},
		{
			name:           "invalid dep-resolution value fails",
			flags:          initFlags{noPrompt: true, deployMode: "code", runtime: "python_3_13", entryPoint: "app.py", depResolution: "invalid"},
			wantErr:        true,
			wantErrContain: "--dep-resolution must be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InitAction{flags: &tt.flags}
			err := a.validateCodeDeployFlags()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.wantErrContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// createdFolder path computation after Chdir
// (covers PR review — directory creation tracking and message accuracy)
// ---------------------------------------------------------------------------

// TestCreatedFolderPath_AfterChdir verifies that formatCreatedFolderMessage
// produces the correct relative display path even after the process has
// chdir'd into the new project directory.
func TestCreatedFolderPath_AfterChdir(t *testing.T) {
	tests := []struct {
		name     string
		folder   string
		wantPath string
	}{
		{
			name:     "simple folder name",
			folder:   "my-agent",
			wantPath: "my-agent",
		},
		{
			name:     "sanitized folder name",
			folder:   folderNameStrippingParenSuffix("Hello World (Python)"),
			wantPath: "hello-world",
		},
		{
			name:     "folder with numbers",
			folder:   "agent-v2",
			wantPath: "agent-v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalCwd := t.TempDir()

			// Create the subdirectory (simulates azd init creating it)
			folderPath := filepath.Join(originalCwd, tt.folder)
			//nolint:gosec // test fixture directory permissions are intentional
			if err := os.MkdirAll(folderPath, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}

			// Simulate the chdir that happens after azd init
			t.Chdir(folderPath)

			msg := formatCreatedFolderMessage(originalCwd, folderPath, "")
			wantSuffix := "cd " + tt.wantPath + "\n"
			if !strings.Contains(msg, tt.wantPath) {
				t.Errorf("message missing display path %q:\n%s", tt.wantPath, msg)
			}
			if !strings.HasSuffix(msg, wantSuffix) {
				t.Errorf("message should end with %q, got:\n%s", wantSuffix, msg)
			}
		})
	}
}

// TestCreatedFolderPath_NotSetWhenDirectoryExists verifies that the
// newlyCreated check correctly identifies an existing directory.
func TestCreatedFolderPath_NotSetWhenDirectoryExists(t *testing.T) {
	originalCwd := t.TempDir()
	t.Chdir(originalCwd)

	folderName := "existing-project"

	// Pre-create the directory (simulates an existing project)
	existingDir := filepath.Join(originalCwd, folderName)
	//nolint:gosec // test fixture directory permissions are intentional
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Mirror production logic: stat + errors.Is
	_, statErr := os.Stat(folderName)
	newlyCreated := errors.Is(statErr, fs.ErrNotExist)

	if newlyCreated {
		t.Error("newlyCreated should be false when directory already exists")
	}
}

// TestCreatedFolderPath_SetWhenDirectoryDoesNotExist verifies that the
// newlyCreated check correctly identifies a missing directory.
func TestCreatedFolderPath_SetWhenDirectoryDoesNotExist(t *testing.T) {
	originalCwd := t.TempDir()
	t.Chdir(originalCwd)

	folderName := "new-agent-project"

	// Do NOT create the directory — simulates fresh init
	_, statErr := os.Stat(folderName)
	newlyCreated := errors.Is(statErr, fs.ErrNotExist)

	if !newlyCreated {
		t.Error("newlyCreated should be true when directory does not exist")
	}

	// Verify formatCreatedFolderMessage produces valid output
	createdFolder := filepath.Join(originalCwd, folderName)
	msg := formatCreatedFolderMessage(originalCwd, createdFolder, "")
	if !strings.Contains(msg, folderName) {
		t.Errorf("message should contain %q:\n%s", folderName, msg)
	}
}

// TestCreatedFolderPath_AzdTemplateCase verifies the full flow for the
// TemplateTypeAzd case: folderNameFromTitle derives the name, and the message
// includes a template-title notice when the name changed.
func TestCreatedFolderPath_AzdTemplateCase(t *testing.T) {
	originalCwd := t.TempDir()

	templateTitle := "Basic Agent (Python)"
	folderName := folderNameStrippingParenSuffix(templateTitle)

	// folderNameFromTitle should strip parenthetical suffix
	if strings.Contains(folderName, "python") {
		t.Errorf("folderName should not contain parenthetical suffix, got %q", folderName)
	}

	createdFolder := filepath.Join(originalCwd, folderName)
	msg := formatCreatedFolderMessage(originalCwd, createdFolder, templateTitle)

	// Should contain the template notice since name differs from title
	if !strings.Contains(msg, templateTitle) {
		t.Errorf("message should reference original title %q:\n%s", templateTitle, msg)
	}
	// Should contain the cd hint
	if !strings.Contains(msg, "cd "+folderName) {
		t.Errorf("message should contain cd hint:\n%s", msg)
	}
}

// TestCreatedFolderPath_ManifestTemplateExistingProject verifies that no
// "created" message is produced when an existing project is found for the
// agent manifest template flow.
func TestCreatedFolderPath_ManifestTemplateExistingProject(t *testing.T) {
	originalCwd := t.TempDir()
	t.Chdir(originalCwd)

	folderName := "my-agent"

	// Pre-create directory and azure.yaml to simulate existing project
	projectDir := filepath.Join(originalCwd, folderName)
	//nolint:gosec // test fixture directory permissions are intentional
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(
		filepath.Join(projectDir, "azure.yaml"),
		[]byte("name: my-agent\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Mirror production logic: directory exists, so newlyCreated is false
	_, statErr := os.Stat(folderName)
	newlyCreated := errors.Is(statErr, fs.ErrNotExist)

	if newlyCreated {
		t.Error("newlyCreated should be false for existing project directory")
	}
}

func TestFolderNameFromTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		title string
		want  string
	}{
		{name: "strips parenthetical suffix", title: "Basic Agent (Python)", want: "basic-agent"},
		{name: "no parenthetical", title: "My Cool Agent", want: "my-cool-agent"},
		{name: "parenthetical with spaces", title: "Agent  ( Preview )", want: "agent"},
		{name: "non-ASCII title", title: "Ünö Agent (Test)", want: "n-agent"},
		{name: "all non-ASCII before paren", title: "日本語 (Python)", want: "my-agent"},
		{name: "empty title", title: "", want: "my-agent"},
		{name: "only parenthetical", title: "(Python)", want: "my-agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := folderNameStrippingParenSuffix(tt.title)
			if got != tt.want {
				t.Errorf("folderNameFromTitle(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// peekManifestName: best-effort manifest name extraction for -m folder
// creation (covers PR review — parity with template-flow folder creation)
// ---------------------------------------------------------------------------

func TestPeekManifestName_LocalFile_WithName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(path, []byte("name: my-cool-agent\ndescription: hi\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := peekManifestName(t.Context(), path, &http.Client{})
	if got != "my-cool-agent" {
		t.Errorf("peekManifestName = %q, want %q", got, "my-cool-agent")
	}
}

func TestPeekManifestName_LocalFile_WithoutName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(path, []byte("description: missing top-level name\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := peekManifestName(t.Context(), path, &http.Client{})
	if got != "" {
		t.Errorf("peekManifestName = %q, want empty", got)
	}
}

func TestPeekManifestName_LocalFile_EmptyName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(path, []byte("name: \"   \"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := peekManifestName(t.Context(), path, &http.Client{})
	if got != "" {
		t.Errorf("peekManifestName = %q, want empty (whitespace-only name)", got)
	}
}

func TestPeekManifestName_LocalFile_NonExistent(t *testing.T) {
	t.Parallel()
	got := peekManifestName(t.Context(), filepath.Join(t.TempDir(), "does-not-exist.yaml"), &http.Client{})
	if got != "" {
		t.Errorf("peekManifestName = %q, want empty for missing file", got)
	}
}

func TestPeekManifestName_LocalFile_Directory(t *testing.T) {
	t.Parallel()
	// Pointing at a directory should not be treated as a manifest — full
	// validation runs in checkNotDirectory; peek must not panic or return
	// noise.
	got := peekManifestName(t.Context(), t.TempDir(), &http.Client{})
	if got != "" {
		t.Errorf("peekManifestName = %q, want empty for directory", got)
	}
}

func TestPeekManifestName_LocalFile_MalformedYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(path, []byte("name: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := peekManifestName(t.Context(), path, &http.Client{})
	if got != "" {
		t.Errorf("peekManifestName = %q, want empty for malformed yaml", got)
	}
}

func TestPeekManifestName_LocalFile_NestedFieldsIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	manifest := "" +
		"name: nested-agent\n" +
		"description: hi\n" +
		"tools:\n" +
		"  - name: not-the-agent-name\n" +
		"  - name: also-not-it\n" +
		"environment:\n" +
		"  - name: SOME_ENV\n" +
		"    value: x\n"
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := peekManifestName(t.Context(), path, &http.Client{})
	if got != "nested-agent" {
		t.Errorf("peekManifestName = %q, want nested-agent (top-level only)", got)
	}
}

func TestPeekManifestName_EmptyPointer(t *testing.T) {
	t.Parallel()
	got := peekManifestName(t.Context(), "", &http.Client{})
	if got != "" {
		t.Errorf("peekManifestName(\"\") = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// resolveAgentNameFromManifestPointer: validates that the agent name is
// resolved (via --agent-name flag or peek+default in noPrompt) BEFORE any
// project folder is created, so the folder / agent identity / service entry /
// src subfolder / cd hint all use the same name.
// ---------------------------------------------------------------------------

func TestResolveAgentNameFromManifestPointer_FlagWinsWithoutPeek(t *testing.T) {
	t.Parallel()

	flags := &initFlags{agentName: "flag-agent", noPrompt: true}
	// Manifest pointer is intentionally unreachable to prove the flag short-circuits
	// the peek entirely.
	got, err := resolveAgentNameFromManifestPointer(
		t.Context(), nil, flags, filepath.Join(t.TempDir(), "does-not-exist.yaml"), &http.Client{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "flag-agent" {
		t.Errorf("got name = %q, want %q", got, "flag-agent")
	}
	if flags.agentName != "flag-agent" {
		t.Errorf("flags.agentName = %q, want pinned to flag value", flags.agentName)
	}
}

func TestResolveAgentNameFromManifestPointer_NoPromptUsesPeekedDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(path, []byte("name: from-manifest\ndescription: hi\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	flags := &initFlags{noPrompt: true}
	got, err := resolveAgentNameFromManifestPointer(
		t.Context(), nil, flags, path, &http.Client{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-manifest" {
		t.Errorf("got name = %q, want %q", got, "from-manifest")
	}
	if flags.agentName != "from-manifest" {
		t.Errorf("flags.agentName = %q, want pinned to peeked default", flags.agentName)
	}
}

func TestResolveAgentNameFromManifestPointer_PeekFailsReturnsEmpty(t *testing.T) {
	t.Parallel()

	// Peek will fail (nonexistent local path, no flag). Helper must return ""
	// and leave flags.agentName empty so the caller falls back to the deferred
	// inner resolution.
	flags := &initFlags{noPrompt: true}
	got, err := resolveAgentNameFromManifestPointer(
		t.Context(), nil, flags, filepath.Join(t.TempDir(), "does-not-exist.yaml"), &http.Client{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got name = %q, want empty when peek fails and no flag", got)
	}
	if flags.agentName != "" {
		t.Errorf("flags.agentName = %q, want unchanged when peek fails", flags.agentName)
	}
}

func TestResolveAgentNameFromManifestPointer_InvalidFlagReturnsError(t *testing.T) {
	t.Parallel()

	// Invalid flag value must surface a validation error rather than silently
	// falling back to peek — the user explicitly asked for this name.
	flags := &initFlags{agentName: "INVALID NAME with spaces", noPrompt: true}
	_, err := resolveAgentNameFromManifestPointer(
		t.Context(), nil, flags, filepath.Join(t.TempDir(), "ignored.yaml"), &http.Client{},
	)
	if err == nil {
		t.Fatalf("expected validation error for invalid --agent-name, got nil")
	}
}

func TestResolveAgentNameFromManifestPointer_FlagPeekConsistency(t *testing.T) {
	t.Parallel()

	// When --agent-name matches what peek would return, the resolved name and
	// the flag value should agree and the manifest never needs to be read.
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(path, []byte("name: shared-name\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	flags := &initFlags{agentName: "shared-name", noPrompt: true}
	got, err := resolveAgentNameFromManifestPointer(
		t.Context(), nil, flags, path, &http.Client{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "shared-name" {
		t.Errorf("got name = %q, want %q", got, "shared-name")
	}
}

func TestPeekManifestName_NonGitHubURL(t *testing.T) {
	t.Parallel()
	// Non-GitHub URLs are unsupported by the naive peek path and must return
	// "" (the caller then falls back to targetDir=".").
	got := peekManifestName(t.Context(), "https://example.com/some/manifest.yaml", &http.Client{})
	if got != "" {
		t.Errorf("peekManifestName = %q, want empty for non-GitHub URL", got)
	}
}

// ---------------------------------------------------------------------------
// absolutizeRelativeManifestPaths: ensures the -m manifest path survives
// ensureProject chdir into the newly created project directory. flags.src
// is intentionally NOT absolutized (see godoc on the helper for why).
// ---------------------------------------------------------------------------

func TestAbsolutizeRelativeManifestPaths_RelativeLocalManifest(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	flags := &initFlags{
		manifestPointer: "agent.yaml",
		src:             "src",
	}
	if err := absolutizeRelativeManifestPaths(flags); err != nil {
		t.Fatalf("absolutizeRelativeManifestPaths: %v", err)
	}
	if !filepath.IsAbs(flags.manifestPointer) {
		t.Errorf("manifestPointer should be absolute, got %q", flags.manifestPointer)
	}
	// Regression guard: --src is an output target (where the agent
	// definition is downloaded to, relative to the project root).
	// Absolutizing it before ensureProject chdirs into the new project
	// folder would cause InitAction.Run's filepath.Rel rewrite to produce
	// "..\src", writing the agent definition outside the new project.
	if flags.src != "src" {
		t.Errorf("src should remain relative %q, got %q", "src", flags.src)
	}
}

func TestAbsolutizeRelativeManifestPaths_AbsoluteLocalUnchanged(t *testing.T) {
	t.Parallel()
	absManifest := filepath.Join(t.TempDir(), "agent.yaml")

	flags := &initFlags{
		manifestPointer: absManifest,
		src:             "src",
	}
	if err := absolutizeRelativeManifestPaths(flags); err != nil {
		t.Fatalf("absolutizeRelativeManifestPaths: %v", err)
	}
	if flags.manifestPointer != absManifest {
		t.Errorf("absolute manifestPointer should be unchanged, got %q", flags.manifestPointer)
	}
	if flags.src != "src" {
		t.Errorf("src should be unchanged, got %q", flags.src)
	}
}

func TestAbsolutizeRelativeManifestPaths_URLPointerUnchanged(t *testing.T) {
	t.Parallel()
	const ghURL = "https://github.com/owner/repo/blob/main/agent.yaml"
	flags := &initFlags{
		manifestPointer: ghURL,
		src:             "",
	}
	if err := absolutizeRelativeManifestPaths(flags); err != nil {
		t.Fatalf("absolutizeRelativeManifestPaths: %v", err)
	}
	if flags.manifestPointer != ghURL {
		t.Errorf("URL manifestPointer should be unchanged, got %q", flags.manifestPointer)
	}
}

func TestAbsolutizeRelativeManifestPaths_EmptyFields(t *testing.T) {
	t.Parallel()
	flags := &initFlags{}
	if err := absolutizeRelativeManifestPaths(flags); err != nil {
		t.Fatalf("absolutizeRelativeManifestPaths: %v", err)
	}
	if flags.manifestPointer != "" {
		t.Errorf("empty manifestPointer should remain empty, got %q", flags.manifestPointer)
	}
	if flags.src != "" {
		t.Errorf("empty src should remain empty, got %q", flags.src)
	}
}

// TestAbsolutizeRelativeManifestPaths_SrcEscapeRegression specifically guards
// against the bug where absolutizing --src before chdir + relativization in
// InitAction.Run would produce "..\src" and write agent files outside the
// newly created project directory.
func TestAbsolutizeRelativeManifestPaths_SrcEscapeRegression(t *testing.T) {
	originalCwd := t.TempDir()
	t.Chdir(originalCwd)

	flags := &initFlags{
		manifestPointer: "agent.yaml",
		src:             "src",
	}
	if err := absolutizeRelativeManifestPaths(flags); err != nil {
		t.Fatalf("absolutizeRelativeManifestPaths: %v", err)
	}

	// Simulate ensureProject creating + chdir'ing into the project folder.
	projectDir := filepath.Join(originalCwd, "my-agent")
	//nolint:gosec // test fixture directory permissions are intentional
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Chdir(projectDir)

	// Mirror what InitAction.Run does: relativize absolute src against the
	// project root. This must NOT escape the project.
	if filepath.IsAbs(flags.src) {
		rel, err := filepath.Rel(projectDir, flags.src)
		if err != nil {
			t.Fatalf("filepath.Rel: %v", err)
		}
		if strings.HasPrefix(rel, "..") {
			t.Fatalf("src rewrite escapes project: %q (was abs %q)", rel, flags.src)
		}
	}
	// Even simpler: src should never have been touched in the first place.
	if flags.src != "src" {
		t.Errorf("src must remain relative %q to stay inside project, got %q", "src", flags.src)
	}
}

func TestGenerateResourceTokenSalt(t *testing.T) {
	salt, err := generateResourceTokenSalt()
	require.NoError(t, err)

	// Should be 8-character hex string (4 random bytes = 8 hex chars)
	require.Len(t, salt, 8)

	// Should be valid hex
	_, err = hex.DecodeString(salt)
	require.NoError(t, err)
}

func TestGenerateResourceTokenSalt_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		salt, err := generateResourceTokenSalt()
		require.NoError(t, err)
		require.False(t, seen[salt], "duplicate salt generated: %s", salt)
		seen[salt] = true
	}
}

func TestComposeSaltedResourceGroupName(t *testing.T) {
	t.Parallel()

	const salt = "deadbeef" // 8 hex chars, matches generateResourceTokenSalt
	// Build a long env name once for the truncation cases.
	longEnvName := strings.Repeat("a", 100)
	// 78 chars is the boundary: 90 - len("rg-") - 1 - len(salt) = 78
	atBoundary := strings.Repeat("a", 78)
	// Env name that, once truncated to 78, ends in a "-" we want to trim.
	trailingDashEnv := strings.Repeat("a", 77) + "-" + strings.Repeat("b", 30)
	// Env name that, once truncated to 78, ends in a "." we want to trim.
	trailingDotEnv := strings.Repeat("a", 77) + "." + strings.Repeat("b", 30)

	cases := []struct {
		name     string
		envName  string
		salt     string
		expected string
	}{
		{
			name:     "short env name",
			envName:  "myapp-dev",
			salt:     salt,
			expected: "rg-myapp-dev-" + salt,
		},
		{
			name:     "env name at boundary",
			envName:  atBoundary,
			salt:     salt,
			expected: "rg-" + atBoundary + "-" + salt,
		},
		{
			name:     "env name over boundary is truncated, salt preserved",
			envName:  longEnvName,
			salt:     salt,
			expected: "rg-" + strings.Repeat("a", 78) + "-" + salt,
		},
		{
			name:     "trailing dash after truncation is trimmed",
			envName:  trailingDashEnv,
			salt:     salt,
			expected: "rg-" + strings.Repeat("a", 77) + "-" + salt,
		},
		{
			name:     "trailing dot after truncation is trimmed",
			envName:  trailingDotEnv,
			salt:     salt,
			expected: "rg-" + strings.Repeat("a", 77) + "-" + salt,
		},
		{
			name:     "empty salt returns name without trailing dash",
			envName:  "myapp-dev",
			salt:     "",
			expected: "rg-myapp-dev",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := composeSaltedResourceGroupName(tc.envName, tc.salt)
			require.Equal(t, tc.expected, got)
			require.LessOrEqual(t, len(got), maxResourceGroupNameLen,
				"composed name must not exceed Azure RG length limit")
			if tc.salt != "" {
				require.True(t, strings.HasSuffix(got, "-"+tc.salt),
					"salt suffix must always be appended last, got %q", got)
			}
			require.False(t, strings.HasSuffix(got, "."),
				"composed name must not end with '.'")
		})
	}
}

func TestEnsureResourceTokenSaltReturnsPersistedSalt(t *testing.T) {
	envServer := &testEnvironmentServiceServer{}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

	const envName = "myapp-dev"

	first := ensureResourceTokenSalt(t.Context(), azdClient, envName)
	require.Len(t, first, 8, "newly generated salt should be 8 hex chars")
	_, err := hex.DecodeString(first)
	require.NoError(t, err, "salt should be valid hex")
	require.Equal(t, first, envServer.values[envName][resourceTokenSaltKey],
		"salt should be persisted to the env under AZD_RESOURCE_TOKEN_SALT")

	// Idempotent: a second call returns the same persisted salt.
	second := ensureResourceTokenSalt(t.Context(), azdClient, envName)
	require.Equal(t, first, second, "second call should return the existing salt unchanged")
}

func TestEnsureResourceGroupNameWritesSaltedName(t *testing.T) {
	envServer := &testEnvironmentServiceServer{}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

	const envName = "myapp-dev"
	const salt = "deadbeef"

	ensureResourceGroupName(t.Context(), azdClient, envName, salt)

	got := envServer.values[envName][resourceGroupEnvKey]
	require.Equal(t, "rg-myapp-dev-"+salt, got,
		"AZURE_RESOURCE_GROUP should be written with the salt appended last")
}

func TestEnsureResourceGroupNameSkipsWhenAlreadySet(t *testing.T) {
	const envName = "myapp-dev"
	const existing = "rg-pre-existing"

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			envName: {resourceGroupEnvKey: existing},
		},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

	ensureResourceGroupName(t.Context(), azdClient, envName, "deadbeef")

	require.Equal(t, existing, envServer.values[envName][resourceGroupEnvKey],
		"existing AZURE_RESOURCE_GROUP must not be overwritten")
}

func TestEnsureResourceGroupNameSkipsWhenSaltEmpty(t *testing.T) {
	envServer := &testEnvironmentServiceServer{}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

	ensureResourceGroupName(t.Context(), azdClient, "myapp-dev", "")

	_, ok := envServer.values["myapp-dev"][resourceGroupEnvKey]
	require.False(t, ok, "no AZURE_RESOURCE_GROUP should be written when salt is empty")
}

func TestRemoveContainerFiles(t *testing.T) {
	t.Parallel()

	t.Run("removes Dockerfile and .dockerignore when present", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM python:3.13"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte("__pycache__"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('hi')"), 0600))

		removeContainerFiles(dir)

		_, err := os.Stat(filepath.Join(dir, "Dockerfile"))
		require.True(t, os.IsNotExist(err), "Dockerfile should be removed")
		_, err = os.Stat(filepath.Join(dir, ".dockerignore"))
		require.True(t, os.IsNotExist(err), ".dockerignore should be removed")
		// Other files are untouched
		_, err = os.Stat(filepath.Join(dir, "app.py"))
		require.NoError(t, err, "app.py should still exist")
	})

	t.Run("no error when files do not exist", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Should not panic or error when directory has no Dockerfile
		removeContainerFiles(dir)
	})

	t.Run("leaves other files untouched", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		files := []string{"requirements.txt", "app.py", "README.md"}
		for _, f := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("content"), 0600))
		}

		removeContainerFiles(dir)

		for _, f := range files {
			_, err := os.Stat(filepath.Join(dir, f))
			require.NoError(t, err, "%s should still exist", f)
		}
	})
}
