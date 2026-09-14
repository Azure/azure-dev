// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const initializedAgentPreviewYAML = `
name: agent-framework-agent-basic-responses
services:
  agent-framework-agent-basic-responses:
    project: src/agent-framework-agent-basic-responses
    host: azure.ai.agent
    language: python
    uses:
      - ai-project
    env:
      AZURE_AI_MODEL_DEPLOYMENT_NAME: ${AZURE_AI_MODEL_DEPLOYMENT_NAME}
    codeConfiguration:
      entryPoint: main.py
      runtime: python_3_13
    container:
      resources:
        cpu: "0.5"
        memory: 1Gi
    description: A basic Agent Framework agent hosted by Foundry.
    kind: hosted
    name: agent-framework-agent-basic-responses
    protocols:
      - protocol: responses
        version: 2.0.0
  ai-project:
    host: azure.ai.project
    endpoint: https://account.services.ai.azure.com/api/projects/project
`

func initializedPreviewProject(t *testing.T, environment map[string]string) *azdext.ProjectConfig {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(initializedAgentPreviewYAML), 0o600))
	var document struct {
		Name     string `yaml:"name"`
		Services map[string]struct {
			Project    string            `yaml:"project"`
			Host       string            `yaml:"host"`
			Language   string            `yaml:"language"`
			Uses       []string          `yaml:"uses"`
			Env        map[string]string `yaml:"env"`
			Properties map[string]any    `yaml:",inline"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(initializedAgentPreviewYAML), &document))
	config := &azdext.ProjectConfig{Name: document.Name, Path: root, Services: map[string]*azdext.ServiceConfig{}}
	for name, entry := range document.Services {
		props, err := structpb.NewStruct(entry.Properties)
		require.NoError(t, err)
		expanded := make(map[string]string, len(entry.Env))
		for key, value := range entry.Env {
			expanded[key], err = project.ExpandEnv(value, func(key string) string { return environment[key] })
			require.NoError(t, err)
		}
		config.Services[name] = &azdext.ServiceConfig{
			Name: name, Host: entry.Host, Language: entry.Language, RelativePath: entry.Project,
			Uses: entry.Uses, Environment: expanded, AdditionalProperties: props,
		}
		if entry.Project != "" {
			require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(entry.Project)), 0o700))
		}
	}
	return config
}

func TestLoadProjectAgentPreviewFromFreshInit(t *testing.T) {
	t.Parallel()
	// #nosec G101 -- Synthetic unrelated values verify environment isolation.
	environment := map[string]string{
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "test-model",
		"FOUNDRY_PROJECT_ENDPOINT":       "https://other.services.ai.azure.com/api/projects/other",
		"UNRELATED_SECRET":               "not-an-agent-variable",
	}
	config := initializedPreviewProject(t, environment)
	prompts := &helpersPromptServer{}
	client := newHelpersTestAzdClient(t, &helpersProjectServer{project: config}, prompts, &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"}, values: map[string]map[string]string{"test": environment},
	})

	options, err := loadProjectAgentPreview(t.Context(), client, agentDeployFlags{dryRun: true, noPrompt: true})
	require.NoError(t, err)
	assert.Equal(t, "agent-framework-agent-basic-responses", options.Service.Name)
	assert.Equal(t, config.Path, options.ProjectRoot)
	assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", options.ProjectEndpoint,
		"the used project must win over a stale global/environment endpoint")
	assert.Equal(t, "test-model", options.Service.Environment["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.Empty(t, options.PendingEnvironment)
	assert.Zero(t, prompts.selectCalls.Load())
	require.NoFileExists(t, filepath.Join(config.Path, "agent.yaml"))
	require.NoFileExists(t, filepath.Join(config.Path, "src", "agent-framework-agent-basic-responses", "agent.yaml"))

	options, err = loadProjectAgentPreview(t.Context(), client, agentDeployFlags{
		dryRun: true, noPrompt: true, codePath: "explicit-source",
		projectEndpoint: "https://override.services.ai.azure.com/api/projects/override/",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://override.services.ai.azure.com/api/projects/override", options.ProjectEndpoint)
	assert.Equal(t, "explicit-source", options.CodePath)
}

func TestLoadProjectAgentPreviewWithoutDeploymentEnvironment(t *testing.T) {
	t.Setenv("AZURE_AI_MODEL_DEPLOYMENT_NAME", "")
	config := initializedPreviewProject(t, nil)
	client := newHelpersTestAzdClient(t, &helpersProjectServer{project: config}, &helpersPromptServer{},
		&testEnvironmentServiceServer{})
	options, err := loadProjectAgentPreview(t.Context(), client, agentDeployFlags{dryRun: true, noPrompt: true})
	require.NoError(t, err)
	assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", options.ProjectEndpoint)
	assert.Empty(t, options.Environment)
	require.NoFileExists(t, filepath.Join(config.Path, "agent.yaml"))
}

func TestLoadProjectAgentPreviewSelection(t *testing.T) {
	t.Parallel()
	config := initializedPreviewProject(t, nil)
	config.Services["another"] = &azdext.ServiceConfig{Name: "another", Host: AiAgentHost}
	client := newHelpersTestAzdClient(t, &helpersProjectServer{project: config}, &helpersPromptServer{},
		&testEnvironmentServiceServer{})
	_, err := loadProjectAgentPreview(t.Context(), client, agentDeployFlags{noPrompt: true, dryRun: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--service")
	assert.NotContains(t, err.Error(), "positional argument")

	options, err := loadProjectAgentPreview(t.Context(), client, agentDeployFlags{
		noPrompt: true, dryRun: true, serviceName: "agent-framework-agent-basic-responses",
	})
	require.NoError(t, err)
	assert.Equal(t, "agent-framework-agent-basic-responses", options.Service.Name)

	_, err = loadProjectAgentPreview(t.Context(), client, agentDeployFlags{
		noPrompt: true, dryRun: true, serviceName: "not-an-agent",
	})
	require.ErrorContains(t, err, "no azure.ai.agent service named")
}

func TestAgentDeployDryRunDefaultsToInitializedProject(t *testing.T) {
	config := initializedPreviewProject(t, map[string]string{"AZURE_AI_MODEL_DEPLOYMENT_NAME": "test-model"})
	t.Chdir(config.Path)
	client := newHelpersTestAzdClient(t, &helpersProjectServer{project: config}, &helpersPromptServer{},
		&testEnvironmentServiceServer{
			current: &azdext.Environment{Name: "test"},
			values:  map[string]map[string]string{"test": {"AZURE_AI_MODEL_DEPLOYMENT_NAME": "test-model"}},
		})
	runner := &recordingDependencyRunner{}
	dependencies := dryRunOnlyDependencies(t, runner, func(
		context.Context, project.DirectDeployOptions, []string,
	) (*project.DirectDeployPreviewResult, error) {
		t.Fatal("an initialized inline project must not require a standalone agent.yaml")
		return nil, nil
	})
	projectCalls := 0
	dependencies.projectPreviewer = func(
		ctx context.Context, flags agentDeployFlags,
	) (*project.DirectDeployPreviewResult, error) {
		projectCalls++
		options, err := loadProjectAgentPreview(ctx, client, flags)
		require.NoError(t, err)
		assert.Equal(t, config.Path, options.ProjectRoot)
		assert.Equal(t, "agent-framework-agent-basic-responses", options.Service.Name)
		return &project.DirectDeployPreviewResult{
			Name: "agent-framework-agent-basic-responses", Service: options.Service.Name,
			Operation: "create", HasChanges: true, Changes: []project.DeployPreviewChangeGroup{},
		}, nil
	}
	command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{NoPrompt: true}, dependencies)
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--dry-run"})
	require.NoError(t, command.Execute())
	assert.Equal(t, 1, projectCalls)
	assert.Contains(t, out.String(), "Agent does not exist; would be created.")
	assert.Contains(t, out.String(), "Dry run complete. No changes were made.")
	assert.Empty(t, runner.args)
	require.NoFileExists(t, filepath.Join(config.Path, "agent.yaml"))
}

func TestAgentDeployDryRunPreservesDefaultStandaloneFile(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	require.NoError(t, os.WriteFile("agent.yaml", []byte("name: agent\nkind: hosted\n"), 0o600))
	dependencies := dryRunOnlyDependencies(t, &recordingDependencyRunner{}, func(
		_ context.Context, options project.DirectDeployOptions, _ []string,
	) (*project.DirectDeployPreviewResult, error) {
		assert.Equal(t, "agent.yaml", options.DefinitionPath)
		return &project.DirectDeployPreviewResult{Name: "agent", Operation: "create", HasChanges: true}, nil
	})
	dependencies.projectPreviewer = func(context.Context, agentDeployFlags) (*project.DirectDeployPreviewResult, error) {
		t.Fatal("an existing default standalone definition must remain supported")
		return nil, nil
	}
	command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{OutputFormat: "json"}, dependencies)
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--dry-run", "--project-endpoint", "https://account.services.ai.azure.com/api/projects/project"})
	require.NoError(t, command.Execute())
	assert.True(t, json.Valid(out.Bytes()))
}

func TestAgentDeployServiceSelectsProjectOverDefaultFile(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("agent.yaml", []byte("invalid: ["), 0o600))
	dependencies := dryRunOnlyDependencies(t, &recordingDependencyRunner{}, func(
		context.Context, project.DirectDeployOptions, []string,
	) (*project.DirectDeployPreviewResult, error) {
		t.Fatal("--service must select project configuration rather than the default file")
		return nil, nil
	})
	projectCalls := 0
	dependencies.projectPreviewer = func(
		_ context.Context, flags agentDeployFlags,
	) (*project.DirectDeployPreviewResult, error) {
		projectCalls++
		assert.Equal(t, "selected-agent", flags.serviceName)
		assert.True(t, flags.noPrompt, "JSON previews must not prompt for service selection")
		return &project.DirectDeployPreviewResult{Name: "agent", Service: flags.serviceName, Operation: "create"}, nil
	}
	command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{OutputFormat: "json"}, dependencies)
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--dry-run", "--service", "selected-agent"})
	require.NoError(t, command.Execute())
	assert.Equal(t, 1, projectCalls)
	assert.True(t, json.Valid(out.Bytes()))
}

func TestPendingProjectEnvironment(t *testing.T) {
	const key = "AZURE_AI_MODEL_DEPLOYMENT_NAME"
	t.Setenv(key, "")
	require.NoError(t, os.Unsetenv(key))
	config := initializedPreviewProject(t, nil)
	service := config.Services["agent-framework-agent-basic-responses"]
	pending, err := pendingProjectEnvironment(config.Path, service, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{key}, pending)

	pending, err = pendingProjectEnvironment(config.Path, service, map[string]string{key: ""})
	require.NoError(t, err)
	assert.Empty(t, pending, "an explicitly empty input is known, not pending")

	service.Environment[key] = "core-resolved-model"
	pending, err = pendingProjectEnvironment(config.Path, service, nil)
	require.NoError(t, err)
	assert.Empty(t, pending, "values resolved by core must not be re-expanded")
}

func TestProjectPreviewEndpointValidation(t *testing.T) {
	t.Parallel()
	config := initializedPreviewProject(t, nil)
	service := config.Services["agent-framework-agent-basic-responses"]
	projectService := config.Services["ai-project"]
	projectService.AdditionalProperties.Fields["endpoint"] = structpb.NewStringValue("${PROJECT_ENDPOINT}")
	environment := map[string]string{"PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/selected"}
	endpoint, err := resolveProjectPreviewEndpoint(t.Context(), service, config, environment, "")
	require.NoError(t, err)
	assert.Equal(t, environment["PROJECT_ENDPOINT"], endpoint)

	projectService.AdditionalProperties.Fields["endpoint"] = structpb.NewStringValue("http://invalid")
	_, err = resolveProjectPreviewEndpoint(t.Context(), service, config, environment, "")
	require.Error(t, err, "invalid bound endpoints must not silently fall back to global context")

	projectService.AdditionalProperties.Fields["endpoint"] = structpb.NewStringValue("")
	environment["FOUNDRY_PROJECT_ENDPOINT"] = "https://account.services.ai.azure.com/api/projects/from-env"
	endpoint, err = resolveProjectPreviewEndpoint(t.Context(), service, config, environment, "")
	require.NoError(t, err)
	assert.Equal(t, environment["FOUNDRY_PROJECT_ENDPOINT"], endpoint)

	service.Uses = append(service.Uses, "missing")
	_, err = resolveProjectPreviewEndpoint(t.Context(), service, config, environment, "")
	require.ErrorContains(t, err, "unknown service")

	service.Uses = []string{"ai-project", "other-project"}
	config.Services["other-project"] = &azdext.ServiceConfig{Name: "other-project", Host: AiProjectHost}
	_, err = resolveProjectPreviewEndpoint(t.Context(), service, config, environment, "")
	require.ErrorContains(t, err, "multiple Foundry projects")
}

func TestAgentDeployServiceFlagConflicts(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--service", "agent"},
		{"--dry-run=false", "--service", "agent"},
		{"agent.yaml", "--dry-run", "--service", "agent"},
		{"--dry-run", "--service", ""},
		{"", "--dry-run"},
	} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			command := newAgentDeployCommand(&azdext.ExtensionContext{})
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(args)
			err := command.Execute()
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Contains(t, []string{
				exterrors.CodeConflictingArguments, exterrors.CodeInvalidParameter, exterrors.CodeInvalidFilePath,
			}, localErr.Code)
		})
	}
}

func TestAgentDeployExplicitMissingFileDoesNotFallBackToProject(t *testing.T) {
	t.Parallel()
	dependencies := agentDeployDependencies{
		projectPreviewer: func(context.Context, agentDeployFlags) (*project.DirectDeployPreviewResult, error) {
			t.Fatal("an explicit missing file must not silently select project configuration")
			return nil, nil
		},
	}
	command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{}, dependencies)
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{
		filepath.Join(t.TempDir(), "missing.yaml"), "--dry-run",
		"--project-endpoint", "https://account.services.ai.azure.com/api/projects/project",
	})
	require.Error(t, command.Execute())
}

type previewFailingEnvironmentServer struct {
	testEnvironmentServiceServer
	currentError error
	valuesError  error
}

func (s *previewFailingEnvironmentServer) GetCurrent(
	ctx context.Context, request *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.currentError != nil {
		return nil, s.currentError
	}
	return s.testEnvironmentServiceServer.GetCurrent(ctx, request)
}

func (s *previewFailingEnvironmentServer) GetValues(
	ctx context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if s.valuesError != nil {
		return nil, s.valuesError
	}
	return s.testEnvironmentServiceServer.GetValues(ctx, request)
}

func TestLoadProjectAgentPreviewDoesNotHideReadFailures(t *testing.T) {
	t.Parallel()
	for _, server := range []*previewFailingEnvironmentServer{
		{currentError: status.Error(codes.PermissionDenied, "denied")},
		{testEnvironmentServiceServer: testEnvironmentServiceServer{current: &azdext.Environment{Name: "test"}},
			valuesError: status.Error(codes.NotFound, "environment disappeared")},
	} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			config := initializedPreviewProject(t, nil)
			client := newHelpersTestAzdClient(t, &helpersProjectServer{project: config}, &helpersPromptServer{}, server)
			_, err := loadProjectAgentPreview(t.Context(), client, agentDeployFlags{dryRun: true, noPrompt: true})
			require.Error(t, err)
		})
	}
}
