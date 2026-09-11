// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
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

func TestConnectionDeleteRunWithContext(t *testing.T) {
	const endpoint = "https://selected-account.services.ai.azure.com/api/projects/selected-project"
	const otherEndpoint = "https://other-account.services.ai.azure.com/api/projects/other-project"
	const connectionName = "Shared Connection"
	const resourcePath = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/selected-rg/" +
		"providers/Microsoft.CognitiveServices/accounts/selected-account/" +
		"projects/selected-project/connections/Shared Connection"
	networkFailure := errors.New("synthetic network failure")
	for _, tt := range []struct {
		name             string
		getStatus        int
		deleteStatus     int
		getErr           error
		force            bool
		noPrompt         bool
		cleanupOperation string
		cleanupErr       error
		repeats          int
	}{
		{name: "absent skips confirmation", getStatus: http.StatusNotFound},
		{name: "absent no-prompt without force is idempotent", getStatus: http.StatusNotFound, noPrompt: true, repeats: 2},
		{name: "absent with force", getStatus: http.StatusNotFound, force: true},
		{
			name: "absent read denied", getStatus: http.StatusNotFound, noPrompt: true,
			cleanupOperation: "GetValues", cleanupErr: status.Error(codes.PermissionDenied, "synthetic read failure"),
		},
		{
			name: "absent read not found is not ARM absence", getStatus: http.StatusNotFound, noPrompt: true,
			cleanupOperation: "GetValues", cleanupErr: status.Error(codes.NotFound, "synthetic missing environment"),
		},
		{
			name: "absent write denied", getStatus: http.StatusNotFound, noPrompt: true,
			cleanupOperation: "SetValue", cleanupErr: status.Error(codes.PermissionDenied, "synthetic write failure"),
		},
		{name: "GET forbidden", getStatus: http.StatusForbidden, force: true},
		{name: "GET server error", getStatus: http.StatusInternalServerError, force: true},
		{name: "GET network error", getErr: networkFailure, force: true},
		{name: "existing forced delete", getStatus: http.StatusOK, deleteStatus: http.StatusNoContent, force: true},
		{name: "racing deletion", getStatus: http.StatusOK, deleteStatus: http.StatusNotFound, force: true},
		{name: "DELETE server error", getStatus: http.StatusOK, deleteStatus: http.StatusInternalServerError, force: true},
		{
			name: "existing read failure prevents DELETE", getStatus: http.StatusOK, force: true,
			cleanupOperation: "GetValues", cleanupErr: status.Error(codes.PermissionDenied, "synthetic read failure"),
		},
		{
			name: "existing write failure prevents DELETE", getStatus: http.StatusOK, force: true,
			cleanupOperation: "SetValue", cleanupErr: status.Error(codes.PermissionDenied, "synthetic write failure"),
		},
		{name: "existing no-prompt still requires force", getStatus: http.StatusOK, noPrompt: true},
	} {
		for _, selection := range []string{"staging", ""} {
			t.Run(tt.name+"/environment="+selection, func(t *testing.T) {
				// AZD_SERVER is process-wide. Only the local gRPC fixture uses a socket;
				// every ARM request and token request is handled entirely in memory.
				t.Setenv("NO_COLOR", "1")
				target, referenced := connectionConfigParsingFixture(t, map[string]any{
					"name": connectionName, "category": "RemoteTool", "target": "https://example.test", "authType": "None",
				}, "json")
				reader, ok := target.projectClient.(*recordingProjectConfigReader)
				require.True(t, ok)
				makeService := func(values map[string]any) *azdext.ServiceConfig {
					props, err := structpb.NewStruct(values)
					require.NoError(t, err)
					return &azdext.ServiceConfig{
						Name: "not-the-service-key", Host: aiConnectionHost, AdditionalProperties: props,
					}
				}
				reader.services = map[string]*azdext.ServiceConfig{
					"search-service": makeService(map[string]any{"name": " shared CONNECTION "}),
					"search_service": referenced,
					"search.service": makeService(map[string]any{"$ref": "./connection.json", "name": "SHARED CONNECTION"}),
					// A matching key cannot override a different effective payload name.
					connectionName:  makeService(map[string]any{"name": "Other Connection"}),
					"other-project": makeService(map[string]any{"name": connectionName}),
				}
				before := map[string]string{"UNRELATED_VALUE": "preserved"}
				for key := range reader.services {
					before[envkey.ConnectionProjectEndpoint(key)] = " \t" + endpoint + "/// \n"
				}
				before[envkey.ConnectionProjectEndpoint("other-project")] = otherEndpoint
				cleared := maps.Clone(before)
				var clearedKeys []string
				for _, key := range []string{"search-service", "search.service", "search_service"} {
					markerKey := envkey.ConnectionProjectEndpoint(key)
					clearedKeys = append(clearedKeys, markerKey)
					cleared[markerKey] = ""
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
				switch tt.cleanupOperation {
				case "GetValues":
					environment.client.valuesErr = tt.cleanupErr
				case "SetValue":
					environment.client.setErr = tt.cleanupErr
				}
				project := &deleteReadinessProjectServer{reader: reader}
				// No Prompt service is registered: an unexpected confirmation fails the action.
				ctx := startDeleteReadinessServer(t, environment, project)
				selected, untouched := "staging", "default"
				if selection == "" {
					selected, untouched = "default", "staging"
				}
				assertValues := func(want map[string]string) {
					t.Helper()
					environment.mu.Lock()
					defer environment.mu.Unlock()
					assert.Equal(t, want, environment.client.values[selected])
					assert.Equal(t, before, environment.client.values[untouched], "never mutate the other environment")
				}
				wantValues := before
				var requests []string
				transport := deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
					requests = append(requests, request.Method)
					assert.Equal(t, "https", request.URL.Scheme)
					assert.Equal(t, "management.azure.com", request.URL.Host)
					assert.Equal(t, resourcePath, request.URL.Path,
						"use the resolved subscription, RG, account, project and name")
					assert.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
					assert.Equal(t, "Bearer synthetic-delete-token", request.Header.Get("Authorization"))
					if len(requests) == 1 {
						environment.mu.Lock()
						assert.Empty(t, environment.client.valuesRequests, "GET must precede marker cleanup")
						assert.Empty(t, environment.client.setRequests)
						environment.mu.Unlock()
					}
					statusCode := tt.getStatus
					switch request.Method {
					case http.MethodGet:
						assertValues(wantValues)
						if tt.getErr != nil {
							return nil, tt.getErr
						}
					case http.MethodDelete:
						// Inspect actual persisted state at the transport boundary, not a mocked cleanup callback.
						assertValues(cleared)
						environment.mu.Lock()
						assert.Len(t, environment.client.setRequests, len(clearedKeys), "all markers cleared before DELETE")
						environment.mu.Unlock()
						statusCode = tt.deleteStatus
					default:
						t.Errorf("unexpected ARM method %s", request.Method)
						return nil, errors.New("unexpected ARM request")
					}
					body := `{"error":{"code":"SyntheticFailure","message":"synthetic ARM failure"}}`
					if statusCode == http.StatusOK {
						body = `{"properties":{"authType":"None","category":"RemoteTool","target":"https://example.test"}}`
					} else if statusCode == http.StatusNoContent {
						body = ""
					}
					return &http.Response{
						StatusCode: statusCode,
						Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
						Request:    request,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				})
				credential := deleteReadinessCredential{}
				client, err := armcognitiveservices.NewProjectConnectionsClient(
					"11111111-1111-1111-1111-111111111111", credential, &arm.ClientOptions{
						ClientOptions: policy.ClientOptions{
							Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
						},
						DisableRPRegistration: true,
					},
				)
				require.NoError(t, err)
				connCtx := &connectionContext{
					armClient: client, rg: "selected-rg", account: "selected-account", project: "selected-project",
					sub: "11111111-1111-1111-1111-111111111111", cred: credential, endpoint: endpoint,
				}
				action := &ConnectionDeleteAction{flags: &connectionDeleteFlags{
					name: connectionName, environment: selection, force: tt.force, noPrompt: tt.noPrompt,
				}}
				wantCleanup := tt.getStatus == http.StatusNotFound || (tt.getStatus == http.StatusOK && tt.force)
				wantCleared := wantCleanup && tt.cleanupErr == nil
				var wantRequests, wantValuesRequests []string
				for range max(1, tt.repeats) {
					err := action.runWithContext(ctx, connCtx)
					switch {
					case tt.cleanupErr != nil:
						require.Error(t, err, "cleanup failure must not report successful deletion")
						assert.Equal(t, status.Code(tt.cleanupErr), status.Code(err))
						assert.ErrorContains(t, err, status.Convert(tt.cleanupErr).Message())
						if tt.cleanupOperation == "SetValue" {
							assert.ErrorContains(t, err, "clearing Connection project marker")
							assert.ErrorContains(t, err, clearedKeys[0])
						}
					case tt.getErr != nil:
						require.ErrorIs(t, err, tt.getErr)
					case tt.getStatus == http.StatusOK && !tt.force && tt.noPrompt:
						localErr, ok := errors.AsType[*azdext.LocalError](err)
						require.True(t, ok, "expected confirmation validation, got %v", err)
						assert.Equal(t, exterrors.CodeMissingForceFlag, localErr.Code)
					case tt.getStatus == http.StatusForbidden || tt.getStatus == http.StatusInternalServerError ||
						tt.deleteStatus == http.StatusInternalServerError:
						serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
						require.True(t, ok, "expected preserved ARM service error, got %v", err)
						wantStatus, wantOperation := tt.getStatus, exterrors.OpGetConnection
						if tt.getStatus == http.StatusOK {
							wantStatus, wantOperation = tt.deleteStatus, exterrors.OpDeleteConnection
						}
						assert.Equal(t, wantStatus, serviceErr.StatusCode)
						assert.Equal(t, wantOperation, serviceErr.ServiceName)
						assert.Equal(t, "SyntheticFailure", serviceErr.ErrorCode)
						assert.ErrorContains(t, err, "synthetic ARM failure")
					default:
						require.NoError(t, err)
					}
					wantRequests = append(wantRequests, http.MethodGet)
					if wantCleared && tt.getStatus == http.StatusOK {
						wantRequests = append(wantRequests, http.MethodDelete)
					}
					assert.Equal(t, wantRequests, requests, "no retries or DELETE for an absent or unconfirmed connection")
					if wantCleanup {
						wantValuesRequests = append(wantValuesRequests, selected)
					}
					if wantCleared {
						wantValues = cleared
					}
					assertValues(wantValues)
				}
				environment.mu.Lock()
				defer environment.mu.Unlock()
				assert.Equal(t, wantValuesRequests, environment.client.valuesRequests)
				currentCalls := 0
				if selection == "" {
					currentCalls = len(wantValuesRequests)
				}
				assert.Equal(t, currentCalls, environment.currentCalls)
				var writtenKeys []string
				for _, request := range environment.client.setRequests {
					assert.Equal(t, selected, request.GetEnvName())
					assert.Empty(t, request.GetValue(), "only invalidate, never publish readiness")
					writtenKeys = append(writtenKeys, request.GetKey())
				}
				switch {
				case wantCleared:
					assert.Equal(t, clearedKeys, writtenKeys, "each matching service key is cleared exactly once")
				case tt.cleanupOperation == "SetValue":
					assert.Equal(t, clearedKeys[:1], writtenKeys, "stop on the first persistence failure")
				default:
					assert.Empty(t, writtenKeys)
				}
				if !wantCleanup || tt.cleanupOperation == "GetValues" {
					project.mu.Lock()
					assert.Zero(t, project.getCalls, "do not load project definitions before successful readiness lookup")
					project.mu.Unlock()
				}
			})
		}
	}
}

func TestIsConnectionNotFound(t *testing.T) {
	t.Parallel()
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil"},
		{name: "SDK 404", err: notFound, want: true},
		{name: "wrapped SDK 404", err: fmt.Errorf("reading connection: %w", notFound), want: true},
		{name: "SDK 403", err: &azcore.ResponseError{StatusCode: http.StatusForbidden}},
		{name: "SDK 500", err: &azcore.ResponseError{StatusCode: http.StatusInternalServerError}},
		{name: "non-SDK 404 text", err: errors.New("404 connection not found")},
		{name: "gRPC not found", err: status.Error(codes.NotFound, "environment not found")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isConnectionNotFound(tt.err))
		})
	}
}

type deleteReadinessTransport func(*http.Request) (*http.Response, error)

func (f deleteReadinessTransport) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type deleteReadinessCredential struct{}

func (deleteReadinessCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "synthetic-delete-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

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
