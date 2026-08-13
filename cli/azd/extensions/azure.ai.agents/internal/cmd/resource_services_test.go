// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

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
	delegatedRequestPaths []string
	delegatedRequests     []map[string]any
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

type recordingProjectWorkflowServer struct {
	azdext.UnimplementedWorkflowServiceServer
	project *recordingProjectServer
}

func (s *recordingProjectWorkflowServer) Run(
	_ context.Context,
	req *azdext.RunWorkflowRequest,
) (*azdext.EmptyResponse, error) {
	workflow := req.GetWorkflow()
	if workflow == nil || len(workflow.GetSteps()) != 1 ||
		workflow.GetSteps()[0].GetCommand() == nil {
		return nil, fmt.Errorf("delegated workflow request is invalid")
	}
	args := workflow.GetSteps()[0].GetCommand().GetArgs()
	requestIndex := slices.Index(args, "--request-file")
	if requestIndex < 0 || requestIndex+1 >= len(args) {
		return nil, fmt.Errorf("delegated request file is missing")
	}
	requestPath := args[requestIndex+1]
	data, err := os.ReadFile(requestPath)
	if err != nil {
		return nil, fmt.Errorf("read delegated request: %w", err)
	}
	var request struct {
		Project struct {
			Endpoint   string `json:"endpoint"`
			Name       string `json:"name"`
			ResourceID string `json:"resourceId"`
		} `json:"project"`
		ReplaceDeployments bool `json:"replaceDeployments"`
		Model              struct {
			Name           string `json:"name"`
			DeploymentName string `json:"deploymentName"`
			Format         string `json:"format"`
			Version        string `json:"version"`
			SKU            string `json:"sku"`
			Capacity       int    `json:"capacity"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("decode delegated request: %w", err)
	}
	var rawRequest map[string]any
	if err := json.Unmarshal(data, &rawRequest); err != nil {
		return nil, fmt.Errorf("decode raw delegated request: %w", err)
	}

	s.project.mu.Lock()
	defer s.project.mu.Unlock()
	s.project.delegatedRequestPaths = append(
		s.project.delegatedRequestPaths, requestPath,
	)
	s.project.delegatedRequests = append(
		s.project.delegatedRequests, rawRequest,
	)
	if len(args) >= 4 && args[0] == "ai" && args[1] == "project" &&
		args[2] == "init" {
		name := delegatedProjectServiceNameForTest(
			request.Project.Name, s.project.existing,
		)
		body := map[string]any{}
		if request.Project.Endpoint != "" {
			body["endpoint"] = request.Project.Endpoint
		}
		if request.ReplaceDeployments {
			deployments := rawRequest["deployments"]
			if deployments == nil {
				deployments = []any{}
			}
			body["deployments"] = deployments
		}
		properties, err := structpb.NewStruct(body)
		if err != nil {
			return nil, err
		}
		if s.project.existing == nil {
			s.project.existing = map[string]*azdext.ServiceConfig{}
		}
		s.project.existing[name] = &azdext.ServiceConfig{
			Name:                 name,
			Host:                 AiProjectHost,
			AdditionalProperties: properties,
		}
		return &azdext.EmptyResponse{}, nil
	}

	if len(args) >= 5 && args[0] == "ai" && args[1] == "project" &&
		args[2] == "deployment" && args[3] == "add" {
		var projectName string
		for name, service := range s.project.existing {
			if service.GetHost() == AiProjectHost {
				projectName = name
				break
			}
		}
		if projectName == "" {
			return nil, fmt.Errorf("delegated project service is missing")
		}
		service := s.project.existing[projectName]
		body := service.GetAdditionalProperties().AsMap()
		deployments, _ := body["deployments"].([]any)
		modelName := request.Model.Name
		if slash := strings.IndexByte(modelName, '/'); slash >= 0 {
			modelName = modelName[slash+1:]
		}
		deployments = append(deployments, map[string]any{
			"name": firstNonEmptyTest(request.Model.DeploymentName, modelName),
			"model": map[string]any{
				"name":    modelName,
				"format":  request.Model.Format,
				"version": request.Model.Version,
			},
			"sku": map[string]any{
				"name":     request.Model.SKU,
				"capacity": request.Model.Capacity,
			},
		})
		body["deployments"] = deployments
		service.AdditionalProperties, err = structpb.NewStruct(body)
		if err != nil {
			return nil, err
		}
		return &azdext.EmptyResponse{}, nil
	}
	return nil, fmt.Errorf("unexpected delegated command %q", args)
}

func delegatedProjectServiceNameForTest(
	projectName string,
	services map[string]*azdext.ServiceConfig,
) string {
	for name, service := range services {
		if service.GetHost() == AiProjectHost {
			return name
		}
	}
	base := strings.ToLower(strings.TrimSpace(projectName))
	base = strings.NewReplacer(" ", "-", "_", "-", "/", "-").Replace(base)
	base = strings.Trim(base, "-")
	if base == "" {
		base = aiProjectServiceName
	}
	if _, exists := services[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, exists := services[candidate]; !exists {
			return candidate
		}
	}
}

func firstNonEmptyTest(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
	client, _ := newProjectRecorderClientWithAddress(t, server)
	return client
}

func newProjectRecorderClientWithAddress(
	t *testing.T,
	server azdext.ProjectServiceServer,
) (*azdext.AzdClient, string) {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, server)
	if projectServer, ok := server.(*recordingProjectServer); ok {
		azdext.RegisterWorkflowServiceServer(
			grpcServer,
			&recordingProjectWorkflowServer{project: projectServer},
		)
	}

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

	return client, listener.Addr().String()
}

func TestDelegateFoundryProjectResourcesUsesResourceID(t *testing.T) {
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	name, err := delegateFoundryProjectResources(
		t.Context(),
		client,
		"my project",
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/projects/project",
		"https://account.services.ai.azure.com/api/projects/project",
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "my-project", name)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Len(t, server.delegatedRequests, 1)
	projectBody := server.delegatedRequests[0]["project"].(map[string]any)
	assert.Equal(t,
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/projects/project",
		projectBody["resourceId"],
	)
	assert.NotContains(t, projectBody, "endpoint")
	require.Len(t, server.delegatedRequestPaths, 1)
	_, err = os.Stat(server.delegatedRequestPaths[0])
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestDelegateFoundryProjectInitUsesEndpoint(t *testing.T) {
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	name, err := delegateFoundryProjectInit(
		t.Context(),
		client,
		"my project",
		"",
		"https://account.services.ai.azure.com/api/projects/project",
	)
	require.NoError(t, err)
	assert.Equal(t, "my-project", name)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Len(t, server.delegatedRequests, 1)
	projectBody := server.delegatedRequests[0]["project"].(map[string]any)
	assert.Equal(t,
		"https://account.services.ai.azure.com/api/projects/project",
		projectBody["endpoint"],
	)
	assert.NotContains(t, projectBody, "resourceId")
}

func TestDelegateFoundryProjectDeploymentsReplacesDeclarations(
	t *testing.T,
) {
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	deployments := []project.Deployment{{
		Name: "chat",
		Model: project.DeploymentModel{
			Format:  "OpenAI",
			Name:    "gpt-4.1",
			Version: "2025-04-14",
		},
		Sku: project.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 10,
		},
	}}

	require.NoError(t, delegateFoundryProjectDeployments(
		t.Context(), client, "my project", "", "", deployments,
	))
	require.NoError(t, delegateFoundryProjectDeployments(
		t.Context(), client, "my project", "", "", nil,
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Len(t, server.delegatedRequests, 2)
	assert.True(t, server.delegatedRequests[0]["replaceDeployments"].(bool))
	declarations := server.delegatedRequests[0]["deployments"].([]any)
	require.Len(t, declarations, 1)
	declaration := declarations[0].(map[string]any)
	assert.Equal(t, "chat", declaration["name"])
	assert.Empty(t, server.delegatedRequests[1]["deployments"])
	for _, path := range server.delegatedRequestPaths {
		_, err := os.Stat(path)
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestDelegateFoundryProjectResourcesPreservesDeploymentTuple(
	t *testing.T,
) {
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	deployments := []project.Deployment{{
		Name: "chat",
		Model: project.DeploymentModel{
			Format:  "OpenAI",
			Name:    "gpt-4.1",
			Version: "2025-04-14",
		},
		Sku: project.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 10,
		},
	}}

	_, err := delegateFoundryProjectResources(
		t.Context(), client, "my project", "", "", deployments,
	)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Len(t, server.delegatedRequests, 2)
	model := server.delegatedRequests[1]["model"].(map[string]any)
	assert.Equal(t, "OpenAI/gpt-4.1", model["name"])
	assert.Equal(t, "chat", model["deploymentName"])
	assert.Equal(t, "OpenAI", model["format"])
	assert.Equal(t, "2025-04-14", model["version"])
	assert.Equal(t, "GlobalStandard", model["sku"])
	assert.Equal(t, float64(10), model["capacity"])
	for _, path := range server.delegatedRequestPaths {
		_, err := os.Stat(path)
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestDelegateFoundryProjectResourcesForwardsConstraints(t *testing.T) {
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	deployments := []project.Deployment{{
		Name:  "chat",
		Model: project.DeploymentModel{Name: "gpt-4.1"},
	}}
	constraints := delegatedProjectConstraints{
		Requirements: delegatedProjectRequirements{
			AllowedLocations: []string{"eastus"},
		},
		RequiredCapabilities: []string{"agentsV2"},
		AllowedLocations:     []string{"eastus"},
		ExcludedModelNames:   []string{"gpt-4o"},
	}

	_, err := delegateFoundryProjectResources(
		t.Context(), client, "my project", "", "", deployments, constraints,
	)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Len(t, server.delegatedRequests, 2)
	require.Equal(t, []any{"eastus"},
		server.delegatedRequests[0]["requirements"].(map[string]any)["allowedLocations"])
	model := server.delegatedRequests[1]["model"].(map[string]any)
	assert.Equal(t, []any{"eastus"}, model["allowedLocations"])
	assert.Equal(t, []any{"agentsV2"}, model["requiredCapabilities"])
	assert.Equal(t, []any{"gpt-4o"}, model["excludedModelNames"])
}

// TestEmitResourceServices_AlwaysEmitsProjectService verifies delegation
// occurs even when the agent has no deployments, connections, or toolboxes.
// The project service remains the stable provisioning-order anchor every
// agent references rather than being gated on a Foundry resource.
func TestEmitResourceServices_AlwaysEmitsProjectService(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	err := emitResourceServices(
		t.Context(), client, "myagent", "", "", "", nil, nil, nil,
	)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	require.Empty(t, server.added)
	projectSvc, ok := server.existing[aiProjectServiceName]
	require.True(t, ok)
	assert.Equal(t, AiProjectHost, projectSvc.Host)
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
	err := emitResourceServices(
		t.Context(), client, "myagent", "", "", "", nil, conns, nil,
	)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	require.Len(t, server.added, 1)
	assert.Equal(t, "myconn", server.added[0].Name)
	assert.Equal(t, AiConnectionHost, server.added[0].Host)
	projectSvc, ok := server.existing[aiProjectServiceName]
	require.True(t, ok)
	assert.Equal(t, AiProjectHost, projectSvc.Host)

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
	require.NoError(t, emitResourceServices(
		t.Context(), client, "myagent", "", "", "", deployments, conns, nil,
	))

	server.mu.Lock()
	defer server.mu.Unlock()

	services := map[string]*azdext.ServiceConfig{}
	maps.Copy(services, server.existing)
	for _, svc := range server.added {
		// Resource keys must travel at the service level, not under config:.
		assert.Nil(t, svc.Config, "service %q must not nest keys under config:", svc.Name)
		assert.NotNil(t, svc.AdditionalProperties, "service %q must carry service-level keys", svc.Name)
		services[svc.Name] = svc
	}

	// Init must write a project shape the owning extension can parse.
	var projectCfg project.ServiceTargetAgentConfig
	err := project.UnmarshalStruct(
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
// empty endpoint (new project) leaves the field unset.
func TestEmitResourceServices_WritesEndpointForExistingProject(t *testing.T) {
	t.Parallel()

	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"

	t.Run("existing project writes endpoint", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		require.NoError(t, emitResourceServices(
			t.Context(), client, "myagent", "", "", endpoint, nil, nil, nil,
		))

		server.mu.Lock()
		defer server.mu.Unlock()

		require.Empty(t, server.added)
		projSvc, ok := server.existing[aiProjectServiceName]
		require.True(t, ok)
		require.Equal(t, aiProjectServiceName, projSvc.Name)
		require.NotNil(t, projSvc.AdditionalProperties)
		assert.Equal(t, endpoint, projSvc.AdditionalProperties.Fields["endpoint"].GetStringValue())
	})

	t.Run("new project omits endpoint", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		require.NoError(t, emitResourceServices(
			t.Context(), client, "myagent", "", "", "", nil, nil, nil,
		))

		server.mu.Lock()
		defer server.mu.Unlock()

		require.Empty(t, server.added)
		projSvc, ok := server.existing[aiProjectServiceName]
		require.True(t, ok)
		if projSvc.AdditionalProperties != nil {
			_, ok := projSvc.AdditionalProperties.Fields["endpoint"]
			assert.False(t, ok, "endpoint must be omitted for a new project")
		}
	})
}

// TestEmitResourceServices_ProjectServiceKey verifies how the azure.ai.project
// service key is resolved: reuse an existing key, else derive from the project
// name, else fall back to "ai-project".
func TestEmitResourceServices_ProjectServiceKey(t *testing.T) {
	t.Parallel()

	t.Run("derives key from project name", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		require.NoError(t, emitResourceServices(
			t.Context(), client, "myagent", "my-foundry-proj", "", "", nil, nil, nil))

		server.mu.Lock()
		defer server.mu.Unlock()
		require.Empty(t, server.added)
		_, ok := server.existing["my-foundry-proj"]
		assert.True(t, ok)
		assert.Equal(t, []string{"my-foundry-proj"}, server.uses["myagent"])
	})

	t.Run("reuses existing project service key", func(t *testing.T) {
		server := &recordingProjectServer{
			existing: map[string]*azdext.ServiceConfig{
				"old-project-key": {Name: "old-project-key", Host: AiProjectHost},
			},
		}
		client := newProjectRecorderClient(t, server)

		// A different project name is supplied, but the existing key wins so a
		// repeated init does not create a second project service.
		require.NoError(t, emitResourceServices(
			t.Context(), client, "myagent", "a-new-name", "", "", nil, nil, nil))

		server.mu.Lock()
		defer server.mu.Unlock()
		require.Empty(t, server.added)
		_, ok := server.existing["old-project-key"]
		assert.True(t, ok)
	})

	t.Run("falls back when project name collides with agent", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		require.NoError(t, emitResourceServices(
			t.Context(), client, "myagent", "my agent", "", "", nil, nil, nil))

		server.mu.Lock()
		defer server.mu.Unlock()
		require.Empty(t, server.added)
		_, ok := server.existing["my-agent"]
		assert.True(t, ok)
	})

	t.Run("falls back when project name unknown", func(t *testing.T) {
		server := &recordingProjectServer{}
		client := newProjectRecorderClient(t, server)

		require.NoError(t, emitResourceServices(
			t.Context(), client, "myagent", "", "", "", nil, nil, nil))

		server.mu.Lock()
		defer server.mu.Unlock()
		require.Empty(t, server.added)
		_, ok := server.existing[aiProjectServiceName]
		assert.True(t, ok)
	})
}

// TestProjectNameHint verifies the project-name hint resolution: a selected
// existing project's name wins, else AZURE_AI_PROJECT_NAME when concretely set,
// else "" (unknown).
func TestProjectNameHint(t *testing.T) {
	t.Parallel()
	const envName = "dev"

	newClient := func(t *testing.T, vals map[string]string) *azdext.AzdClient {
		env := &testEnvironmentServiceServer{values: map[string]map[string]string{envName: vals}}
		return newTestAzdClient(t, env, &testWorkflowServiceServer{})
	}

	t.Run("selected project name wins", func(t *testing.T) {
		client := newClient(t, map[string]string{"AZURE_AI_PROJECT_NAME": "from-env"})
		got := projectNameHint(t.Context(), client, envName, &FoundryProjectInfo{ProjectName: "from-selected"})
		assert.Equal(t, "from-selected", got)
	})

	t.Run("falls back to env when no selection", func(t *testing.T) {
		client := newClient(t, map[string]string{"AZURE_AI_PROJECT_NAME": "from-env"})
		assert.Equal(t, "from-env", projectNameHint(t.Context(), client, envName, nil))
	})

	t.Run("placeholder env value yields empty", func(t *testing.T) {
		client := newClient(t, map[string]string{"AZURE_AI_PROJECT_NAME": "${AZURE_AI_PROJECT_NAME}"})
		assert.Equal(t, "", projectNameHint(t.Context(), client, envName, nil))
	})

	t.Run("missing env value yields empty", func(t *testing.T) {
		client := newClient(t, map[string]string{})
		assert.Equal(t, "", projectNameHint(t.Context(), client, envName, nil))
	})
}
