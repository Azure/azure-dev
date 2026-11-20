// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1 "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type addPromptServer struct {
	v1.UnimplementedPromptServiceServer
	acceptExisting     bool
	acceptProvision    bool
	confirmations      []*v1.ConfirmRequest
	layerPrompts       int
	layerName          string
	followUp           string
	commands           [][]string
	hostProject        string
	serviceError       error
	layerError         error
	readError          error
	existingResourceID string
	environmentError   error
	workflowError      error
	confirmError       error
	commandsAtConfirm  int
}

func (s *addPromptServer) Prompt(_ context.Context, req *v1.PromptRequest) (*v1.PromptResponse, error) {
	value := req.Options.DefaultValue
	if s.hostProject != "" && strings.Contains(req.Options.Message, "app code project") {
		value = s.hostProject
	}
	if strings.Contains(req.Options.Message, "layer") {
		s.layerPrompts++
		if s.layerName != "" {
			value = s.layerName
		}
	}
	if value == "" {
		value = "cache"
	}
	return &v1.PromptResponse{Value: value}, nil
}

func (s *addPromptServer) Confirm(_ context.Context, req *v1.ConfirmRequest) (*v1.ConfirmResponse, error) {
	s.confirmations = append(s.confirmations, req)
	var value bool
	switch {
	case strings.Contains(req.Options.Message, "existing layer"), strings.HasPrefix(req.Options.Message, "Replace"):
		value = s.acceptExisting
	case strings.HasPrefix(req.Options.Message, "Proceed to "):
		s.commandsAtConfirm = len(s.commands)
		if s.confirmError != nil {
			return nil, s.confirmError
		}
		value = s.acceptProvision
	default:
		return nil, errors.New("unexpected confirmation: " + req.Options.Message)
	}
	return &v1.ConfirmResponse{Value: new(value)}, nil
}

func (s *addPromptServer) Select(_ context.Context, req *v1.SelectRequest) (*v1.SelectResponse, error) {
	if strings.Contains(req.Options.Message, "Do you want to provision") {
		answer := s.followUp
		if answer == "" {
			answer = "No"
		}
		for i, choice := range req.Options.Choices {
			if choice.Label == answer {
				return &v1.SelectResponse{Value: new(int32(i))}, nil
			}
		}
	}
	needles := []string{"database", "redis", "later"}
	if s.hostProject != "" {
		needles = []string{"host service", "app service"}
	}
	if s.existingResourceID != "" {
		needles = []string{"existing resource", "storage account", "later"}
	}
	for _, needle := range needles {
		for i, choice := range req.Options.Choices {
			if strings.Contains(strings.ToLower(choice.Label), needle) {
				return &v1.SelectResponse{Value: new(int32(i))}, nil
			}
		}
	}
	return nil, errors.New("unexpected selection: " + req.Options.Message)
}

func (s *addPromptServer) PromptSubscriptionResource(
	context.Context, *v1.PromptSubscriptionResourceRequest,
) (*v1.PromptSubscriptionResourceResponse, error) {
	return &v1.PromptSubscriptionResourceResponse{Resource: &v1.ResourceExtended{Id: s.existingResourceID}}, nil
}

type addAccountServer struct {
	v1.UnimplementedAccountServiceServer
}

func (s *addAccountServer) LookupTenant(
	context.Context, *v1.LookupTenantRequest,
) (*v1.LookupTenantResponse, error) {
	return &v1.LookupTenantResponse{TenantId: "access-tenant"}, nil
}

type addWorkflowServer struct {
	v1.UnimplementedWorkflowServiceServer
	prompts *addPromptServer
}

func (s *addWorkflowServer) Run(_ context.Context, req *v1.RunWorkflowRequest) (*v1.EmptyResponse, error) {
	for _, step := range req.GetWorkflow().GetSteps() {
		s.prompts.commands = append(s.prompts.commands, step.GetCommand().GetArgs())
		if s.prompts.workflowError != nil {
			return nil, s.prompts.workflowError
		}
	}
	return &v1.EmptyResponse{}, nil
}

func (s *addPromptServer) MultiSelect(
	context.Context, *v1.MultiSelectRequest,
) (*v1.MultiSelectResponse, error) {
	return &v1.MultiSelectResponse{}, nil
}

type addEnvironmentServer struct {
	v1.UnimplementedEnvironmentServiceServer
	writes       int
	project      *addProjectServer
	reads        []*v1.GetEnvironmentRequest
	saved        []*v1.SetEnvRequest
	currentCalls int
	selectedErr  error
}

type addProjectServer struct {
	v1.UnimplementedProjectServiceServer
	requests      []*v1.SetProjectConfigSectionRequest
	layerRequests []*v1.SetProjectConfigValueRequest
	unsetRequests []string
	config        config.Config
	path          string
	err           error
	layerErr      error
	readErr       error
	unsetErr      error
}

func (s *addProjectServer) GetConfigValue(
	_ context.Context, req *v1.GetProjectConfigValueRequest,
) (*v1.GetProjectConfigValueResponse, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	value, found := s.config.Get(req.Path)
	if !found {
		return &v1.GetProjectConfigValueResponse{}, nil
	}
	protoValue, err := structpb.NewValue(value)
	if err != nil {
		return nil, err
	}
	return &v1.GetProjectConfigValueResponse{Value: protoValue, Found: true}, nil
}

func (s *addProjectServer) SetConfigValue(
	_ context.Context, req *v1.SetProjectConfigValueRequest,
) (*v1.EmptyResponse, error) {
	s.layerRequests = append(s.layerRequests, req)
	if s.layerErr != nil {
		return nil, s.layerErr
	}
	if err := s.config.Set(req.Path, req.Value.AsInterface()); err != nil {
		return nil, err
	}
	return &v1.EmptyResponse{}, s.save()
}

func (s *addProjectServer) save() error {
	if s.path == "" {
		return nil
	}
	contents, err := yaml.Marshal(s.config.Raw())
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, contents, 0o600)
}

func (s *addProjectServer) SetConfigSection(
	_ context.Context, req *v1.SetProjectConfigSectionRequest,
) (*v1.EmptyResponse, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	if err := s.config.Set(req.Path, req.Section.AsMap()); err != nil {
		return nil, err
	}
	return &v1.EmptyResponse{}, s.save()
}

func (s *addProjectServer) UnsetConfig(
	_ context.Context, req *v1.UnsetProjectConfigRequest,
) (*v1.EmptyResponse, error) {
	s.unsetRequests = append(s.unsetRequests, req.Path)
	if s.unsetErr != nil {
		return nil, s.unsetErr
	}
	if err := s.config.Unset(req.Path); err != nil {
		return nil, err
	}
	return &v1.EmptyResponse{}, s.save()
}

func (s *addEnvironmentServer) GetCurrent(context.Context, *v1.EmptyRequest) (*v1.EnvironmentResponse, error) {
	s.currentCalls++
	return &v1.EnvironmentResponse{Environment: &v1.Environment{Name: "dev"}}, nil
}

func (s *addEnvironmentServer) Get(
	_ context.Context, request *v1.GetEnvironmentRequest,
) (*v1.EnvironmentResponse, error) {
	return &v1.EnvironmentResponse{Environment: &v1.Environment{Name: request.Name}}, s.selectedErr
}

func (s *addEnvironmentServer) GetValues(
	_ context.Context, request *v1.GetEnvironmentRequest,
) (*v1.KeyValueListResponse, error) {
	s.reads = append(s.reads, request)
	return &v1.KeyValueListResponse{KeyValues: []*v1.KeyValue{
		{Key: "AZURE_SUBSCRIPTION_ID", Value: "sub"}, {Key: "AZURE_LOCATION", Value: "eastus"},
	}}, nil
}

func (s *addEnvironmentServer) SetValue(_ context.Context, request *v1.SetEnvRequest) (*v1.EmptyResponse, error) {
	s.writes++
	s.saved = append(s.saved, request)
	return &v1.EmptyResponse{}, nil
}

func startAddTestHost(t *testing.T, prompts *addPromptServer) *addEnvironmentServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	environment := &addEnvironmentServer{selectedErr: prompts.environmentError, project: &addProjectServer{
		config: config.NewEmptyConfig(), err: prompts.serviceError,
		layerErr: prompts.layerError, readErr: prompts.readError,
	}}
	v1.RegisterPromptServiceServer(server, prompts)
	v1.RegisterEnvironmentServiceServer(server, environment)
	v1.RegisterWorkflowServiceServer(server, &addWorkflowServer{prompts: prompts})
	v1.RegisterProjectServiceServer(server, environment.project)
	v1.RegisterAccountServiceServer(server, &addAccountServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Setenv("AZD_SERVER", listener.Addr().String())
	return environment
}

func runAddTest(
	t *testing.T, original string, args []string, prompts *addPromptServer,
) (string, string, *addEnvironmentServer, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.yaml")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
	environment := startAddTestHost(t, prompts)
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(original), &raw); err != nil {
		environment.project.readErr = err
	} else {
		environment.project.config = config.NewConfig(raw)
	}
	environment.project.path = path
	t.Setenv("AZD_EXEC_PROJECT_DIR", dir)
	t.Setenv("AZD_CWD", "")
	t.Setenv("AZD_NO_PROMPT", "false")
	command := NewRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(append([]string{"add"}, args...))
	command.SetContext(t.Context())
	runErr := command.Execute()
	//nolint:gosec // G304: path is an azure.yaml fixture in t.TempDir().
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(saved), output.String(), environment, runErr
}

func TestAddExistingLayerDeclineDoesNotWrite(t *testing.T) {
	for _, provider := range []string{"resources", "bicep", "terraform"} {
		t.Run(provider, func(t *testing.T) {
			original := "name: example\ninfra:\n  layers:\n    - name: data\n      provider: " + provider + "\n"
			prompts := &addPromptServer{layerName: "data"}
			saved, _, environment, err := runAddTest(t, original, nil, prompts)
			require.NoError(t, err)
			require.Equal(t, original, saved)
			require.Zero(t, environment.writes)
			if provider == "resources" {
				require.Zero(t, prompts.layerPrompts)
			} else {
				require.Equal(t, 1, prompts.layerPrompts)
			}
			require.Len(t, prompts.confirmations, 1)
			require.Equal(t, new(false), prompts.confirmations[0].Options.DefaultValue)
		})
	}
}

func TestAddNoPromptLeavesProjectUnchanged(t *testing.T) {
	original := "name: example\n"
	saved, _, environment, err := runAddTest(t, original, []string{"--no-prompt"},
		&addPromptServer{})
	require.ErrorContains(t, err, "requires interactive input")
	require.Equal(t, original, saved)
	require.Zero(t, environment.writes)
}

func TestAddRejectsInvalidPromptedLayer(t *testing.T) {
	original := "name: example\n"
	prompts := &addPromptServer{layerName: "../invalid"}
	saved, _, host, err := runAddTest(t, original, nil, prompts)
	require.ErrorContains(t, err, "invalid infrastructure layer name")
	require.Equal(t, original, saved)
	require.Equal(t, 1, prompts.layerPrompts)
	require.Empty(t, host.project.layerRequests)
	require.Zero(t, host.writes)
}

func TestAddInvalidProjectDoesNotPromptOrWrite(t *testing.T) {
	original := "name: example\nresources:\n  cache:\n    type: db.redis\n"
	prompts := &addPromptServer{}
	saved, _, environment, err := runAddTest(t, original, nil, prompts)
	require.ErrorContains(t, err, "top-level resources")
	require.Equal(t, original, saved)
	require.Empty(t, prompts.confirmations)
	require.Zero(t, prompts.layerPrompts)
	require.Zero(t, environment.writes)
}

func TestAddRejectsUnsupportedOutput(t *testing.T) {
	original := "name: example\n"
	saved, _, environment, err := runAddTest(t, original, []string{"--output", "json"}, &addPromptServer{})
	require.Error(t, err)
	require.Equal(t, original, saved)
	require.Zero(t, environment.writes)
}

func TestAddWizardWritesOnlySelectedLayerConfig(t *testing.T) {
	prompts := &addPromptServer{layerName: "data"}
	saved, output, host, err := runAddTest(t, "name: example\n", nil, prompts)
	require.NoError(t, err)
	project := readAddTestProject(t, saved, "data")
	require.Len(t, project.config, 2)
	require.Equal(t, "db.redis", project.config["redis"].Type)
	require.Equal(t, "keyvault", project.config["vault"].Type)
	require.NotContains(t, saved, "\nresources:")
	require.Equal(t, 1, prompts.layerPrompts)
	require.Empty(t, prompts.confirmations, "new layers are saved without final confirmation")
	require.Contains(t, output, "azure.yaml updated:\n"+
		"  Added resources layer \"data\".\n"+
		"  Added resource \"redis\" (db.redis).\n"+
		"  Added resource \"vault\" (keyvault).\n")
	require.NotContains(t, output, "Proposed azure.yaml")
	require.NotContains(t, output, "provider: resources")
	require.Contains(t, output, "azd provision data")
	require.Len(t, host.project.layerRequests, 1)
	require.Equal(t, "infra.layers", host.project.layerRequests[0].Path)
	require.Empty(t, host.project.requests)
	require.NoFileExists(t, host.project.path+".lock")
}

func TestAddWizardSavesAfterPromptingForLayer(t *testing.T) {
	original := "name: example\n"
	prompts := &addPromptServer{}
	saved, output, _, err := runAddTest(t, original, nil, prompts)
	require.NoError(t, err)
	project := readAddTestProject(t, saved, defaultResourcesLayer)
	require.Len(t, project.config, 2)
	require.Contains(t, output, "Added resources layer \"azureresources\".")
	require.Equal(t, 1, prompts.layerPrompts)
	require.Empty(t, prompts.confirmations)
}

func TestAddReusesExistingResourcesLayerWithoutNamePrompt(t *testing.T) {
	original := `name: example
infra:
  layers:
    - name: data
      provider: resources
`
	prompts := &addPromptServer{acceptExisting: true}

	saved, output, host, err := runAddTest(t, original, nil, prompts)
	require.NoError(t, err)

	// Add the wizard's resources to data instead of creating a second layer.
	project := readAddTestProject(t, saved, "data")
	require.Len(t, project.layers, 1)
	require.Len(t, project.config, 2)

	require.Zero(t, prompts.layerPrompts)
	require.Len(t, prompts.confirmations, 1)
	require.Equal(t, `Add resources to existing layer "data"?`, prompts.confirmations[0].Options.Message)
	require.Equal(t, new(false), prompts.confirmations[0].Options.DefaultValue)

	require.Len(t, host.project.layerRequests, 1)
	require.Contains(t, output, "azd provision data")
}

func TestAddReusesResourcesLayerAndPreservesOtherLayers(t *testing.T) {
	original := `name: example
infra:
  layers:
    - name: platform
      provider: bicep
      path: infra/platform
    - name: data
      provider: resources
      config:
        files:
          type: storage
`
	prompts := &addPromptServer{acceptExisting: true}

	saved, output, host, err := runAddTest(t, original, nil, prompts)
	require.NoError(t, err)

	project := readAddTestProject(t, saved, "data")
	require.Len(t, project.layers, 2)
	require.Equal(t, map[string]any{
		"name": "platform", "provider": "bicep", "path": "infra/platform",
	}, project.layers[0])
	require.Equal(t, 1, project.index)
	require.Len(t, project.config, 3)
	require.Equal(t, "storage", project.config["files"].Type)
	require.Equal(t, "db.redis", project.config["redis"].Type)
	require.Equal(t, "keyvault", project.config["vault"].Type)

	require.Zero(t, prompts.layerPrompts)
	require.Len(t, prompts.confirmations, 1)
	require.Equal(t, `Add resources to existing layer "data"?`, prompts.confirmations[0].Options.Message)
	require.Equal(t, new(false), prompts.confirmations[0].Options.DefaultValue)
	require.Len(t, host.project.layerRequests, 1)
	require.Empty(t, host.project.requests)
	require.Contains(t, output, "Updated resources layer \"data\".")
	require.Contains(t, output, "azd provision data")
}

func TestAddDoesNotDefaultToLayerWithoutExplicitProvider(t *testing.T) {
	// infra declares resources, but data itself has no provider and must not inherit it.
	original := `name: example
infra:
  provider: resources
  layers:
    - name: data
`
	prompts := &addPromptServer{}

	// Accept the wizard's default name rather than explicitly selecting data.
	saved, output, host, err := runAddTest(t, original, nil, prompts)
	require.NoError(t, err)

	// Preserve data and append the default resources layer with the wizard's resources.
	project := readAddTestProject(t, saved, defaultResourcesLayer)
	require.Len(t, project.layers, 2)
	require.Equal(t, map[string]any{"name": "data"}, project.layers[0])
	require.Equal(t, defaultResourcesLayer, project.resourcesLayer)
	require.Len(t, project.config, 2)

	// Creating a new layer needs no reuse confirmation; the follow-up must name the new layer.
	require.Equal(t, 1, prompts.layerPrompts)
	require.Empty(t, prompts.confirmations)
	require.Len(t, host.project.layerRequests, 1)
	require.Contains(t, output, "azd provision "+defaultResourcesLayer)
}

func TestAddProviderSelectionRunsWizard(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", t.TempDir())
	prompts := &addPromptServer{
		followUp:           "No",
		existingResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/shared",
	}
	host := startAddTestHost(t, prompts)
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(client.Close)
	var output bytes.Buffer
	handler := newAddProviderHandler(client, &azdext.ExtensionContext{Environment: "staging"}, &output)

	require.NoError(t, handler(t.Context(), azureResourceAddItemID))
	require.Equal(t, 1, prompts.layerPrompts)
	require.Len(t, host.project.layerRequests, 1)
	require.Equal(t, "infra.layers", host.project.layerRequests[0].Path)
	require.Zero(t, host.currentCalls)
	require.NotEmpty(t, host.reads)
	for _, read := range host.reads {
		require.Equal(t, "staging", read.Name)
	}
	require.NotEmpty(t, host.saved)
	for _, write := range host.saved {
		require.Equal(t, "staging", write.EnvName)
	}
	require.Contains(t, output.String(), "azure.yaml updated:")
	require.Contains(t, output.String(), `azd provision azureresources --environment "staging"`)
	require.Empty(t, prompts.commands)
}

func TestAddProviderRejectsInvalidRequestsWithoutHost(t *testing.T) {
	for _, test := range []struct {
		name     string
		itemID   string
		noPrompt bool
		want     string
	}{
		{name: "empty ID", want: invalidAddItemCode},
		{name: "unknown ID", itemID: "unknown", want: invalidAddItemCode},
		{name: "no prompt", itemID: azureResourceAddItemID, noPrompt: true, want: addRequiresPromptCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := newAddProviderHandler(nil, &azdext.ExtensionContext{NoPrompt: test.noPrompt}, &output)
			err := handler(t.Context(), test.itemID)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected a local validation error, got %v", err)
			require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
			require.Equal(t, test.want, localErr.Code)
			require.Empty(t, output.String())
		})
	}
}

func TestAddWizardReplacesOnlyExplicitlyAcceptedLayer(t *testing.T) {
	original := `name: example
infra:
  layers:
    - name: data
      provider: terraform
      path: infra/old
    - name: untouched
      provider: bicep
      path: infra/native
`
	prompts := &addPromptServer{acceptExisting: true, layerName: "data"}
	saved, output, _, err := runAddTest(t, original, nil, prompts)
	require.NoError(t, err)
	project := readAddTestProject(t, saved, "data")
	require.Equal(t, "resources", project.provider)
	untouched := readAddTestProject(t, saved, "untouched")
	require.Equal(t, "bicep", untouched.provider)
	require.Contains(t, saved, "infra/native")
	require.NotContains(t, saved, "infra/old")
	require.Len(t, prompts.confirmations, 1)
	require.Contains(t, output, "Replaced resources layer \"data\".")
	require.Equal(t, new(false), prompts.confirmations[0].Options.DefaultValue)
	require.Contains(t, prompts.confirmations[0].Options.Message, "Replace")
}

func TestAddProvisionFollowUpsUseLocalHostWorkflow(t *testing.T) {
	for _, test := range []struct {
		name       string
		followUp   string
		hasService bool
		accept     bool
		want       [][]string
	}{
		{name: "later", followUp: "No"},
		{name: "provision", followUp: "Yes", want: [][]string{{"provision", "data"}}},
		{name: "deploy", followUp: "Yes", hasService: true, want: [][]string{{"up"}}},
		{name: "preview declined", followUp: "Yes (preview changes)",
			want: [][]string{{"provision", "data", "--preview"}}},
		{name: "preview accepted", followUp: "Yes (preview changes)", accept: true,
			want: [][]string{{"provision", "data", "--preview"}, {"provision", "data"}}},
		{name: "preview and deploy", followUp: "Yes (preview changes)", accept: true, hasService: true,
			want: [][]string{{"provision", "data", "--preview"}, {"up"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompts := &addPromptServer{followUp: test.followUp, acceptProvision: test.accept}
			startAddTestHost(t, prompts)
			client, err := azdext.NewAzdClient()
			require.NoError(t, err)
			t.Cleanup(client.Close)
			var output bytes.Buffer
			require.NoError(t, followUpAdd(t.Context(), client, test.hasService, "data", "", &output))
			require.Equal(t, test.want, prompts.commands)
			if test.followUp == "Yes (preview changes)" {
				require.Len(t, prompts.confirmations, 1)
				require.Equal(t, new(true), prompts.confirmations[0].Options.DefaultValue)
			} else {
				require.Empty(t, prompts.confirmations)
			}
		})
	}
}

func TestAddPreviewTargetsSelectedLayerInMultiLayerProject(t *testing.T) {
	original := `name: example
infra:
  layers:
    - name: platform
      provider: bicep
      path: infra/platform
`
	for _, test := range []struct {
		name       string
		hasService bool
		wantApply  []string
	}{
		{name: "host", hasService: true, wantApply: []string{"up", "--environment", "staging"}},
		{name: "resource", wantApply: []string{"provision", "application", "--environment", "staging"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompts := &addPromptServer{followUp: "Yes (preview changes)", acceptProvision: true, layerName: "application"}
			if test.hasService {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("fastapi\n"), 0o600))
				prompts.hostProject = dir
			}
			_, _, _, err := runAddTest(t, original, []string{"--environment", "staging"}, prompts)
			require.NoError(t, err)
			require.Equal(t, [][]string{
				{"provision", "application", "--preview", "--environment", "staging"},
				test.wantApply,
			}, prompts.commands)
			require.Equal(t, 1, prompts.commandsAtConfirm)
			require.Len(t, prompts.confirmations, 1)
			require.Equal(t, new(true), prompts.confirmations[0].Options.DefaultValue)
		})
	}
}

func TestAddHostPreviewDoesNotDeployWithoutSuccessfulConfirmation(t *testing.T) {
	for _, test := range []struct {
		name          string
		workflowError error
		confirmError  error
		wantError     string
	}{
		{name: "declined"},
		{name: "preview failed", workflowError: status.Error(codes.Internal, "preview failed"), wantError: "preview failed"},
		{name: "preview canceled", workflowError: status.Error(codes.Canceled, "preview canceled"),
			wantError: "preview canceled"},
		{name: "confirmation failed", confirmError: status.Error(codes.Canceled, "confirmation canceled"),
			wantError: "confirmation canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompts := &addPromptServer{
				followUp:      "Yes (preview changes)",
				workflowError: test.workflowError, confirmError: test.confirmError,
			}
			startAddTestHost(t, prompts)
			client, err := azdext.NewAzdClient()
			require.NoError(t, err)
			t.Cleanup(client.Close)

			var output bytes.Buffer
			err = followUpAdd(t.Context(), client, true, "application", "staging", &output)
			if test.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantError)
			}
			want := [][]string{
				{"provision", "application", "--preview", "--environment", "staging"},
			}
			if test.workflowError == nil {
				require.Len(t, prompts.confirmations, 1)
				require.Equal(t, 1, prompts.commandsAtConfirm)
			} else {
				require.Empty(t, prompts.confirmations)
			}
			require.Equal(t, want, prompts.commands)
		})
	}
}

func TestAddExistingResourcesLayerDeclineDoesNotPromptForNameOrWrite(t *testing.T) {
	original := "name: example\ninfra:\n  layers:\n    - name: data\n      provider: resources\n"
	prompts := &addPromptServer{}

	saved, _, host, err := runAddTest(t, original, nil, prompts)

	// Declining is a successful cancellation, with no project or environment writes.
	require.NoError(t, err)
	require.Equal(t, original, saved)
	require.Zero(t, prompts.layerPrompts)
	require.Len(t, prompts.confirmations, 1)
	require.Equal(t, `Add resources to existing layer "data"?`, prompts.confirmations[0].Options.Message)
	require.Empty(t, host.project.layerRequests)
	require.Empty(t, host.saved)
}

func TestAddRejectsMissingSelectedEnvironmentBeforeMutations(t *testing.T) {
	original := "name: example\n"
	prompts := &addPromptServer{
		environmentError:   status.Error(codes.NotFound, "environment not found"),
		existingResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/shared",
	}
	saved, _, host, err := runAddTest(t, original, []string{"--environment", "missing"}, prompts)
	require.ErrorContains(t, err, `getting environment "missing"`)
	require.Equal(t, original, saved)
	require.Zero(t, host.currentCalls)
	require.Empty(t, host.reads)
	require.Empty(t, host.saved)
	require.Empty(t, host.project.layerRequests)
	require.Empty(t, host.project.requests)
	require.Empty(t, prompts.commands)
}

func TestAddUsesSelectedEnvironmentForDiscoverySavingAndWorkflow(t *testing.T) {
	for _, selection := range []string{"flag", "environment variable"} {
		t.Run(selection, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", "")
			var args []string
			if selection == "flag" {
				args = append(args, "--environment", "staging")
			} else {
				t.Setenv("AZD_ENVIRONMENT", "staging")
			}
			prompts := &addPromptServer{
				followUp: "Yes (preview changes)", acceptProvision: true,
				layerName: "data",
				existingResourceID: "/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.Storage/storageAccounts/shared",
			}
			_, _, host, err := runAddTest(t, "name: example\n", args, prompts)
			require.NoError(t, err)
			require.Zero(t, host.currentCalls)
			require.NotEmpty(t, host.reads)
			for _, read := range host.reads {
				require.Equal(t, "staging", read.Name)
			}
			require.NotEmpty(t, host.saved)
			for _, write := range host.saved {
				require.Equal(t, "staging", write.EnvName)
			}
			require.Equal(t, [][]string{
				{"provision", "data", "--preview", "--environment", "staging"},
				{"provision", "data", "--environment", "staging"},
			}, prompts.commands)
		})
	}
}
