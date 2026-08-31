// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path"
	"path/filepath"
	"sync"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func mustMarshalConfig[T any](t *testing.T, in *T) *azdext.ServiceConfig {
	t.Helper()
	cfg, err := project.MarshalStruct(in)
	require.NoError(t, err)
	return &azdext.ServiceConfig{Config: cfg}
}

func projectService(t *testing.T, name string, deployments ...project.Deployment) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &project.ServiceTargetAgentConfig{Deployments: deployments})
	svc.Name = name
	svc.Host = AiProjectHost
	return svc
}

func connectionService(t *testing.T, name string, conn project.Connection) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &conn)
	svc.Name = name
	svc.Host = AiConnectionHost
	return svc
}

func toolboxService(t *testing.T, name string, toolbox project.Toolbox) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &toolbox)
	svc.Name = name
	svc.Host = AiToolboxHost
	return svc
}

func agentService(t *testing.T, name string, toolConnections ...project.ToolConnection) *azdext.ServiceConfig {
	t.Helper()
	svc := mustMarshalConfig(t, &project.ServiceTargetAgentConfig{ToolConnections: toolConnections})
	svc.Name = name
	svc.Host = AiAgentHost
	return svc
}

// TestSanitizeServiceName verifies resource names are normalized into valid
// azure.yaml service keys (spaces removed, surrounding whitespace trimmed).
func TestSanitizeServiceName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "MyAgent", sanitizeServiceName("  My Agent  "))
	assert.Equal(t, "gpt4o", sanitizeServiceName("gpt 4 o"))
	assert.Equal(t, "", sanitizeServiceName("   "))
}

// TestReserveServiceName verifies distinct service keys are accepted and that
// two resources collapsing to the same azure.yaml key (e.g. "my conn" and
// "myconn") fail fast with an actionable collision error instead of silently
// overwriting each other.
func TestReserveServiceName(t *testing.T) {
	t.Parallel()

	used := map[string]string{"weatheragent": "agent service"}
	require.NoError(t, reserveServiceName(used, "myconn", `connection "my conn"`))
	require.NoError(t, reserveServiceName(used, "toolbox", `toolbox "toolbox"`))

	err := reserveServiceName(used, "myconn", `connection "myconn"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collision")
	assert.Contains(t, err.Error(), "myconn")

	// A resource colliding with the seeded agent service is also caught.
	err = reserveServiceName(used, "weatheragent", `connection "weather agent"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent service")
}

func TestServiceEnvironmentTemplates(t *testing.T) {
	t.Parallel()

	cfg, err := project.MarshalStruct(&project.Connection{
		Credentials: map[string]any{
			"key": "${SEARCH_KEY}",
		},
		Metadata: map[string]string{
			"server":          "${SERVER_NAME}",
			"default":         "${DEFAULT_NAME:-fallback}",
			"foundry_default": "${EVENT_BODY:-${{event.body}}}",
			"token":           "${{connections.search.credentials.key}}",
			"literal":         "$${LITERAL}",
		},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"SEARCH_KEY":   "${SEARCH_KEY}",
		"SERVER_NAME":  "${SERVER_NAME}",
		"DEFAULT_NAME": "${DEFAULT_NAME}",
		"EVENT_BODY":   "${EVENT_BODY}",
	}, serviceEnvironmentTemplates(cfg))
}

// TestServiceEnvironmentTemplatesDeterministic verifies a var
// referenced both bare and with a default collapses to the same
// canonical ${VAR}, so field order cannot change the result.
func TestServiceEnvironmentTemplatesDeterministic(t *testing.T) {
	t.Parallel()

	cfg, err := project.MarshalStruct(&project.Connection{
		Metadata: map[string]string{
			"bare":     "${TOPIC}",
			"default":  "${TOPIC:-general}",
			"default2": "${TOPIC:-other}",
		},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"TOPIC": "${TOPIC}",
	}, serviceEnvironmentTemplates(cfg))
}

func TestEscapeFoundryTemplates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"foundry span", "${{event.body}}", "$${{event.body}}"},
		{"already escaped", "$${{event.body}}", "$${{event.body}}"},
		{"single brace untouched", "${VAR}", "${VAR}"},
		{"literal untouched", "info", "info"},
		{
			"embedded span",
			"prefix-${{connections.x.key}}-suffix",
			"prefix-$${{connections.x.key}}-suffix",
		},
		{
			"span in default",
			"${MISSING:-${{event.body}}}",
			"${MISSING:-$${{event.body}}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, escapeFoundryTemplates(tt.value))
		})
	}
}

func TestAddResourceServiceWritesEnvironment(t *testing.T) {
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	cfg, err := project.MarshalStruct(&project.Connection{
		Credentials: map[string]any{"key": "${SEARCH_KEY}"},
	})
	require.NoError(t, err)

	require.NoError(t, addResourceService(
		t.Context(),
		client,
		"search",
		AiConnectionHost,
		cfg,
		nil,
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Len(t, server.added, 1)
	assert.Empty(t, server.added[0].GetEnvironment())
	assert.Equal(t, map[string]any{
		"SEARCH_KEY": "${SEARCH_KEY}",
	}, server.env["search"])
}

func TestCollectLegacyProjectDeploymentsIgnoresSplitProject(
	t *testing.T,
) {
	t.Parallel()

	dep := project.Deployment{Name: "gpt-4o", Model: project.DeploymentModel{Name: "gpt-4o"}}
	services := map[string]*azdext.ServiceConfig{
		"ai-project": projectService(t, "ai-project", dep),
		"agent":      agentService(t, "agent"),
		"conn":       connectionService(t, "conn", project.Connection{Name: "conn"}),
	}

	deployments, err := collectLegacyProjectDeployments(services, "")
	require.NoError(t, err)
	assert.Empty(t, deployments)
}

// TestCollectConnections verifies connections are sourced from
// azure.ai.connection services in deterministic (sorted) order.
func TestCollectConnections(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"zeta":       connectionService(t, "zeta", project.Connection{Name: "zeta", Category: "ApiKey"}),
		"alpha":      connectionService(t, "alpha", project.Connection{Name: "alpha", Category: "ApiKey"}),
		"ai-project": projectService(t, "ai-project"),
		"agent":      agentService(t, "agent"),
	}

	connections, err := collectConnections(services, "")
	require.NoError(t, err)
	require.Len(t, connections, 2)
	// Sorted by service key (alpha before zeta) for stable env-var output.
	assert.Equal(t, "alpha", connections[0].Name)
	assert.Equal(t, "zeta", connections[1].Name)
}

func TestCollectConnections_UsesAgentConfigPrecedence(t *testing.T) {
	t.Parallel()

	inline, err := structpb.NewStruct(map[string]any{
		"connections": []any{
			map[string]any{
				"name":     "inline-connection",
				"category": "ApiKey",
				"target":   "https://inline.example",
			},
		},
	})
	require.NoError(t, err)
	legacy, err := structpb.NewStruct(map[string]any{
		"kind": "hostedAgent",
		"connections": []any{
			map[string]any{
				"name":     "legacy-connection",
				"category": "ApiKey",
				"target":   "https://legacy.example",
			},
		},
	})
	require.NoError(t, err)

	services := map[string]*azdext.ServiceConfig{
		"agent": {
			Name:                 "agent",
			Host:                 AiAgentHost,
			AdditionalProperties: inline,
			Config:               legacy,
		},
	}

	connections, err := collectConnections(services, "")
	require.NoError(t, err)
	require.Len(t, connections, 1)
	assert.Equal(t, "legacy-connection", connections[0].Name)
}

func TestCollectConnections_UsesResolvedInlineConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte(
			"kind: hostedAgent\n"+
				"connections:\n"+
				"  - name: inline-connection\n"+
				"    category: ApiKey\n"+
				"    target: https://inline.example\n",
		),
		0o600,
	))
	inline, err := structpb.NewStruct(map[string]any{
		"$ref": "./agent.yaml",
	})
	require.NoError(t, err)
	legacy, err := structpb.NewStruct(map[string]any{
		"kind": "hostedAgent",
		"connections": []any{
			map[string]any{
				"name":     "legacy-connection",
				"category": "ApiKey",
				"target":   "https://legacy.example",
			},
		},
	})
	require.NoError(t, err)

	services := map[string]*azdext.ServiceConfig{
		"agent": {
			Name:                 "agent",
			Host:                 AiAgentHost,
			AdditionalProperties: inline,
			Config:               legacy,
		},
	}

	connections, err := collectConnections(services, root)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	assert.Equal(t, "inline-connection", connections[0].Name)
}

// TestCollectToolboxes verifies toolboxes are sourced from azure.ai.toolbox
// services only.
func TestCollectToolboxes(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"tb":    toolboxService(t, "tb", project.Toolbox{Name: "tb", Tools: []map[string]any{{"type": "mcp"}}}),
		"agent": agentService(t, "agent"),
	}

	toolboxes, err := collectToolboxes(services, "")
	require.NoError(t, err)
	require.Len(t, toolboxes, 1)
	assert.Equal(t, "tb", toolboxes[0].Name)
	require.Len(t, toolboxes[0].Tools, 1)
}

func TestCollectResourceServices_ResolvesFileRefs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "deployment.yaml"),
		[]byte(
			"name: gpt-4o\n"+
				"model: {name: gpt-4o}\n",
		),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "project.yaml"),
		[]byte(
			"deployments:\n"+
				"  - $ref: ./deployment.yaml\n",
		),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "connection.yaml"),
		[]byte(
			"category: ApiKey\n"+
				"target: https://example.test\n",
		),
		0o600,
	))
	projectProps, err := structpb.NewStruct(map[string]any{
		"$ref": "./project.yaml",
	})
	require.NoError(t, err)
	connectionProps, err := structpb.NewStruct(map[string]any{
		"$ref": "./connection.yaml",
	})
	require.NoError(t, err)
	services := map[string]*azdext.ServiceConfig{
		"ai-project": {
			Name:                 "ai-project",
			Host:                 AiProjectHost,
			AdditionalProperties: projectProps,
		},
		"search": {
			Name:                 "search",
			Host:                 AiConnectionHost,
			AdditionalProperties: connectionProps,
		},
	}

	deployments, err := collectLegacyProjectDeployments(
		services,
		root,
	)
	require.NoError(t, err)
	assert.Empty(t, deployments)

	connections, err := collectConnections(services, root)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	assert.Equal(t, "search", connections[0].Name)
	assert.Equal(t, "ApiKey", connections[0].Category)
}

// TestCollectAgentToolConnections verifies tool connections stay on the agent
// service and are sourced from there for toolbox enrichment.
func TestCollectAgentToolConnections(t *testing.T) {
	t.Parallel()

	tc := project.ToolConnection{Name: "mcp-conn", Category: "CustomKeys", Target: "https://example.com"}
	services := map[string]*azdext.ServiceConfig{
		"agent":      agentService(t, "agent", tc),
		"ai-project": projectService(t, "ai-project"),
	}

	toolConnections, err := collectAgentToolConnections(services, "")
	require.NoError(t, err)
	require.Len(t, toolConnections, 1)
	assert.Equal(t, "mcp-conn", toolConnections[0].Name)
}

// TestCollectHelpers_EmptyAndNilConfigs verifies the collectors tolerate
// services with nil config and unrelated hosts without error.
func TestCollectHelpers_EmptyAndNilConfigs(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"web":    {Name: "web", Host: "containerapp"},
		"nilcfg": {Name: "nilcfg", Host: AiProjectHost},
	}

	deployments, err := collectLegacyProjectDeployments(services, "")
	require.NoError(t, err)
	assert.Empty(t, deployments)

	connections, err := collectConnections(services, "")
	require.NoError(t, err)
	assert.Empty(t, connections)

	toolboxes, err := collectToolboxes(services, "")
	require.NoError(t, err)
	assert.Empty(t, toolboxes)
}

// TestCollect_FallbackToBundledAgentConfig verifies that a pre-split azure.yaml
// -- deployments, connections, and toolboxes bundled on the agent service with
// no sibling azure.ai.<kind> services -- still yields those resources, so
// existing projects provision without re-running init.
func TestCollect_FallbackToBundledAgentConfig(t *testing.T) {
	t.Parallel()

	bundled := &project.ServiceTargetAgentConfig{
		Deployments: []project.Deployment{{Name: "gpt-4o", Model: project.DeploymentModel{Name: "gpt-4o"}}},
		Connections: []project.Connection{{Name: "conn", Category: "ApiKey"}},
		Toolboxes:   []project.Toolbox{{Name: "tb", Tools: []map[string]any{{"type": "mcp"}}}},
	}
	svc := mustMarshalConfig(t, bundled)
	svc.Name = "my-agent"
	svc.Host = AiAgentHost
	services := map[string]*azdext.ServiceConfig{"my-agent": svc}

	deployments, err := collectLegacyProjectDeployments(services, "")
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)

	connections, err := collectConnections(services, "")
	require.NoError(t, err)
	require.Len(t, connections, 1)
	assert.Equal(t, "conn", connections[0].Name)

	toolboxes, err := collectToolboxes(services, "")
	require.NoError(t, err)
	require.Len(t, toolboxes, 1)
	assert.Equal(t, "tb", toolboxes[0].Name)
}

func TestCollectLegacyProjectDeploymentsSplitDisablesFallback(
	t *testing.T,
) {
	t.Parallel()

	bundled := &project.ServiceTargetAgentConfig{
		Deployments: []project.Deployment{{Name: "legacy", Model: project.DeploymentModel{Name: "legacy"}}},
	}
	agentSvc := mustMarshalConfig(t, bundled)
	agentSvc.Name = "my-agent"
	agentSvc.Host = AiAgentHost

	services := map[string]*azdext.ServiceConfig{
		"my-agent": agentSvc,
		"ai-project": projectService(
			t, "ai-project", project.Deployment{Name: "gpt-4o", Model: project.DeploymentModel{Name: "gpt-4o"}},
		),
	}

	deployments, err := collectLegacyProjectDeployments(services, "")
	require.NoError(t, err)
	assert.Empty(t, deployments)
}

// recordingProjectServer captures the AddService and SetServiceConfigValue
// calls emitResourceServices makes, so tests can assert on the emitted
// azure.yaml service graph without a real azd host.
type recordingProjectServer struct {
	azdext.UnimplementedProjectServiceServer

	mu    sync.Mutex
	added []*azdext.ServiceConfig
	uses  map[string][]string
	env   map[string]map[string]any
	// configValues records non-"uses" SetServiceConfigValue calls keyed by path.
	configValues map[string]configValueRecord
	// existing is returned by Get to simulate services already present in the
	// project (e.g. a prior init's azure.ai.project service).
	existing map[string]*azdext.ServiceConfig
	// rawEnv is returned by GetServiceConfigValue for path "env" to
	// simulate a service that already carries an env section (raw,
	// on-disk templates).
	rawEnv                map[string]map[string]any
	unsetPaths            []string
	setEnvironmentErr     error
	unsetServiceConfigErr error
}

// configValueRecord captures a single SetServiceConfigValue call.
type configValueRecord struct {
	serviceName string
	value       string
}

func (s *recordingProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &azdext.GetProjectResponse{
		Project: &azdext.ProjectConfig{Services: s.existing},
	}, nil
}

func (s *recordingProjectServer) AddService(
	_ context.Context, req *azdext.AddServiceRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.added = append(s.added, req.Service)
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServer) GetServiceConfigValue(
	_ context.Context, req *azdext.GetServiceConfigValueRequest,
) (*azdext.GetServiceConfigValueResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Path == "env" {
		if raw, ok := s.rawEnv[req.ServiceName]; ok {
			value, err := structpb.NewValue(raw)
			if err != nil {
				return nil, err
			}
			return &azdext.GetServiceConfigValueResponse{
				Found: true,
				Value: value,
			}, nil
		}
	}
	return &azdext.GetServiceConfigValueResponse{}, nil
}

func (s *recordingProjectServer) SetServiceConfigValue(
	_ context.Context, req *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uses == nil {
		s.uses = map[string][]string{}
	}
	if s.configValues == nil {
		s.configValues = map[string]configValueRecord{}
	}
	if req.Path == "uses" && req.Value != nil {
		if list, ok := req.Value.AsInterface().([]any); ok {
			vals := make([]string, 0, len(list))
			for _, v := range list {
				if str, ok := v.(string); ok {
					vals = append(vals, str)
				}
			}
			s.uses[req.ServiceName] = vals
		}
	} else if req.Value != nil {
		if str, ok := req.Value.AsInterface().(string); ok {
			s.configValues[req.Path] = configValueRecord{
				serviceName: req.ServiceName,
				value:       str,
			}
		}
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServer) SetServiceConfigSection(
	_ context.Context,
	req *azdext.SetServiceConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setEnvironmentErr != nil {
		return nil, s.setEnvironmentErr
	}
	if s.env == nil {
		s.env = map[string]map[string]any{}
	}
	if req.Path == "env" && req.Section != nil {
		s.env[req.ServiceName] = req.Section.AsMap()
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServer) UnsetServiceConfig(
	_ context.Context,
	req *azdext.UnsetServiceConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsetPaths = append(s.unsetPaths, req.Path)
	if s.unsetServiceConfigErr != nil {
		return nil, s.unsetServiceConfigErr
	}
	return &azdext.EmptyResponse{}, nil
}

// newProjectRecorderClient spins up an in-process gRPC server backed by the
// supplied project server stub and returns a client wired to its address.
func newProjectRecorderClient(
	t *testing.T,
	server azdext.ProjectServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, server)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	serveErr := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			serveErr <- err
		}
	}()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
		select {
		case err := <-serveErr:
			require.ErrorIs(t, err, grpc.ErrServerStopped)
		default:
		}
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

// TestEmitResourceServices_AlwaysEmitsProjectService verifies the ai-project
// service is written even when the agent has no deployments, connections, or
// toolboxes, and that the agent's uses: is wired to it. The project service is
// emitted unconditionally as the stable provisioning-order anchor every agent
// references rather than being gated on a Foundry resource being present.
func TestEmitResourceServices_AlwaysEmitsProjectService(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	_, err := emitResourceServices(t.Context(), client, "myagent", "", foundryResources{})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	require.Len(t, server.added, 1)
	assert.Equal(t, aiProjectServiceName, server.added[0].Name)
	assert.Equal(t, AiProjectHost, server.added[0].Host)
	assert.Equal(t, []string{aiProjectServiceName}, server.uses["myagent"])
}

// TestPromptResourceServices covers the translation from a prompt agent's own
// definition and folder layout into the sibling services init writes, which is
// what gives prompt agents the same host coverage hosted agents already have.
func TestPromptResourceServices(t *testing.T) {
	t.Parallel()

	t.Run("connections and skill bundles become siblings", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		bundle := filepath.Join(dir, "skills", "code-review")
		require.NoError(t, os.MkdirAll(bundle, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(bundle, "SKILL.md"), []byte(
			"---\nname: code-review\ndescription: reviews code\n---\n\nDo the review.\n",
		), 0o600))

		client := newProjectRecorderClient(t, &recordingProjectServer{})
		agent := &agent_yaml.PromptAgent{
			Connections: []string{"search"},
		}

		got, err := promptResourceServices(t.Context(), client, agent, dir)
		require.NoError(t, err)

		require.Empty(t, got.Connections)

		// archive: points at the bundle folder so the whole bundle -- scripts and
		// references included -- travels with the instructions.
		require.Len(t, got.Skills, 1)
		skill, ok := got.Skills["code-review"]
		require.True(t, ok, "the skill is keyed by the name SKILL.md declares")
		assert.Equal(t, "reviews code", skill.Description)
		assert.Equal(t, "./"+path.Join(filepath.ToSlash(dir), "skills/code-review"), skill.Archive)
	})

	t.Run("toolbox reference is only wired when the service exists", func(t *testing.T) {
		t.Parallel()

		agent := &agent_yaml.PromptAgent{Toolbox: &agent_yaml.ToolboxReference{Name: "my-toolbox"}}

		// A dangling uses: entry would fail the project load, so a toolbox with
		// no service in azure.yaml contributes no edge at all.
		client := newProjectRecorderClient(t, &recordingProjectServer{})
		got, err := promptResourceServices(t.Context(), client, agent, t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, got.ExtraUses)

		withToolbox := newProjectRecorderClient(t, &recordingProjectServer{
			existing: map[string]*azdext.ServiceConfig{
				"my-toolbox": {Name: "my-toolbox", Host: AiToolboxHost},
			},
		})
		got, err = promptResourceServices(t.Context(), withToolbox, agent, t.TempDir())
		require.NoError(t, err)
		assert.Equal(t, []string{"my-toolbox"}, got.ExtraUses)
	})
}

// TestEmitResourceServices_EmitsSkillServices verifies each skill bundle is
// written as its own azure.ai.skill service pointing at the bundle folder, and
// that the agent uses: it. Creating and versioning the skill belongs to the
// azure.ai.skills extension; the agents extension only attaches the version it
// publishes, so without this service nothing ever creates the skill.
func TestEmitResourceServices_EmitsSkillServices(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	_, err := emitResourceServices(t.Context(), client, "myagent", "", foundryResources{
		Skills: map[string]project.SkillService{
			"code-review": {Description: "reviews code", Archive: "./skills/code-review"},
		},
	})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	var skillSvc *azdext.ServiceConfig
	for _, svc := range server.added {
		if svc.Host == AiSkillHost {
			skillSvc = svc
		}
	}
	require.NotNil(t, skillSvc, "a skill bundle must produce an azure.ai.skill service")
	// The service key is the skill name; the skills extension has no name field.
	assert.Equal(t, "code-review", skillSvc.Name)
	require.NotNil(t, skillSvc.AdditionalProperties)
	assert.Equal(t,
		"./skills/code-review",
		skillSvc.AdditionalProperties.Fields["archive"].GetStringValue(),
	)
	// The skill must deploy before the agent that pins its version.
	assert.Contains(t, server.uses["myagent"], "code-review")
	assert.Equal(t, []string{aiProjectServiceName}, server.uses["code-review"])
}

// TestEmitResourceServices_ExtraUsesAreWired verifies a name passed as ExtraUses
// joins the agent's uses: without a service being emitted for it. A prompt
// agent's toolbox: references a toolbox someone else defines, so there is
// nothing to write, but the ordering edge is still required.
func TestEmitResourceServices_ExtraUsesAreWired(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	_, err := emitResourceServices(t.Context(), client, "myagent", "", foundryResources{
		ExtraUses: []string{"my-toolbox"},
	})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	assert.Equal(t, []string{aiProjectServiceName, "my-toolbox"}, server.uses["myagent"])
	for _, svc := range server.added {
		assert.NotEqual(t, "my-toolbox", svc.Name, "ExtraUses must not emit a service")
	}
}

// TestEmitResourceServices_WiresSiblingsToProject verifies a connection service
// is emitted alongside the project service, depends on it via uses: so the
// project provisions first, and that the agent is wired to both siblings.
func TestEmitResourceServices_WiresSiblingsToProject(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	conns := []project.Connection{{Name: "myconn", Category: "ApiKey"}}
	_, err := emitResourceServices(t.Context(), client, "myagent", "", foundryResources{Connections: conns})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	require.Len(t, server.added, 2)
	assert.Equal(t, aiProjectServiceName, server.added[0].Name)
	assert.Equal(t, AiProjectHost, server.added[0].Host)
	assert.Equal(t, "myconn", server.added[1].Name)
	assert.Equal(t, AiConnectionHost, server.added[1].Host)

	assert.Equal(t, []string{aiProjectServiceName}, server.uses["myconn"])
	assert.Equal(t, []string{aiProjectServiceName, "myconn"}, server.uses["myagent"])
}

func TestEmitResourceServices_CountsEmittedConnections(t *testing.T) {
	t.Parallel()

	t.Run("valid connection returns 1", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)
		conns := []project.Connection{{Name: "myconn", Category: "ApiKey"}}

		got, err := emitResourceServices(
			t.Context(), client, "myagent", "", foundryResources{Connections: conns})
		require.NoError(t, err)
		assert.Equal(t, 1, got)
	})

	t.Run("blank name returns 0", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)
		conns := []project.Connection{{Name: "   ", Category: "ApiKey"}}

		got, err := emitResourceServices(
			t.Context(), client, "myagent", "", foundryResources{Connections: conns})
		require.NoError(t, err)
		assert.Equal(t, 0, got)

		server.mu.Lock()
		defer server.mu.Unlock()
		for _, svc := range server.added {
			assert.NotEqual(t, AiConnectionHost, svc.Host)
		}
	})
}

// TestEmitResourceServices_WritesServiceLevelProps verifies resource services are
// written with their keys composed at the service level (inline via
// AdditionalProperties, matching the agent service shape and the config:false
// host schema conditionals) rather than nested under config:, and that the
// collectors read that service-level shape back.
func TestEmitResourceServices_WritesServiceLevelProps(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	deployments := []project.Deployment{{
		Name:  "gpt-4.1-mini",
		Model: project.DeploymentModel{Format: "OpenAI", Name: "gpt-4.1-mini", Version: "2025-04-14"},
		Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}}
	conns := []project.Connection{{Name: "myconn", Category: "ApiKey", Target: "https://example", AuthType: "ApiKey"}}
	_, err := emitResourceServices(
		t.Context(), client, "myagent", "", foundryResources{Deployments: deployments, Connections: conns})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	services := map[string]*azdext.ServiceConfig{}
	for _, svc := range server.added {
		// Resource keys must travel at the service level, not under config:.
		assert.Nil(t, svc.Config, "service %q must not nest keys under config:", svc.Name)
		assert.NotNil(t, svc.AdditionalProperties, "service %q must carry service-level keys", svc.Name)
		services[svc.Name] = svc
	}

	// Init must write a project shape the owning extension can parse.
	var projectCfg project.ServiceTargetAgentConfig
	err = project.UnmarshalStruct(
		project.ServiceConfigProps(services["ai-project"]),
		&projectCfg,
	)
	require.NoError(t, err)
	require.Len(t, projectCfg.Deployments, 1)
	assert.Equal(t, "gpt-4.1-mini", projectCfg.Deployments[0].Name)

	gotConns, err := collectConnections(services, "")
	require.NoError(t, err)
	require.Len(t, gotConns, 1)
	assert.Equal(t, "myconn", gotConns[0].Name)
	assert.Equal(t, "ApiKey", gotConns[0].Category)
}

// TestEmitResourceServices_WritesEndpointForExistingProject verifies that a
// non-empty projectEndpoint is written as endpoint: on the ai-project service
// (the brownfield signal provision reads to reuse the project) and that an
// empty endpoint (new project) leaves the field unset. Callers pass the
// portable ${FOUNDRY_PROJECT_ENDPOINT} reference, never a literal URL.
func TestEmitResourceServices_WritesEndpointForExistingProject(t *testing.T) {
	t.Parallel()

	t.Run("existing project writes endpoint", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		_, err := emitResourceServices(
			t.Context(), client, "myagent", projectEndpointRef, foundryResources{})
		require.NoError(t, err)

		server.mu.Lock()
		defer server.mu.Unlock()

		require.Len(t, server.added, 1)
		projSvc := server.added[0]
		require.Equal(t, aiProjectServiceName, projSvc.Name)
		require.NotNil(t, projSvc.AdditionalProperties)
		assert.Equal(t,
			"${FOUNDRY_PROJECT_ENDPOINT}",
			projSvc.AdditionalProperties.Fields["endpoint"].GetStringValue(),
		)
		require.NotContains(t, server.env, aiProjectServiceName,
			"the project endpoint must resolve from the azd environment, not a self-referential service env")
	})

	t.Run("new project omits endpoint", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		_, err := emitResourceServices(t.Context(), client, "myagent", "", foundryResources{})
		require.NoError(t, err)

		server.mu.Lock()
		defer server.mu.Unlock()

		require.Len(t, server.added, 1)
		projSvc := server.added[0]
		if projSvc.AdditionalProperties != nil {
			_, ok := projSvc.AdditionalProperties.Fields["endpoint"]
			assert.False(t, ok, "endpoint must be omitted for a new project")
		}
	})
}

// TestEmitResourceServices_ProjectServiceKey verifies how the azure.ai.project
// service key is resolved: reuse an existing key, else the generic "ai-project".
// The key is never derived from the Foundry project name -- azure.yaml must not
// carry tenant-specific identifiers.
func TestEmitResourceServices_ProjectServiceKey(t *testing.T) {
	t.Parallel()

	t.Run("uses the generic key for a new project", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		_, err := emitResourceServices(t.Context(), client, "myagent", "", foundryResources{})
		require.NoError(t, err)

		server.mu.Lock()
		defer server.mu.Unlock()
		require.Len(t, server.added, 1)
		assert.Equal(t, aiProjectServiceName, server.added[0].Name)
		assert.Equal(t, []string{aiProjectServiceName}, server.uses["myagent"])
	})

	t.Run("reuses existing project service key", func(t *testing.T) {
		server := &recordingProjectServer{
			existing: map[string]*azdext.ServiceConfig{
				"old-project-key": {Name: "old-project-key", Host: AiProjectHost},
			},
		}
		client := newProjectRecorderClient(t, server)

		// The existing key wins so a repeated init does not create a second
		// project service.
		_, err := emitResourceServices(t.Context(), client, "myagent", "", foundryResources{})
		require.NoError(t, err)

		server.mu.Lock()
		defer server.mu.Unlock()
		require.Len(t, server.added, 1)
		assert.Equal(t, "old-project-key", server.added[0].Name)
	})
}
