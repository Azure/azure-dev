// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"maps"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/pkg/envkey"
)

func TestInvalidateDeletedConnectionMarkersSelectsEnvironment(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	const otherEndpoint = "https://account.services.ai.azure.com/api/projects/other"
	for _, selection := range []string{"staging", ""} {
		for _, source := range []string{"inline", "config", "json", "yaml"} {
			t.Run("environment="+selection+"/"+source, func(t *testing.T) {
				// AZD_SERVER is process-wide; these wrapper tests must not run in parallel.
				target, svc := connectionConfigParsingFixture(t, map[string]any{
					"name": "Shared Connection", "category": "RemoteTool",
					"target": "https://example.test", "authType": "None",
				}, source)
				reader, ok := target.projectClient.(*recordingProjectConfigReader)
				require.True(t, ok)
				reader.services = map[string]*azdext.ServiceConfig{
					"search-service": svc, // The marker uses the map key, not svc.Name or the payload name.
					"other-project":  svc,
					"other-name":     {Name: "other-name", Host: aiConnectionHost},
				}
				markerKey := envkey.ConnectionProjectEndpoint("search-service")
				before := map[string]string{
					markerKey: " \t" + endpoint + "/// \n",
					envkey.ConnectionProjectEndpoint("other-project"): otherEndpoint,
					envkey.ConnectionProjectEndpoint("other-name"):    endpoint,
					"UNRELATED_VALUE": "preserved",
				}
				environment := &deleteReadinessEnvironmentServer{
					current: &azdext.Environment{Name: "default"},
					client: &deleteReadinessEnvironmentClient{
						recordingServiceEnvironmentClient: &recordingServiceEnvironmentClient{
							values: map[string]map[string]string{
								"staging": maps.Clone(before), "default": maps.Clone(before),
							},
						},
					},
				}
				project := &deleteReadinessProjectServer{reader: reader}
				ctx := startDeleteReadinessServer(t, environment, project)

				err := invalidateDeletedConnectionMarkers(ctx, selection, " shared CONNECTION ", endpoint+"/ ")
				require.NoError(t, err)
				environment.mu.Lock()
				defer environment.mu.Unlock()
				project.mu.Lock()
				defer project.mu.Unlock()
				selected, untouched, currentCalls := "staging", "default", 0
				if selection == "" {
					selected, untouched, currentCalls = "default", "staging", 1
				}
				assert.Equal(t, currentCalls, environment.currentCalls, "explicit selection must bypass GetCurrent")
				assert.Equal(t, []string{selected}, environment.client.valuesRequests)
				assert.Equal(t, 2, project.getCalls, "load services and the project path used for definition references")
				require.Len(t, environment.client.setRequests, 1)
				request := environment.client.setRequests[0]
				assert.Equal(t, selected, request.GetEnvName())
				assert.Equal(t, markerKey, request.GetKey())
				assert.Empty(t, request.GetValue(), "deletion must clear readiness, not publish it")
				want := maps.Clone(before)
				want[markerKey] = ""
				assert.Equal(t, want, environment.client.values[selected])
				assert.Equal(t, before, environment.client.values[untouched])
			})
		}
	}
}

func TestInvalidateDeletedConnectionMarkersWithoutCurrentEnvironment(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, tt := range []struct {
		name       string
		current    *azdext.Environment
		currentErr error
		wantCode   codes.Code
	}{
		{"not found", nil, status.Error(codes.NotFound, "no current environment"), codes.OK},
		{"missing environment", nil, nil, codes.OK},
		{"empty environment name", &azdext.Environment{}, nil, codes.OK},
		{
			"permission denied", nil,
			status.Error(codes.PermissionDenied, "current environment denied"), codes.PermissionDenied,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			markerKey := envkey.ConnectionProjectEndpoint("search")
			before := map[string]string{markerKey: endpoint}
			environment := &deleteReadinessEnvironmentServer{
				current: tt.current, currentErr: tt.currentErr,
				client: &deleteReadinessEnvironmentClient{
					recordingServiceEnvironmentClient: &recordingServiceEnvironmentClient{
						values: map[string]map[string]string{"default": maps.Clone(before)},
					},
				},
			}
			project := &deleteReadinessProjectServer{reader: &recordingProjectConfigReader{
				services: map[string]*azdext.ServiceConfig{"search": {Name: "search", Host: aiConnectionHost}},
			}}
			ctx := startDeleteReadinessServer(t, environment, project)

			err := invalidateDeletedConnectionMarkers(ctx, "", "search", endpoint)
			if tt.wantCode == codes.OK {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.currentErr.Error())
				assert.Equal(t, tt.wantCode, status.Code(err))
			}
			environment.mu.Lock()
			defer environment.mu.Unlock()
			project.mu.Lock()
			defer project.mu.Unlock()
			assert.Equal(t, 1, environment.currentCalls)
			assert.Empty(t, environment.client.valuesRequests, "do not read readiness without an environment")
			assert.Zero(t, project.getCalls, "do not read project configuration without an environment")
			assert.Empty(t, environment.client.setRequests)
			assert.Equal(t, map[string]map[string]string{"default": before}, environment.client.values)
		})
	}
}

func TestInvalidateDeletedConnectionMarkersPropagatesPersistenceErrors(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, tt := range []struct {
		name         string
		operation    string
		code         codes.Code
		projectCalls int
	}{
		{"read values denied", "GetValues", codes.PermissionDenied, 0},
		{"read values not found", "GetValues", codes.NotFound, 0},
		{"read project", "GetProject", codes.PermissionDenied, 1},
		{"read project path", "GetProject", codes.PermissionDenied, 2},
		{"write marker", "SetValue", codes.PermissionDenied, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			failure := status.Error(tt.code, "synthetic "+tt.name+" failure")
			target, svc := connectionConfigParsingFixture(t, map[string]any{"name": "Shared Connection"}, "json")
			reader, ok := target.projectClient.(*recordingProjectConfigReader)
			require.True(t, ok)
			reader.services = map[string]*azdext.ServiceConfig{"search": svc}
			markerKey := envkey.ConnectionProjectEndpoint("search")
			before := map[string]string{markerKey: endpoint}
			environment := &deleteReadinessEnvironmentServer{
				current: &azdext.Environment{Name: "default"},
				client: &deleteReadinessEnvironmentClient{
					recordingServiceEnvironmentClient: &recordingServiceEnvironmentClient{
						values: map[string]map[string]string{
							"staging": maps.Clone(before), "default": maps.Clone(before),
						},
					},
				},
			}
			project := &deleteReadinessProjectServer{reader: reader}
			switch tt.operation {
			case "GetValues":
				environment.client.valuesErr = failure
			case "GetProject":
				project.getErr, project.getErrAt = failure, tt.projectCalls
			case "SetValue":
				environment.client.setErr = failure
			}
			ctx := startDeleteReadinessServer(t, environment, project)

			err := invalidateDeletedConnectionMarkers(ctx, "staging", "Shared Connection", endpoint)
			require.Error(t, err, "failed persistence must not be reported as successful invalidation")
			assert.Equal(t, tt.code, status.Code(err), "preserve the gRPC status through any wrapping")
			assert.ErrorContains(t, err, "synthetic "+tt.name+" failure")
			environment.mu.Lock()
			defer environment.mu.Unlock()
			project.mu.Lock()
			defer project.mu.Unlock()
			assert.Zero(t, environment.currentCalls)
			assert.Equal(t, []string{"staging"}, environment.client.valuesRequests)
			assert.Equal(t, tt.projectCalls, project.getCalls)
			if tt.operation == "SetValue" {
				assert.ErrorContains(t, err, "clearing Connection project marker")
				assert.ErrorContains(t, err, markerKey)
				require.Len(t, environment.client.setRequests, 1)
				assert.Equal(t, "staging", environment.client.setRequests[0].GetEnvName())
				assert.Equal(t, markerKey, environment.client.setRequests[0].GetKey())
				assert.Empty(t, environment.client.setRequests[0].GetValue())
			} else {
				assert.Empty(t, environment.client.setRequests)
			}
			if tt.operation == "GetProject" && tt.projectCalls == 2 {
				assert.ErrorContains(t, err, "reading project path for connection service")
			}
			assert.Equal(t, map[string]map[string]string{
				"staging": before, "default": before,
			}, environment.client.values, "failures must leave both environments unchanged")
		})
	}
}

func startDeleteReadinessServer(
	t *testing.T, environment *deleteReadinessEnvironmentServer, project *deleteReadinessProjectServer,
) context.Context {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, environment)
	azdext.RegisterProjectServiceServer(server, project)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("serving delete readiness requests: %v", err)
		}
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type deleteReadinessEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	mu           sync.Mutex
	client       *deleteReadinessEnvironmentClient
	current      *azdext.Environment
	currentErr   error
	currentCalls int
}

func (s *deleteReadinessEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentCalls++
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	return &azdext.EnvironmentResponse{Environment: s.current}, nil
}

func (s *deleteReadinessEnvironmentServer) GetValues(
	ctx context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client.GetValues(ctx, request)
}

func (s *deleteReadinessEnvironmentServer) SetValue(
	ctx context.Context, request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client.SetValue(ctx, request)
}

type deleteReadinessProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	mu       sync.Mutex
	reader   *recordingProjectConfigReader
	getErr   error
	getErrAt int
	getCalls int
}

func (s *deleteReadinessProjectServer) Get(
	ctx context.Context, request *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.getErr != nil && s.getCalls == s.getErrAt {
		return nil, s.getErr
	}
	return s.reader.Get(ctx, request)
}

func TestInvalidateMatchingConnectionMarkersEffectiveNames(t *testing.T) {
	t.Parallel()
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, tt := range []struct {
		name           string
		serviceKey     string
		connectionName string
		deletedName    string
		wantClear      bool
	}{
		{"payload name overrides key", "search-service", "Shared Connection", "Shared Connection", true},
		{"case and whitespace", "search-service", " \tShared Connection\n", " shared CONNECTION ", true},
		{"omitted name uses map key", "Search", "", " SEARCH ", true},
		{"blank name uses map key", "Search", " \t\n", "search", true},
		{"key must not override payload", "Search", "Other Connection", "search", false},
		{"unrelated name", "search-service", "Shared Connection", "different", false},
		{"overlaid reference name is not effective", "search-service", "Shared Connection", "file-only", false},
	} {
		for _, source := range []string{"inline", "config", "json", "yaml", "ref overlay"} {
			t.Run(tt.name+"/"+source, func(t *testing.T) {
				t.Parallel()
				values := map[string]any{
					"category": "RemoteTool", "target": "https://example.test", "authType": "None",
				}
				if tt.connectionName != "" {
					values["name"] = tt.connectionName
				}
				fixtureSource := source
				if source == "ref overlay" {
					fixtureSource = "json"
					values["name"] = "file-only"
				}
				target, svc := connectionConfigParsingFixture(t, values, fixtureSource)
				if source == "ref overlay" {
					svc.AdditionalProperties.Fields["name"] = structpb.NewStringValue(tt.connectionName)
				}
				// Readiness and fallback names use the project map key, not the DTO's name.
				svc.Name = "not-the-service-key"
				project, ok := target.projectClient.(*recordingProjectConfigReader)
				require.True(t, ok)
				project.services = map[string]*azdext.ServiceConfig{tt.serviceKey: svc}
				markerKey := envkey.ConnectionProjectEndpoint(tt.serviceKey)
				marker := " \t" + endpoint + "/// \n"
				environment := &recordingServiceEnvironmentClient{values: map[string]map[string]string{
					"staging":    {markerKey: marker},
					"production": {markerKey: endpoint},
				}}
				target.envClient = environment

				err := target.invalidateMatchingConnectionMarkers(t.Context(), "staging", tt.deletedName, endpoint+"/ ")
				require.NoError(t, err)
				assert.Equal(t, []string{"staging"}, environment.valuesRequests)
				if tt.wantClear {
					require.Len(t, environment.setRequests, 1)
					assert.Equal(t, "staging", environment.setRequests[0].GetEnvName())
					assert.Equal(t, markerKey, environment.setRequests[0].GetKey())
					assert.Empty(t, environment.setRequests[0].GetValue())
					marker = ""
				} else {
					assert.Empty(t, environment.setRequests)
				}
				assert.Equal(t, map[string]string{markerKey: marker}, environment.values["staging"])
				assert.Equal(t, map[string]string{markerKey: endpoint}, environment.values["production"])
			})
		}
	}
}

func TestInvalidateMatchingConnectionMarkersClearsAllMatchingServiceKeys(t *testing.T) {
	t.Parallel()
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	const otherEndpoint = "https://account.services.ai.azure.com/api/projects/other"
	target, referenced := connectionConfigParsingFixture(t, map[string]any{
		"name": "Shared", "category": "RemoteTool", "target": "https://example.test", "authType": "None",
	}, "json")
	makeService := func(key, host string, values map[string]any) *azdext.ServiceConfig {
		props, err := structpb.NewStruct(values)
		require.NoError(t, err)
		return &azdext.ServiceConfig{Name: key, Host: host, AdditionalProperties: props}
	}
	referenced.Name = "referenced"
	services := map[string]*azdext.ServiceConfig{
		"inline":     makeService("inline", aiConnectionHost, map[string]any{"name": " SHARED "}),
		"referenced": referenced,
		"overlaid": makeService("overlaid", aiConnectionHost, map[string]any{
			"$ref": "./connection.json", "name": "shared",
		}),
		"Shared":    {Name: "not-the-map-key", Host: aiConnectionHost},
		"unrelated": makeService("unrelated", aiConnectionHost, map[string]any{"name": "other"}),
		// These definitions must be skipped before parsing, even when invalid.
		"other-project": makeService("other-project", aiConnectionHost, map[string]any{"unexpected": true}),
		"other-host":    makeService("other-host", "azure.ai.agent", map[string]any{"unexpected": true}),
		"missing":       makeService("missing", aiConnectionHost, map[string]any{"$ref": "./missing.json"}),
		"empty":         makeService("empty", aiConnectionHost, map[string]any{"unexpected": true}),
	}
	project, ok := target.projectClient.(*recordingProjectConfigReader)
	require.True(t, ok)
	project.services = services
	values := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": endpoint,
		"UNRELATED_VALUE":          "preserved",
	}
	for key := range services {
		values[envkey.ConnectionProjectEndpoint(key)] = endpoint
	}
	values[envkey.ConnectionProjectEndpoint("other-project")] = otherEndpoint
	values[envkey.ConnectionProjectEndpoint("empty")] = ""
	delete(values, envkey.ConnectionProjectEndpoint("missing"))
	values[envkey.ConnectionProjectEndpoint("orphan")] = endpoint
	production := maps.Clone(values)
	wantStaging := maps.Clone(values)
	var wantClearedKeys []string
	for _, key := range []string{"inline", "referenced", "overlaid", "Shared"} {
		markerKey := envkey.ConnectionProjectEndpoint(key)
		wantClearedKeys = append(wantClearedKeys, markerKey)
		wantStaging[markerKey] = ""
	}
	environment := &recordingServiceEnvironmentClient{values: map[string]map[string]string{
		"staging": values, "production": maps.Clone(production),
	}}
	target.envClient = environment

	err := target.invalidateMatchingConnectionMarkers(t.Context(), "staging", " sHaReD ", endpoint)
	require.NoError(t, err)
	assert.Equal(t, []string{"staging"}, environment.valuesRequests)
	var clearedKeys []string
	for _, request := range environment.setRequests {
		assert.Equal(t, "staging", request.GetEnvName())
		assert.Empty(t, request.GetValue(), "deletion must only clear readiness, never publish it")
		clearedKeys = append(clearedKeys, request.GetKey())
	}
	assert.ElementsMatch(t, wantClearedKeys, clearedKeys, "all aliases of the deleted connection must be invalidated")
	assert.Equal(t, wantStaging, environment.values["staging"])
	assert.Equal(t, production, environment.values["production"])
}

func TestInvalidateMatchingConnectionMarkersUsesOnlyPersistedMarkers(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	markerKey := envkey.ConnectionProjectEndpoint("search")
	t.Setenv(markerKey, endpoint)
	for _, tt := range []struct {
		name   string
		values map[string]string
	}{
		{"missing", map[string]string{}},
		{"empty", map[string]string{markerKey: ""}},
		{"other project", map[string]string{markerKey: endpoint + "-other"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := maps.Clone(tt.values)
			environment := &recordingServiceEnvironmentClient{values: map[string]map[string]string{
				"staging": maps.Clone(before), "production": {markerKey: endpoint},
			}}
			target := &connectionServiceTarget{
				environment: "production", // The explicit method argument must win.
				envClient:   environment,
				projectClient: &recordingProjectConfigReader{services: map[string]*azdext.ServiceConfig{
					"search": {Name: "search", Host: aiConnectionHost, Environment: map[string]string{markerKey: endpoint}},
				}},
			}

			err := target.invalidateMatchingConnectionMarkers(t.Context(), "staging", "search", endpoint)
			require.NoError(t, err)
			assert.Equal(t, []string{"staging"}, environment.valuesRequests)
			assert.Empty(t, environment.setRequests, "ambient readiness must not authorize persisted marker changes")
			assert.Equal(t, before, environment.values["staging"])
			assert.Equal(t, map[string]string{markerKey: endpoint}, environment.values["production"])
		})
	}
}

func TestInvalidateMatchingConnectionMarkersRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, tt := range []struct {
		name   string
		values map[string]any
		code   string
		field  string
	}{
		{
			"unknown field", map[string]any{"name": "search", "authTyp": "None"},
			exterrors.CodeInvalidParameter, "authTyp",
		},
		{
			"invalid name type", map[string]any{"name": true},
			exterrors.CodeInvalidParameter, "name",
		},
		{
			"core field", map[string]any{"name": "search", "env": map[string]any{}},
			exterrors.CodeInvalidParameter, "env",
		},
		{
			"missing reference", map[string]any{"$ref": "./missing.json"},
			foundry.CodeInvalidFileRef, "$ref",
		},
	} {
		for _, source := range []string{"inline", "config", "json", "yaml"} {
			t.Run(tt.name+"/"+source, func(t *testing.T) {
				t.Parallel()
				target, svc := connectionConfigParsingFixture(t, tt.values, source)
				project, ok := target.projectClient.(*recordingProjectConfigReader)
				require.True(t, ok)
				project.services = map[string]*azdext.ServiceConfig{svc.GetName(): svc}
				markerKey := envkey.ConnectionProjectEndpoint(svc.GetName())
				environment := &recordingServiceEnvironmentClient{values: map[string]map[string]string{
					"staging": {markerKey: endpoint},
				}}
				target.envClient = environment

				err := target.invalidateMatchingConnectionMarkers(t.Context(), "staging", "search", endpoint)
				requireConnectionValidationError(t, err, tt.code, tt.field)
				assert.IsType(t, &azdext.LocalError{}, err, "preserve the structured parsing error")
				assert.Equal(t, []string{"staging"}, environment.valuesRequests)
				assert.Empty(t, environment.setRequests, "invalid definitions must not report successful invalidation")
				assert.Equal(t, map[string]string{markerKey: endpoint}, environment.values["staging"])
			})
		}
	}
}

func TestInvalidateMatchingConnectionMarkersPropagatesPersistenceErrors(t *testing.T) {
	t.Parallel()
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, operation := range []string{"GetValues", "GetProject", "SetValue"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			failure := errors.New("synthetic " + operation + " failure")
			markerKey := envkey.ConnectionProjectEndpoint("search")
			environment := &deleteReadinessEnvironmentClient{
				recordingServiceEnvironmentClient: &recordingServiceEnvironmentClient{
					values: map[string]map[string]string{
						"staging": {markerKey: endpoint}, "production": {markerKey: endpoint},
					},
				},
			}
			project := &deleteReadinessProjectReader{
				recordingProjectConfigReader: &recordingProjectConfigReader{
					services: map[string]*azdext.ServiceConfig{"search": {Name: "search", Host: aiConnectionHost}},
				},
			}
			switch operation {
			case "GetValues":
				environment.valuesErr = failure
			case "GetProject":
				project.getErr = failure
			case "SetValue":
				environment.setErr = failure
			}
			target := &connectionServiceTarget{projectClient: project, envClient: environment}

			err := target.invalidateMatchingConnectionMarkers(t.Context(), "staging", "search", endpoint)
			require.ErrorIs(t, err, failure, "failed persistence must not be reported as successful invalidation")
			assert.Equal(t, []string{"staging"}, environment.valuesRequests)
			if operation == "GetValues" {
				assert.Zero(t, project.getCalls, "stop before loading project services when readiness cannot be read")
			}
			if operation == "SetValue" {
				assert.ErrorContains(t, err, "clearing Connection project marker")
				assert.ErrorContains(t, err, markerKey)
				require.Len(t, environment.setRequests, 1)
				assert.Equal(t, "staging", environment.setRequests[0].GetEnvName())
				assert.Equal(t, markerKey, environment.setRequests[0].GetKey())
				assert.Empty(t, environment.setRequests[0].GetValue())
			} else {
				assert.Empty(t, environment.setRequests)
			}
			assert.Equal(t, map[string]map[string]string{
				"staging": {markerKey: endpoint}, "production": {markerKey: endpoint},
			}, environment.values, "failed persistence must not mutate readiness in either environment")
		})
	}
}

type deleteReadinessEnvironmentClient struct {
	*recordingServiceEnvironmentClient
	valuesErr error
}

func (r *deleteReadinessEnvironmentClient) GetValues(
	ctx context.Context, request *azdext.GetEnvironmentRequest, opts ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	if r.valuesErr != nil {
		r.valuesRequests = append(r.valuesRequests, request.GetName())
		return nil, r.valuesErr
	}
	return r.recordingServiceEnvironmentClient.GetValues(ctx, request, opts...)
}

type deleteReadinessProjectReader struct {
	*recordingProjectConfigReader
	getErr   error
	getCalls int
}

func (r *deleteReadinessProjectReader) Get(
	ctx context.Context, request *azdext.EmptyRequest, opts ...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	r.getCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.recordingProjectConfigReader.Get(ctx, request, opts...)
}
