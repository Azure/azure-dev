// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
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
	projects    []*azdext.ProjectConfig
	lastProject *azdext.ProjectConfig
	rawProjects []*azdext.ProjectConfig
	lastRaw     *azdext.ProjectConfig
	getCalls    int
	rawCalls    int
	added       []*azdext.ServiceConfig
	setPaths    []string
	setCalls    int
	addErr      error
	setErr      error
	setErrAt    int
}

func (c *recordingRoutineProjectClient) Get(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	if c.getCalls < len(c.projects) {
		project := c.projects[c.getCalls]
		c.getCalls++
		c.lastProject = project
		return &azdext.GetProjectResponse{Project: project}, nil
	}
	c.getCalls++
	c.lastProject = c.project
	return &azdext.GetProjectResponse{Project: c.project}, nil
}

func (c *recordingRoutineProjectClient) AddService(
	_ context.Context,
	req *azdext.AddServiceRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	c.added = append(c.added, req.GetService())
	return &azdext.EmptyResponse{}, c.addErr
}

func (c *recordingRoutineProjectClient) GetConfigSection(
	_ context.Context,
	_ *azdext.GetProjectConfigSectionRequest,
	_ ...grpc.CallOption,
) (*azdext.GetProjectConfigSectionResponse, error) {
	project := c.lastProject
	if c.rawCalls < len(c.rawProjects) {
		project = c.rawProjects[c.rawCalls]
	}
	c.rawCalls++
	c.lastRaw = project
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

func (c *recordingRoutineProjectClient) SetServiceConfigValue(
	_ context.Context,
	req *azdext.SetServiceConfigValueRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	c.setPaths = append(c.setPaths, req.GetPath())
	c.setCalls++
	if c.setErr != nil && (c.setErrAt == 0 || c.setCalls == c.setErrAt) {
		return nil, c.setErr
	}
	service := c.lastRaw.GetServices()[req.GetServiceName()]
	if req.GetPath() == "uses" {
		service.Uses = nil
		for _, item := range req.GetValue().GetListValue().GetValues() {
			service.Uses = append(service.Uses, item.GetStringValue())
		}
		return &azdext.EmptyResponse{}, nil
	}
	path := req.GetPath()
	if strings.HasPrefix(path, "config.") {
		if service.Config == nil {
			service.Config = &structpb.Struct{Fields: map[string]*structpb.Value{}}
		}
		service.Config.Fields[strings.TrimPrefix(path, "config.")] = req.GetValue()
		return &azdext.EmptyResponse{}, nil
	}
	if service.AdditionalProperties == nil {
		service.AdditionalProperties = &structpb.Struct{Fields: map[string]*structpb.Value{}}
	}
	if service.AdditionalProperties.Fields == nil {
		service.AdditionalProperties.Fields = map[string]*structpb.Value{}
	}
	service.AdditionalProperties.Fields[path] = req.GetValue()
	return &azdext.EmptyResponse{}, nil
}
func TestRoutineProjectAuthorApplyReloadsCurrentUses(t *testing.T) {
	t.Parallel()

	initial := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {
			Name: "nightly-summary",
			Host: aiRoutineHost,
			Uses: []string{"old-connection"},
		},
	}}
	current := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {
			Name: "nightly-summary",
			Host: aiRoutineHost,
			Uses: []string{"new-connection"},
		},
	}}
	client := &recordingRoutineProjectClient{
		projects:    []*azdext.ProjectConfig{initial, initial},
		rawProjects: []*azdext.ProjectConfig{initial, current},
	}
	routine := &routines.Routine{Name: "nightly-summary"}
	author := &routineProjectAuthor{projectClient: client}
	require.NoError(t, author.Prepare(t.Context(), routine))

	err := author.Apply(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, client.getCalls)
	assert.Empty(t, client.added)
	assert.Equal(t, []string{"description"}, client.setPaths)
	assert.Equal(t, []string{"new-connection"}, current.GetServices()["nightly-summary"].GetUses())
}

func TestRoutineProjectAuthorApplyRejectsCurrentHostChange(t *testing.T) {
	t.Parallel()

	initial := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {Name: "nightly-summary", Host: aiRoutineHost},
	}}
	current := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {Name: "nightly-summary", Host: "containerapp"},
	}}
	client := &recordingRoutineProjectClient{
		projects:    []*azdext.ProjectConfig{initial, initial},
		rawProjects: []*azdext.ProjectConfig{initial, current},
	}
	author := &routineProjectAuthor{projectClient: client}
	require.NoError(t, author.Prepare(t.Context(), &routines.Routine{Name: "nightly-summary"}))

	err := author.Apply(t.Context())
	require.ErrorContains(t, err, "service name is already used by host \"containerapp\"")
	assert.Empty(t, client.added)
	assert.Empty(t, client.setPaths)
}

func TestRoutineProjectAuthorApplyReloadsAgentDefinition(t *testing.T) {
	t.Parallel()

	initial := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {Name: "nightly-summary", Host: aiRoutineHost},
		"agent-service": {
			Name: "agent-service", Host: aiAgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "old-agent"}),
		},
	}}
	current := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"nightly-summary": {Name: "nightly-summary", Host: aiRoutineHost},
		"agent-service": {
			Name: "agent-service", Host: aiAgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "new-agent"}),
		},
	}}
	client := &recordingRoutineProjectClient{
		projects:    []*azdext.ProjectConfig{initial, initial},
		rawProjects: []*azdext.ProjectConfig{initial, current},
	}
	author := &routineProjectAuthor{projectClient: client}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "new-agent",
		},
	}
	require.NoError(t, author.Prepare(t.Context(), routine))

	require.NoError(t, author.Apply(t.Context()))
	assert.Contains(t, client.setPaths, "uses")
	assert.Equal(t, []string{"agent-service"}, current.Services["nightly-summary"].GetUses())
}

func TestUpsertRoutineServiceCreatesBlockAndAgentDependency(t *testing.T) {
	t.Parallel()

	agentProperties, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"name": "deployed-researcher",
	})
	require.NoError(t, err)
	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"researcher": {
				Name:                 "researcher",
				Host:                 aiAgentHost,
				AdditionalProperties: agentProperties,
			},
		}},
	}
	routine := &routines.Routine{
		Name:        "nightly-summary",
		Description: "Summarize changes",
		Enabled:     new(true),
		CreatedAt:   "2026-08-24T00:00:00Z",
		UpdatedAt:   "2026-08-24T01:00:00Z",
		Triggers: map[string]routines.RoutineTrigger{
			routines.DefaultTriggerKey: {Type: "schedule", CronExpression: "0 2 * * *"},
		},
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "deployed-researcher",
		},
	}

	err = upsertRoutineService(t.Context(), client, routine)
	require.NoError(t, err)
	require.Len(t, client.added, 1)
	service := client.added[0]
	assert.Equal(t, "nightly-summary", service.GetName())
	assert.Equal(t, aiRoutineHost, service.GetHost())
	properties := service.GetAdditionalProperties().GetFields()
	assert.Equal(t, "Summarize changes", properties["description"].AsInterface())
	assert.NotContains(t, properties, "name")
	assert.NotContains(t, properties, "created_at")
	assert.NotContains(t, properties, "updated_at")
	assert.Equal(t, []string{"researcher"}, service.GetUses())
}

func TestUpsertRoutineServiceUpdatesOwnedFieldsAndPreservesOtherConfiguration(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name:                 "nightly-summary",
				Host:                 aiRoutineHost,
				Uses:                 []string{"connection", "old-agent-service"},
				Environment:          map[string]string{"API_KEY": "${API_KEY}"},
				Config:               mustStruct(t, map[string]any{"legacy": "preserved"}),
				AdditionalProperties: mustStruct(t, map[string]any{"custom": "preserved"}),
			},
			"connection": {Name: "connection", Host: "azure.ai.connection"},
			"old-agent-service": {
				Name: "old-agent-service",
				Host: aiAgentHost,
			},
			"new-agent-service": {
				Name: "new-agent-service",
				Host: aiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind": "hosted",
					"name": "new-agent",
				}),
			},
		}},
	}
	routine := &routines.Routine{
		Name:        "nightly-summary",
		Description: "new description",
		Enabled:     new(true),
		Triggers: map[string]routines.RoutineTrigger{
			routines.DefaultTriggerKey: {Type: "schedule", CronExpression: "0 3 * * *"},
		},
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "new-agent",
		},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.NoError(t, err)
	assert.Empty(t, client.added)
	service := client.project.GetServices()["nightly-summary"]
	properties := service.GetAdditionalProperties().GetFields()
	assert.Equal(t, "preserved", properties["custom"].GetStringValue())
	assert.Equal(t, "new description", properties["description"].AsInterface())
	assert.Contains(t, properties, "enabled")
	assert.Contains(t, properties, "triggers")
	assert.Contains(t, properties, "action")
	assert.Equal(t, "preserved", service.GetConfig().GetFields()["legacy"].GetStringValue())
	assert.Equal(t, map[string]string{"API_KEY": "${API_KEY}"}, service.GetEnvironment())
	assert.Equal(t, []string{"connection", "new-agent-service"}, service.GetUses())
	assert.ElementsMatch(
		t, []string{"description", "enabled", "triggers", "action", "uses"}, client.setPaths,
	)
	parsed, err := parseRoutineServiceConfig(service, "")
	require.NoError(t, err)
	assert.Equal(t, routine.Description, parsed.Description)
	assert.Equal(t, routine.Action.AgentName, parsed.Action.AgentName)
}

func TestUpsertRoutineServiceIdempotentlyUpdatesExistingBlock(t *testing.T) {
	t.Parallel()

	routine := &routines.Routine{
		Name:        "nightly.summary",
		Description: "",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "researcher",
		},
	}
	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly.summary": {
				Name: "nightly.summary",
				Host: aiRoutineHost,
				Uses: []string{"researcher-service"},
			},
			"researcher-service": {
				Name: "researcher-service",
				Host: aiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind": "hosted",
					"name": "researcher",
				}),
			},
		}},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.NoError(t, err)
	assert.Empty(t, client.added)
	first := client.project.GetServices()["nightly.summary"]
	assert.Empty(t, first.GetAdditionalProperties().GetFields()["description"].GetStringValue())
	assert.Contains(t, first.GetAdditionalProperties().GetFields(), "action")
	assert.Equal(t, []string{"researcher-service"}, first.GetUses())

	client.setPaths = nil
	require.NoError(t, upsertRoutineService(t.Context(), client, routine))
	assert.Empty(t, client.setPaths)
}

func TestUpsertRoutineServiceInitializesEmptyAdditionalProperties(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name:                 "nightly-summary",
				Host:                 aiRoutineHost,
				AdditionalProperties: &structpb.Struct{},
			},
		}},
	}

	require.NoError(t, upsertRoutineService(
		t.Context(), client, &routines.Routine{Name: "nightly-summary"},
	))
	assert.Empty(t, client.added)
	properties := client.project.GetServices()["nightly-summary"].GetAdditionalProperties().GetFields()
	assert.Contains(t, properties, "description")
}

func TestUpsertRoutineServiceResolvesAgentNameFromFileRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte("kind: hosted\nname: deployed-researcher\n"),
		0o600,
	))
	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"researcher-service": {
					Name: "researcher-service",
					Host: aiAgentHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"$ref": "./agent.yaml",
					}),
				},
			},
		},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "deployed-researcher",
		},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.NoError(t, err)
	assert.Equal(t, []string{"researcher-service"}, requireAddedService(t, client).GetUses())
}

func TestUpsertRoutineServiceResolvesAgentNameFromLegacyManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	serviceDir := filepath.Join(root, "agents", "researcher")
	require.NoError(t, os.MkdirAll(serviceDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "agent.yaml"),
		[]byte("kind: hosted\nname: deployed-researcher\n"),
		0o600,
	))
	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"researcher-service": {
					Name:         "researcher-service",
					Host:         aiAgentHost,
					RelativePath: "agents/researcher",
				},
			},
		},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "deployed-researcher",
		},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.NoError(t, err)
	assert.Equal(t, []string{"researcher-service"}, requireAddedService(t, client).GetUses())
}

func TestUpsertRoutineServiceInlineAgentDefinitionDoesNotUseLegacyName(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"agent-service": {
				Name:                 "agent-service",
				Host:                 aiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{"name": "stale-inline-name"}),
				Config: mustStruct(t, map[string]any{
					"kind": "hosted",
					"name": "configured-agent",
				}),
			},
		}},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "stale-inline-name",
		},
	}

	require.NoError(t, upsertRoutineService(t.Context(), client, routine))
	assert.Empty(t, requireAddedService(t, client).GetUses())

	client.added = nil
	routine.Action.AgentName = "configured-agent"
	require.NoError(t, upsertRoutineService(t.Context(), client, routine))
	assert.Equal(t, []string{"agent-service"}, requireAddedService(t, client).GetUses())
}

func TestUpsertRoutineServicePreservesAgentDependencyWhenNameIsUnresolved(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name: "nightly-summary",
				Host: aiRoutineHost,
				Uses: []string{"connection", "researcher-service"},
				AdditionalProperties: mustStruct(t, map[string]any{
					"action": map[string]any{
						"type":       "invoke_agent_responses_api",
						"agent_name": "deployed-researcher",
					},
				}),
			},
			"connection": {Name: "connection", Host: "azure.ai.connection"},
			"researcher-service": {
				Name: "researcher-service",
				Host: aiAgentHost,
			},
		}},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "deployed-researcher",
		},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.NoError(t, err)
	assert.Empty(t, client.added)
	assert.NotContains(t, client.setPaths, "uses")
	assert.Equal(t, []string{"connection", "researcher-service"}, client.project.Services["nightly-summary"].GetUses())
}

func TestUpsertRoutineServiceRemovesOldAgentDependencyWhenUnresolvedAgentChanges(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name: "nightly-summary",
				Host: aiRoutineHost,
				Uses: []string{"connection", "researcher-service"},
				AdditionalProperties: mustStruct(t, map[string]any{
					"action": map[string]any{
						"type":       "invoke_agent_responses_api",
						"agent_name": "old-agent",
					},
				}),
			},
			"connection": {Name: "connection", Host: "azure.ai.connection"},
			"researcher-service": {
				Name: "researcher-service",
				Host: aiAgentHost,
			},
		}},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "new-unresolved-agent",
		},
	}

	require.NoError(t, upsertRoutineService(t.Context(), client, routine))
	assert.Contains(t, client.setPaths, "uses")
	assert.Equal(t, []string{"connection"}, client.project.Services["nightly-summary"].GetUses())
}

func TestUpsertRoutineServiceRejectsLegacyAgentPathOutsideProject(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"researcher-service": {
					Name:         "researcher-service",
					Host:         aiAgentHost,
					RelativePath: "../outside",
				},
			},
		},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "deployed-researcher",
		},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.ErrorContains(t, err, "path escapes from parent")
	assert.Empty(t, client.added)
}

func TestUpsertRoutineServiceRemovesAgentDependencyWhenActionHasNoAgentName(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name: "nightly-summary",
				Host: aiRoutineHost,
				Uses: []string{"connection", "researcher-service"},
			},
			"connection": {Name: "connection", Host: "azure.ai.connection"},
			"researcher-service": {
				Name: "researcher-service",
				Host: aiAgentHost,
			},
		}},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:            "invoke_agent_responses_api",
			AgentEndpointID: "agent-endpoint-id",
		},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.NoError(t, err)
	assert.Empty(t, client.added)
	assert.Contains(t, client.setPaths, "uses")
	assert.Equal(t, []string{"connection"}, client.project.Services["nightly-summary"].GetUses())
}

func TestUpsertRoutineServiceRejectsAmbiguousAgentName(t *testing.T) {
	t.Parallel()

	agentProperties := mustStruct(t, map[string]any{"kind": "hosted", "name": "researcher"})
	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"researcher-a": {
				Name:                 "researcher-a",
				Host:                 aiAgentHost,
				AdditionalProperties: agentProperties,
			},
			"researcher-b": {
				Name:                 "researcher-b",
				Host:                 aiAgentHost,
				AdditionalProperties: agentProperties,
			},
		}},
	}
	routine := &routines.Routine{
		Name: "nightly-summary",
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "researcher",
		},
	}

	err := upsertRoutineService(t.Context(), client, routine)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeProjectServiceConflict, localErr.Code)
	assert.Contains(t, localErr.Message, "agent name \"researcher\" matches multiple services")
	assert.Contains(t, localErr.Suggestion, "only one azure.ai.agent service")
	assert.Empty(t, client.added)
}

func TestUpsertRoutineServiceRejectsDifferentHostCollision(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name: "nightly-summary",
				Host: "containerapp",
			},
		}},
	}

	err := upsertRoutineService(t.Context(), client, &routines.Routine{Name: "nightly-summary"})
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeProjectServiceConflict, localErr.Code)
	assert.Contains(t, localErr.Message, "service name is already used by host \"containerapp\"")
	assert.Contains(t, localErr.Suggestion, "rename the routine")
	assert.Empty(t, client.added)
}

func TestUpsertRoutineServiceRejectsInvalidServiceNameBeforeReadingProject(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{}
	err := upsertRoutineService(t.Context(), client, &routines.Routine{Name: "nightly summary"})
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	assert.Contains(t, localErr.Message, "cannot be used as an azure.yaml service name")
	assert.Zero(t, client.getCalls)
	assert.Empty(t, client.added)
	assert.Empty(t, client.setPaths)
}

func TestUpsertRoutineServiceReturnsAddServiceError(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{}},
		addErr:  assert.AnError,
	}

	err := upsertRoutineService(t.Context(), client, &routines.Routine{Name: "nightly-summary"})
	require.ErrorIs(t, err, assert.AnError)
	require.Len(t, client.added, 1)
}

func TestUpsertRoutineServiceReturnsSetServiceConfigValueError(t *testing.T) {
	t.Parallel()

	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name: "nightly-summary",
				Host: aiRoutineHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"description": "old",
				}),
			},
		}},
		setErr: assert.AnError,
	}

	err := upsertRoutineService(
		t.Context(), client,
		&routines.Routine{Name: "nightly-summary", Description: "new"},
	)
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, client.added)
	assert.Equal(t, []string{"description"}, client.setPaths)
}

func TestUpsertRoutineServiceConvergesAfterMidSequenceFailure(t *testing.T) {
	t.Parallel()

	routine := &routines.Routine{
		Name:        "nightly-summary",
		Description: "new description",
		Enabled:     new(true),
		Triggers: map[string]routines.RoutineTrigger{
			routines.DefaultTriggerKey: {Type: "schedule", CronExpression: "0 2 * * *"},
		},
		Action: &routines.RoutineAction{
			Type:      "invoke_agent_responses_api",
			AgentName: "researcher",
		},
	}
	client := &recordingRoutineProjectClient{
		project: &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
			"nightly-summary": {
				Name:   "nightly-summary",
				Host:   aiRoutineHost,
				Config: mustStruct(t, map[string]any{"description": "legacy"}),
			},
		}},
		setErr:   assert.AnError,
		setErrAt: 3,
	}

	err := upsertRoutineService(t.Context(), client, routine)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, []string{"config.description", "config.enabled", "config.triggers"}, client.setPaths)

	client.setErr = nil
	client.setPaths = nil
	require.NoError(t, upsertRoutineService(t.Context(), client, routine))
	assert.Equal(t, []string{"config.triggers", "config.action"}, client.setPaths)

	expected, err := routineServiceProperties(routine)
	require.NoError(t, err)
	actual := client.project.Services["nightly-summary"].GetConfig()
	assert.Equal(t, expected.AsMap(), actual.AsMap())
}

func TestRoutineProjectAuthoringRetryQuotesName(t *testing.T) {
	t.Parallel()

	assert.Contains(t, routineProjectAuthoringRetry("nightly summary"), `update "nightly summary"`)
}

func TestRoutineCommandsRegisterAddToProjectFlag(t *testing.T) {
	t.Parallel()

	extensionContext := &azdext.ExtensionContext{}
	for _, command := range []*cobra.Command{
		newRoutineCreateCommand(extensionContext),
		newRoutineUpdateCommand(extensionContext),
	} {
		flag := command.Flags().Lookup("add-to-project")
		require.NotNil(t, flag)
		assert.Equal(t, "false", flag.DefValue)
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
