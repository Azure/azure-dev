// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"sync"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
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

// TestCollectProjectDeployments verifies deployments are sourced only from
// azure.ai.project services and ignore sibling hosts.
func TestCollectProjectDeployments(t *testing.T) {
	t.Parallel()

	dep := project.Deployment{Name: "gpt-4o", Model: project.DeploymentModel{Name: "gpt-4o"}}
	services := map[string]*azdext.ServiceConfig{
		"ai-project": projectService(t, "ai-project", dep),
		"agent":      agentService(t, "agent"),
		"conn":       connectionService(t, "conn", project.Connection{Name: "conn"}),
	}

	deployments, err := collectProjectDeployments(services)
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)
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

	connections, err := collectConnections(services)
	require.NoError(t, err)
	require.Len(t, connections, 2)
	// Sorted by service key (alpha before zeta) for stable env-var output.
	assert.Equal(t, "alpha", connections[0].Name)
	assert.Equal(t, "zeta", connections[1].Name)
}

// TestCollectToolboxes verifies toolboxes are sourced from azure.ai.toolbox
// services only.
func TestCollectToolboxes(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"tb":    toolboxService(t, "tb", project.Toolbox{Name: "tb", Tools: []map[string]any{{"type": "mcp"}}}),
		"agent": agentService(t, "agent"),
	}

	toolboxes, err := collectToolboxes(services)
	require.NoError(t, err)
	require.Len(t, toolboxes, 1)
	assert.Equal(t, "tb", toolboxes[0].Name)
	require.Len(t, toolboxes[0].Tools, 1)
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

	toolConnections, err := collectAgentToolConnections(services)
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

	deployments, err := collectProjectDeployments(services)
	require.NoError(t, err)
	assert.Empty(t, deployments)

	connections, err := collectConnections(services)
	require.NoError(t, err)
	assert.Empty(t, connections)

	toolboxes, err := collectToolboxes(services)
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

	deployments, err := collectProjectDeployments(services)
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)

	connections, err := collectConnections(services)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	assert.Equal(t, "conn", connections[0].Name)

	toolboxes, err := collectToolboxes(services)
	require.NoError(t, err)
	require.Len(t, toolboxes, 1)
	assert.Equal(t, "tb", toolboxes[0].Name)
}

// TestCollectProjectDeployments_SiblingWinsOverBundled verifies the sibling
// azure.ai.project service takes precedence: the fallback to bundled agent
// deployments only applies when no project service carries any.
func TestCollectProjectDeployments_SiblingWinsOverBundled(t *testing.T) {
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

	deployments, err := collectProjectDeployments(services)
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "gpt-4o", deployments[0].Name)
}

// recordingProjectServer captures the AddService and SetServiceConfigValue
// calls emitResourceServices makes, so tests can assert on the emitted
// azure.yaml service graph without a real azd host.
type recordingProjectServer struct {
	azdext.UnimplementedProjectServiceServer

	mu    sync.Mutex
	added []*azdext.ServiceConfig
	uses  map[string][]string
}

func (s *recordingProjectServer) AddService(
	_ context.Context, req *azdext.AddServiceRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.added = append(s.added, req.Service)
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServer) SetServiceConfigValue(
	_ context.Context, req *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uses == nil {
		s.uses = map[string][]string{}
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
	}
	return &azdext.EmptyResponse{}, nil
}

// newProjectRecorderClient spins up an in-process gRPC server backed by the
// supplied project server stub and returns a client wired to its address.
func newProjectRecorderClient(t *testing.T, server azdext.ProjectServiceServer) *azdext.AzdClient {
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

	err := emitResourceServices(t.Context(), client, "myagent", "", nil, nil, nil)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	require.Len(t, server.added, 1)
	assert.Equal(t, aiProjectServiceName, server.added[0].Name)
	assert.Equal(t, AiProjectHost, server.added[0].Host)
	assert.Equal(t, []string{aiProjectServiceName}, server.uses["myagent"])
}

// TestEmitResourceServices_WiresSiblingsToProject verifies a connection service
// is emitted alongside the project service, depends on it via uses: so the
// project provisions first, and that the agent is wired to both siblings.
func TestEmitResourceServices_WiresSiblingsToProject(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	conns := []project.Connection{{Name: "myconn", Category: "ApiKey"}}
	err := emitResourceServices(t.Context(), client, "myagent", "", nil, conns, nil)
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
	require.NoError(t, emitResourceServices(t.Context(), client, "myagent", "", deployments, conns, nil))

	server.mu.Lock()
	defer server.mu.Unlock()

	services := map[string]*azdext.ServiceConfig{}
	for _, svc := range server.added {
		// Resource keys must travel at the service level, not under config:.
		assert.Nil(t, svc.Config, "service %q must not nest keys under config:", svc.Name)
		assert.NotNil(t, svc.AdditionalProperties, "service %q must carry service-level keys", svc.Name)
		services[svc.Name] = svc
	}

	// The collectors read the service-level shape back through ServiceConfigProps.
	gotDeployments, err := collectProjectDeployments(services)
	require.NoError(t, err)
	require.Len(t, gotDeployments, 1)
	assert.Equal(t, "gpt-4.1-mini", gotDeployments[0].Name)

	gotConns, err := collectConnections(services)
	require.NoError(t, err)
	require.Len(t, gotConns, 1)
	assert.Equal(t, "myconn", gotConns[0].Name)
	assert.Equal(t, "ApiKey", gotConns[0].Category)
}

// TestEmitResourceServices_WritesEndpointForExistingProject verifies that a
// non-empty projectEndpoint is written as endpoint: on the ai-project service
// (the brownfield signal provision reads to reuse the project) and that an
// empty endpoint (new project) leaves the field unset.
func TestEmitResourceServices_WritesEndpointForExistingProject(t *testing.T) {
	t.Parallel()

	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"

	t.Run("existing project writes endpoint", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		require.NoError(t, emitResourceServices(t.Context(), client, "myagent", endpoint, nil, nil, nil))

		server.mu.Lock()
		defer server.mu.Unlock()

		require.Len(t, server.added, 1)
		projSvc := server.added[0]
		require.Equal(t, aiProjectServiceName, projSvc.Name)
		require.NotNil(t, projSvc.AdditionalProperties)
		assert.Equal(t, endpoint, projSvc.AdditionalProperties.Fields["endpoint"].GetStringValue())
	})

	t.Run("new project omits endpoint", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		require.NoError(t, emitResourceServices(t.Context(), client, "myagent", "", nil, nil, nil))

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
