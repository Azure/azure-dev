// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type recordingRoutineProjectClient struct {
	project     *azdext.ProjectConfig
	rawProjects []*azdext.ProjectConfig
	lastProject *azdext.ProjectConfig
	rawCalls    int
	getCalls    int
	added       []*azdext.ServiceConfig
	setPaths    []string
	setCalls    int
	setErrAt    int
}

func (client *recordingRoutineProjectClient) Get(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	client.getCalls++
	client.lastProject = client.project
	return &azdext.GetProjectResponse{Project: client.project}, nil
}

func (client *recordingRoutineProjectClient) GetConfigSection(
	_ context.Context,
	_ *azdext.GetProjectConfigSectionRequest,
	_ ...grpc.CallOption,
) (*azdext.GetProjectConfigSectionResponse, error) {
	project := client.lastProject
	if client.rawCalls < len(client.rawProjects) {
		project = client.rawProjects[client.rawCalls]
	}
	client.rawCalls++
	client.lastProject = project
	services := map[string]any{}
	for name, service := range project.GetServices() {
		values := map[string]any{}
		if properties := service.GetAdditionalProperties(); properties != nil {
			maps.Copy(values, properties.AsMap())
		}
		values["host"] = service.GetHost()
		if service.GetRelativePath() != "" {
			values["project"] = service.GetRelativePath()
		}
		if len(service.GetUses()) > 0 {
			uses := make([]any, len(service.GetUses()))
			for index, use := range service.GetUses() {
				uses[index] = use
			}
			values["uses"] = uses
		}
		if config := service.GetConfig(); config != nil {
			values["config"] = config.AsMap()
		}
		services[name] = values
	}
	section, err := structpb.NewStruct(services)
	if err != nil {
		return nil, err
	}
	return &azdext.GetProjectConfigSectionResponse{Found: true, Section: section}, nil
}

func (client *recordingRoutineProjectClient) AddService(
	_ context.Context,
	request *azdext.AddServiceRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	client.added = append(client.added, request.GetService())
	return &azdext.EmptyResponse{}, nil
}

func (client *recordingRoutineProjectClient) SetServiceConfigValue(
	_ context.Context,
	request *azdext.SetServiceConfigValueRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	client.setPaths = append(client.setPaths, request.GetPath())
	client.setCalls++
	if client.setErrAt > 0 && client.setCalls == client.setErrAt {
		return nil, assert.AnError
	}
	service := client.lastProject.GetServices()[request.GetServiceName()]
	if request.GetPath() == "uses" {
		service.Uses = stringList(request.GetValue())
		return &azdext.EmptyResponse{}, nil
	}
	if service.AdditionalProperties == nil {
		service.AdditionalProperties = &structpb.Struct{Fields: map[string]*structpb.Value{}}
	}
	service.AdditionalProperties.Fields[request.GetPath()] = request.GetValue()
	return &azdext.EmptyResponse{}, nil
}

func TestUpsertRoutineServiceCreatesFileReferenceAndAgentDependency(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "deployed-researcher")
	client := &recordingRoutineProjectClient{project: &azdext.ProjectConfig{
		Path: projectRoot,
		Services: map[string]*azdext.ServiceConfig{
			"researcher": {
				Name: "researcher", Host: aiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind": "hosted", "name": "deployed-researcher",
				}),
			},
		},
	}}

	result, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.Equal(t, "./routines/nightly.yaml", result.Ref)
	service := requireAddedService(t, client)
	assert.Equal(t, "nightly-summary", service.GetName())
	assert.Equal(t, aiRoutineHost, service.GetHost())
	assert.Equal(t, "./routines/nightly.yaml", service.GetAdditionalProperties().AsMap()["$ref"])
	assert.Equal(t, []string{"researcher"}, service.GetUses())
	assert.Len(t, service.GetAdditionalProperties().GetFields(), 1)
}

func TestUpsertRoutineServiceUpdatesFileReferenceAndPreservesUnownedFields(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "new-agent")
	existingProperties := mustStruct(t, map[string]any{
		"$ref":        "./routines/old.yaml",
		"description": "preserved override",
		"custom":      "preserved",
	})
	client := &recordingRoutineProjectClient{project: &azdext.ProjectConfig{
		Path: projectRoot,
		Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name: "nightly-summary", Host: aiRoutineHost,
				Uses: []string{"connection", "old-agent-service"}, AdditionalProperties: existingProperties,
			},
			"connection":        {Name: "connection", Host: "azure.ai.connection"},
			"old-agent-service": {Name: "old-agent-service", Host: aiAgentHost},
			"new-agent-service": {
				Name: "new-agent-service", Host: aiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "new-agent"}),
			},
		},
	}}

	result, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	require.NoError(t, err)
	assert.False(t, result.Created)
	service := client.project.GetServices()["nightly-summary"]
	properties := service.GetAdditionalProperties().AsMap()
	assert.Equal(t, "./routines/nightly.yaml", properties["$ref"])
	assert.Equal(t, "preserved override", properties["description"])
	assert.Equal(t, "preserved", properties["custom"])
	assert.ElementsMatch(t, []string{"$ref", "uses"}, client.setPaths)
	assert.Equal(t, []string{"connection", "new-agent-service"}, service.GetUses())
}

func TestUpsertRoutineServiceIsIdempotent(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "researcher")
	client := &recordingRoutineProjectClient{project: &azdext.ProjectConfig{
		Path: projectRoot,
		Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name: "nightly-summary", Host: aiRoutineHost, Uses: []string{"researcher"},
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./routines/nightly.yaml"}),
			},
			"researcher": {
				Name: "researcher", Host: aiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "researcher"}),
			},
		},
	}}

	result, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	require.NoError(t, err)
	assert.False(t, result.Created)
	assert.Empty(t, client.added)
	assert.Empty(t, client.setPaths)
}

func TestUpsertRoutineServiceRejectsInlineLegacyAndMixedServices(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "researcher")
	for name, service := range map[string]*azdext.ServiceConfig{
		"inline": {
			Name: "nightly-summary", Host: aiRoutineHost,
			AdditionalProperties: mustStruct(t, map[string]any{
				"triggers": map[string]any{}, "action": map[string]any{},
			}),
		},
		"legacy": {
			Name: "nightly-summary", Host: aiRoutineHost,
			Config: mustStruct(t, map[string]any{
				"triggers": map[string]any{}, "action": map[string]any{},
			}),
		},
		"mixed": {
			Name: "nightly-summary", Host: aiRoutineHost,
			AdditionalProperties: mustStruct(t, map[string]any{
				"$ref": "./routines/old.yaml", "action": map[string]any{},
			}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := &recordingRoutineProjectClient{project: &azdext.ProjectConfig{
				Path: projectRoot,
				Services: map[string]*azdext.ServiceConfig{
					"nightly-summary": service,
				},
			}}

			_, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
			var localErr *azdext.LocalError
			require.ErrorAs(t, err, &localErr)
			assert.Equal(t, exterrors.CodeProjectServiceConflict, localErr.Code)
			assert.Contains(t, localErr.Message, "cannot update routine service")
			assert.Empty(t, client.setPaths)
		})
	}
}

func TestUpsertRoutineServiceUsesFreshRawHost(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "researcher")
	initial := &azdext.ProjectConfig{Path: projectRoot, Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {Name: "nightly-summary", Host: aiRoutineHost},
	}}
	current := &azdext.ProjectConfig{Path: projectRoot, Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {Name: "nightly-summary", Host: "containerapp"},
	}}
	client := &recordingRoutineProjectClient{project: initial, rawProjects: []*azdext.ProjectConfig{current}}

	_, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	require.ErrorContains(t, err, "service name is already used by host \"containerapp\"")
	assert.Empty(t, client.added)
	assert.Empty(t, client.setPaths)
}

func TestUpsertRoutineServiceUsesFreshRawAgentDefinition(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "new-agent")
	initial := &azdext.ProjectConfig{Path: projectRoot, Services: map[string]*azdext.ServiceConfig{
		"agent-service": {
			Name: "agent-service", Host: aiAgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "old-agent"}),
		},
	}}
	current := &azdext.ProjectConfig{Path: projectRoot, Services: map[string]*azdext.ServiceConfig{
		"agent-service": {
			Name: "agent-service", Host: aiAgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "new-agent"}),
		},
	}}
	client := &recordingRoutineProjectClient{project: initial, rawProjects: []*azdext.ProjectConfig{current}}

	result, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.Equal(t, []string{"agent-service"}, requireAddedService(t, client).GetUses())
}

func TestUpsertRoutineServiceRejectsManifestOutsideProject(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("triggers: {}\naction: {}\n"), 0o600))
	client := &recordingRoutineProjectClient{project: &azdext.ProjectConfig{
		Path: projectRoot, Services: map[string]*azdext.ServiceConfig{},
	}}

	_, err := upsertRoutineService(
		t.Context(), client, &routines.Routine{Name: "nightly-summary"}, outside,
	)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeInvalidRoutineManifest, localErr.Code)
	assert.Contains(t, localErr.Message, "must be inside")
	assert.Empty(t, client.added)
}

func TestUpsertRoutineServiceRejectsInvalidServiceNameBeforeReadingProject(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{}
	_, err := upsertRoutineService(
		t.Context(), client, &routines.Routine{Name: "nightly summary"}, "routine.yaml",
	)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	assert.Zero(t, client.getCalls)
}

func TestRoutineAddRequiresFileAndCreateUpdateRemainRemoteOnly(t *testing.T) {
	t.Parallel()

	extensionContext := &azdext.ExtensionContext{}
	addCommand := newRoutineAddCommand(extensionContext)
	fileFlag := addCommand.Flags().Lookup("file")
	require.NotNil(t, fileFlag)
	assert.NotEmpty(t, fileFlag.Annotations[cobra.BashCompOneRequiredFlag])
	assert.Nil(t, newRoutineCreateCommand(extensionContext).Flags().Lookup("add-to-project"))
	assert.Nil(t, newRoutineUpdateCommand(extensionContext).Flags().Lookup("add-to-project"))
}

func TestRoutineAddCommandUsesConfiguredOutputWriter(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	command := newRoutineAddCommandWithDependencies(
		&azdext.ExtensionContext{},
		func(string) (*routines.Routine, error) {
			return &routines.Routine{
				Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
				Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api"},
			}, nil
		},
		func(
			_ context.Context, routine *routines.Routine, _ string,
		) (*routineServiceUpsertResult, error) {
			return &routineServiceUpsertResult{Name: routine.Name, Host: aiRoutineHost, Created: true}, nil
		},
	)
	command.SetOut(&output)
	command.SetArgs([]string{"nightly-summary", "--file", "routine.yaml"})

	require.NoError(t, command.ExecuteContext(t.Context()))
	assert.Equal(t, "Added routine 'nightly-summary' in azure.yaml.\n", output.String())
}

func routineManifestFixture(t *testing.T, agentName string) (string, string, *routines.Routine) {
	t.Helper()
	projectRoot := t.TempDir()
	manifestPath := filepath.Join(projectRoot, "routines", "nightly.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(manifestPath), 0o700))
	require.NoError(t, os.WriteFile(manifestPath, []byte(`triggers:
  default:
    type: schedule
    cron_expression: "0 2 * * *"
action:
  type: invoke_agent_responses_api
  agent_name: `+agentName+"\n"), 0o600))
	return projectRoot, manifestPath, &routines.Routine{
		Name: "nightly-summary",
		Triggers: map[string]routines.RoutineTrigger{
			routines.DefaultTriggerKey: {Type: "schedule", CronExpression: "0 2 * * *"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: agentName},
	}
}

func mustStruct(t *testing.T, values map[string]any) *structpb.Struct {
	t.Helper()
	result, err := structpb.NewStruct(values)
	require.NoError(t, err)
	return result
}

func requireAddedService(t *testing.T, client *recordingRoutineProjectClient) *azdext.ServiceConfig {
	t.Helper()
	require.Len(t, client.added, 1)
	return client.added[0]
}

func TestUpsertRoutineServiceConvergesAfterUpdateFailure(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "new-agent")
	service := &azdext.ServiceConfig{
		Name: "nightly-summary", Host: aiRoutineHost, Uses: []string{"old-agent"},
		AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./routines/old.yaml"}),
	}
	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Path: projectRoot, Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": service,
			"old-agent":       {Name: "old-agent", Host: aiAgentHost},
			"new-agent": {
				Name: "new-agent", Host: aiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind": "hosted", "name": "new-agent",
				}),
			},
		}},
		setErrAt: 2,
	}

	_, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, "./routines/nightly.yaml", service.GetAdditionalProperties().AsMap()["$ref"])
	assert.Equal(t, []string{"old-agent"}, service.GetUses())

	client.setErrAt = 0
	client.setPaths = nil
	result, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	require.NoError(t, err)
	assert.False(t, result.Created)
	assert.Equal(t, []string{"uses"}, client.setPaths)
	assert.Equal(t, []string{"new-agent"}, service.GetUses())
}

func TestPortableRoutineManifestReferenceUsesResolvedProjectPath(t *testing.T) {
	t.Parallel()

	realProject := t.TempDir()
	manifest := filepath.Join(realProject, "routines", "nightly.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(manifest), 0o700))
	require.NoError(t, os.WriteFile(manifest, []byte("{}"), 0o600))
	projectLink := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(realProject, projectLink); err != nil {
		t.Skipf("creating a directory symlink is unavailable: %v", err)
	}

	reference, err := portableRoutineManifestReference(projectLink, manifest)
	require.NoError(t, err)
	assert.Equal(t, "./routines/nightly.yaml", reference)
}

func TestUpsertRoutineServiceRejectsAmbiguousAgentName(t *testing.T) {
	t.Parallel()

	projectRoot, manifestPath, routine := routineManifestFixture(t, "researcher")
	agent := mustStruct(t, map[string]any{"kind": "hosted", "name": "researcher"})
	client := &recordingRoutineProjectClient{project: &azdext.ProjectConfig{
		Path: projectRoot,
		Services: map[string]*azdext.ServiceConfig{
			"researcher-a": {Name: "researcher-a", Host: aiAgentHost, AdditionalProperties: agent},
			"researcher-b": {Name: "researcher-b", Host: aiAgentHost, AdditionalProperties: agent},
		},
	}}

	_, err := upsertRoutineService(t.Context(), client, routine, manifestPath)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeProjectServiceConflict, localErr.Code)
	assert.Contains(t, localErr.Message, "matches multiple services")
	assert.Empty(t, client.added)
}

func TestRoutineAddActionWritesHumanAndJSONOutput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		output     string
		created    bool
		wantOutput string
	}{
		{name: "created", created: true, wantOutput: "Added routine 'nightly-summary' in azure.yaml.\n"},
		{name: "updated", wantOutput: "Updated routine 'nightly-summary' in azure.yaml.\n"},
		{name: "json", output: "json", created: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			action := &routineAddAction{
				flags: &routineAddFlags{name: "nightly-summary", file: "routine.yaml", output: test.output},
				load: func(string) (*routines.Routine, error) {
					return &routines.Routine{
						Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
						Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api"},
					}, nil
				},
				upsert: func(
					_ context.Context, routine *routines.Routine, file string,
				) (*routineServiceUpsertResult, error) {
					assert.Equal(t, "nightly-summary", routine.Name)
					assert.Equal(t, "routine.yaml", file)
					return &routineServiceUpsertResult{
						Name: routine.Name, Host: aiRoutineHost, Ref: "./routine.yaml", Created: test.created,
					}, nil
				},
				writer: &output,
			}

			require.NoError(t, action.Run(t.Context()))
			if test.output != "json" {
				assert.Equal(t, test.wantOutput, output.String())
				return
			}
			var result routineServiceUpsertResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			assert.Equal(t, "./routine.yaml", result.Ref)
			assert.True(t, result.Created)
		})
	}
}

func TestRoutineAddActionRejectsIncompleteManifestBeforeProjectWrite(t *testing.T) {
	t.Parallel()

	upsertCalled := false
	action := &routineAddAction{
		flags: &routineAddFlags{name: "nightly-summary", file: "routine.yaml"},
		load:  func(string) (*routines.Routine, error) { return &routines.Routine{}, nil },
		upsert: func(context.Context, *routines.Routine, string) (*routineServiceUpsertResult, error) {
			upsertCalled = true
			return nil, nil
		},
		writer: &bytes.Buffer{},
	}

	err := action.Run(t.Context())
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeInvalidRoutineManifest, localErr.Code)
	assert.False(t, upsertCalled)
}
