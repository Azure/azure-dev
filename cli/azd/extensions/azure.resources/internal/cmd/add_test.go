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
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type addPromptServer struct {
	v1.UnimplementedPromptServiceServer
	acceptExisting  bool
	acceptProvision bool
	confirmations   []*v1.ConfirmRequest
	layerPrompts    int
	followUp        string
	commands        [][]string
	hostProject     string
	serviceError    error
	layerError      error
	readError       error
}

func (s *addPromptServer) Prompt(_ context.Context, req *v1.PromptRequest) (*v1.PromptResponse, error) {
	value := req.Options.DefaultValue
	if s.hostProject != "" && strings.Contains(req.Options.Message, "app code project") {
		value = s.hostProject
	}
	if strings.Contains(req.Options.Message, "layer") {
		s.layerPrompts++
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
	for _, needle := range needles {
		for i, choice := range req.Options.Choices {
			if strings.Contains(strings.ToLower(choice.Label), needle) {
				return &v1.SelectResponse{Value: new(int32(i))}, nil
			}
		}
	}
	return nil, errors.New("unexpected selection: " + req.Options.Message)
}

type addWorkflowServer struct {
	v1.UnimplementedWorkflowServiceServer
	prompts *addPromptServer
}

func (s *addWorkflowServer) Run(_ context.Context, req *v1.RunWorkflowRequest) (*v1.EmptyResponse, error) {
	for _, step := range req.GetWorkflow().GetSteps() {
		s.prompts.commands = append(s.prompts.commands, step.GetCommand().GetArgs())
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
	writes  int
	project *addProjectServer
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
	return &v1.EnvironmentResponse{Environment: &v1.Environment{Name: "dev"}}, nil
}

func (s *addEnvironmentServer) GetValues(context.Context, *v1.GetEnvironmentRequest) (*v1.KeyValueListResponse, error) {
	return &v1.KeyValueListResponse{KeyValues: []*v1.KeyValue{
		{Key: "AZURE_SUBSCRIPTION_ID", Value: "sub"}, {Key: "AZURE_LOCATION", Value: "eastus"},
	}}, nil
}

func (s *addEnvironmentServer) SetValue(context.Context, *v1.SetEnvRequest) (*v1.EmptyResponse, error) {
	s.writes++
	return &v1.EmptyResponse{}, nil
}

func startAddTestHost(t *testing.T, prompts *addPromptServer) *addEnvironmentServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	environment := &addEnvironmentServer{project: &addProjectServer{
		config: config.NewEmptyConfig(), err: prompts.serviceError,
		layerErr: prompts.layerError, readErr: prompts.readError,
	}}
	v1.RegisterPromptServiceServer(server, prompts)
	v1.RegisterEnvironmentServiceServer(server, environment)
	v1.RegisterWorkflowServiceServer(server, &addWorkflowServer{prompts: prompts})
	v1.RegisterProjectServiceServer(server, environment.project)
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
			prompts := &addPromptServer{}
			saved, _, environment, err := runAddTest(t, original, []string{"--layer", "data"}, prompts)
			require.NoError(t, err)
			require.Equal(t, original, saved)
			require.Zero(t, environment.writes)
			require.Zero(t, prompts.layerPrompts, "explicit --layer must not prompt for another name")
			require.Len(t, prompts.confirmations, 1)
			require.Equal(t, new(false), prompts.confirmations[0].Options.DefaultValue)
		})
	}
}

func TestAddNoPromptLeavesProjectUnchanged(t *testing.T) {
	original := "name: example\n"
	saved, _, environment, err := runAddTest(t, original, []string{"--no-prompt", "--layer", "data"},
		&addPromptServer{})
	require.ErrorContains(t, err, "requires interactive input")
	require.Equal(t, original, saved)
	require.Zero(t, environment.writes)
}

func TestAddExplicitEmptyLayerIsRejected(t *testing.T) {
	original := "name: example\n"
	saved, _, environment, err := runAddTest(t, original, []string{"--layer="}, &addPromptServer{})
	require.ErrorContains(t, err, "--layer")
	require.Equal(t, original, saved)
	require.Zero(t, environment.writes)
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
	prompts := &addPromptServer{}
	saved, output, host, err := runAddTest(t, "name: example\n", []string{"--layer", "data"}, prompts)
	require.NoError(t, err)
	project := readAddTestProject(t, saved, "data")
	require.Len(t, project.config, 2)
	require.Equal(t, "db.redis", project.config["redis"].Type)
	require.Equal(t, "keyvault", project.config["vault"].Type)
	require.NotContains(t, saved, "\nresources:")
	require.Zero(t, prompts.layerPrompts)
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
	require.Contains(t, output, "Added resources layer \"default-resources\".")
	require.Equal(t, 1, prompts.layerPrompts)
	require.Empty(t, prompts.confirmations)
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
	prompts := &addPromptServer{acceptExisting: true}
	saved, output, _, err := runAddTest(t, original, []string{"--layer", "data"}, prompts)
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
			require.NoError(t, followUpAdd(t.Context(), client, test.hasService, "data", &output))
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
