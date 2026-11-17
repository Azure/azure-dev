// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS kubelet

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type provisionTimingServer struct {
	azdext.UnimplementedTelemetryServiceServer
	azdext.UnimplementedEnvironmentServiceServer
	mu     sync.Mutex
	events []*azdext.ReportUsageRequest
	code   codes.Code
	parent string
}

func (s *provisionTimingServer) ReportUsage(
	ctx context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, req)
	md, _ := metadata.FromIncomingContext(ctx)
	s.parent = md.Get("traceparent")[0]
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

func (s *provisionTimingServer) GetValue(
	context.Context, *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	if s.code != codes.OK {
		return nil, status.Error(s.code, "resource response not recorded")
	}
	return &azdext.KeyValueResponse{}, nil
}

func (s *provisionTimingServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if s.code != codes.OK {
		return nil, status.Error(s.code, "resource response not recorded")
	}
	return &azdext.KeyValueListResponse{}, nil
}

func (s *provisionTimingServer) SetValue(
	context.Context, *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	return &azdext.EmptyResponse{}, nil
}

func (s *provisionTimingServer) SetConfig(
	context.Context, *azdext.SetConfigRequest,
) (*azdext.EmptyResponse, error) {
	return &azdext.EmptyResponse{}, nil
}

func (s *provisionTimingServer) GetConfig(
	context.Context, *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	return &azdext.GetConfigResponse{}, nil
}

func newProvisionTimingClient(t *testing.T, code codes.Code) (*azdext.AzdClient, *provisionTimingServer) {
	t.Helper()
	capture := &provisionTimingServer{code: code}
	server := grpc.NewServer()
	azdext.RegisterTelemetryServiceServer(server, capture)
	azdext.RegisterEnvironmentServiceServer(server, capture)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, capture
}

func provisionTimingContext(t *testing.T) context.Context {
	t.Helper()
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := propagation.TraceContext{}.Extract(
		t.Context(),
		propagation.MapCarrier{"traceparent": parent},
	)
	return azdext.WithAccessToken(ctx, "test-token")
}

func TestProvisionProviderTimingsCoverSharedEntriesAndEarlyReturns(t *testing.T) {
	for _, entry := range []string{"up", "provision"} {
		for _, test := range []struct {
			name    string
			reuse   bool
			code    codes.Code
			outcome string
		}{
			{"reuse", true, codes.OK, "success"},
			{"template error", false, codes.OK, "error"},
			{"canceled", true, codes.Canceled, "canceled"},
			{"timeout", true, codes.DeadlineExceeded, "timeout"},
		} {
			t.Run(entry+"/"+test.name, func(t *testing.T) {
				capture := &provisionTimingServer{code: test.code}
				server := grpc.NewServer()
				azdext.RegisterTelemetryServiceServer(server, capture)
				azdext.RegisterEnvironmentServiceServer(server, capture)
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				done := make(chan error, 1)
				go func() { done <- server.Serve(listener) }()
				t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
				client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
				require.NoError(t, err)
				t.Cleanup(client.Close)
				const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
				ctx := propagation.TraceContext{}.Extract(t.Context(), propagation.MapCarrier{"traceparent": parent})
				ctx = azdext.WithAccessToken(ctx, "test-token")
				provider := &FoundryProvisioningProvider{
					azdClient: client, projectPath: t.TempDir(), existingProjectReuseOnly: test.reuse,
				}
				result, err := provider.Deploy(ctx, func(string) {})
				if test.outcome == "success" {
					require.NoError(t, err)
					require.NotNil(t, result)
				} else {
					require.Error(t, err)
				}
				capture.mu.Lock()
				defer capture.mu.Unlock()
				require.NotEmpty(t, capture.events)
				first := capture.events[0]
				require.Equal(t, "agent.phase.completed", first.EventName)
				require.Equal(t, "provision", first.Attributes["agent.phase.name"])
				require.Equal(t, "provider", first.Attributes["agent.phase.scope"])
				require.Equal(t, test.outcome, first.Attributes["agent.phase.outcome"])
				require.Equal(t, parent, capture.parent)
				require.NotEmpty(t, first.Attributes["agent.phase.start_time"])
				require.NotEmpty(t, first.Attributes["agent.phase.end_time"])
				for _, event := range capture.events[1:] {
					require.Equal(t, first.Attributes["agent.phase.attempt_id"], event.Attributes["agent.phase.attempt_id"])
				}
			})
		}
	}
}

func TestProvisionProviderBYOAKSTelemetry(t *testing.T) {
	const invalidIdentityID = "/subscriptions/id-sub/resourceGroups/id-rg/providers/Microsoft.Compute/disks/bad-id"

	assertNoResourceLeak := func(t *testing.T, events []*azdext.ReportUsageRequest, forbidden ...string) {
		t.Helper()
		payload := fmt.Sprint(events)
		for _, secret := range append([]string{
			offlineClusterID,
			offlineClusterGroupID,
			offlineKubeletObjectID,
			offlineProjectID,
			offlineProjectEndpoint,
			invalidIdentityID,
			"aks-rg",
			"cluster-sub",
		}, forbidden...) {
			require.NotContains(t, payload, secret)
		}
		for _, event := range events[1:] {
			_, stepMarked := event.Attributes["agent.hosting.byo_aks"]
			require.False(t, stepMarked)
		}
	}

	t.Run("byo aks deploy success", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		result, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "agent.phase.completed", first.EventName)
		require.Equal(t, "provision", first.Attributes["agent.phase.name"])
		require.Equal(t, "provider", first.Attributes["agent.phase.scope"])
		require.Equal(t, "success", first.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", first.Attributes["agent.hosting.byo_aks"])
		assertNoResourceLeak(t, capture.events)
	})

	t.Run("byo aks feature gate disabled with file ref", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		projectDir := t.TempDir()
		refPath := filepath.Join(projectDir, "project.yaml")
		require.NoError(t, os.WriteFile(refPath, []byte(
			"host: azure.ai.project\n"+
				"monitoring:\n  enabled: false\n"+
				"agentHosting:\n  clusterResourceId: "+offlineClusterID+"\n",
		), 0o600))
		synthResult, err := synthesis.Synthesize(synthesis.Input{
			ServiceName:  "project",
			ProjectRoot:  projectDir,
			RawAzureYAML: []byte("services:\n  project:\n    $ref: ./project.yaml\n"),
		})
		require.NoError(t, err)
		provider.projectPath = projectDir
		provider.synthResult = synthResult
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client
		t.Setenv(synthesis.BYOAKSEnvVar, "")
		require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))

		_, err = provider.Deploy(provisionTimingContext(t), func(string) {})
		requireBYOFeatureNotEnabled(t, err)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "agent.phase.completed", first.EventName)
		require.Equal(t, "error", first.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", first.Attributes["agent.hosting.byo_aks"])
		assertNoResourceLeak(t, capture.events, refPath)
	})

	t.Run("byo aks invalid cluster id", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		const badClusterID = "invalid-cluster-resource-id"
		provider.synthResult.Parameters["agentHosting"] = map[string]any{
			"enabled":           true,
			"clusterResourceId": badClusterID,
		}
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		_, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.Error(t, err)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "error", first.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", first.Attributes["agent.hosting.byo_aks"])
		assertNoResourceLeak(t, capture.events, badClusterID)
	})

	t.Run("byo aks invalid managed identity id", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		provider.synthResult.Parameters["agentHosting"] = map[string]any{
			"enabled":                             true,
			"clusterResourceId":                   offlineClusterID,
			"hostingManagementIdentityResourceId": invalidIdentityID,
		}
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		_, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.Error(t, err)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "error", first.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", first.Attributes["agent.hosting.byo_aks"])
		assertNoResourceLeak(t, capture.events)
	})

	t.Run("byo aks preflight failure", func(t *testing.T) {
		provider, fake := offlineBYOProvider(t)
		fake.clusterVersion = "1.27.0"
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		_, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.Error(t, err)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "error", first.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", first.Attributes["agent.hosting.byo_aks"])
		assertNoResourceLeak(t, capture.events, "1.27.0")
	})

	t.Run("byo aks model policy failure before prepareBYO", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		const policyProjectID = "/subscriptions/00000000-0000-0000-0000-000000000001" +
			"/resourceGroups/target-rg/providers/Microsoft.CognitiveServices/accounts/cog-native/projects/project"
		provider.existingProjectID = policyProjectID
		provider.synthResult.Parameters["deployments"] = []synthesis.Deployment{{Name: "gpt-4o"}}
		provider.modelPolicyCheck = func(
			context.Context, azcore.TokenCredential, string, []synthesis.Deployment, map[string]string,
		) error {
			return errors.New("policy check rejected secret-model")
		}
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		_, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.ErrorContains(t, err, "policy check rejected")

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "error", first.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", first.Attributes["agent.hosting.byo_aks"])
		assertNoResourceLeak(t, capture.events, "secret-model", policyProjectID)
	})

	t.Run("ordinary foundry project omits byo aks", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		synthResult, err := synthesis.Synthesize(synthesis.Input{
			ServiceName: "project",
			RawAzureYAML: []byte(
				"services:\n  project:\n    host: azure.ai.project\n    monitoring:\n      enabled: false\n",
			),
		})
		require.NoError(t, err)
		provider.synthResult = synthResult
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		result, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "success", first.Attributes["agent.phase.outcome"])
		_, marked := first.Attributes["agent.hosting.byo_aks"]
		require.False(t, marked)
	})

	t.Run("on-disk parameters override enabled false omits byo aks", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		infraDir := filepath.Join(provider.projectPath, "infra")
		require.NoError(t, os.MkdirAll(infraDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.bicep"), []byte("// stub\n"), 0o600))
		paramsJSON, err := json.Marshal(map[string]any{
			"parameters": map[string]any{
				"monitoringMode": map[string]any{"value": "none"},
				"agentHosting": map[string]any{
					"value": map[string]any{
						"enabled":           false,
						"clusterResourceId": "",
					},
				},
			},
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.parameters.json"), paramsJSON, 0o600))
		rawTemplate, err := synthesis.ARMTemplate()
		require.NoError(t, err)
		provider.bicepCliInstance = &stubCompiler{
			buildResult: bicep.BuildResult{Compiled: string(rawTemplate)},
		}
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client
		t.Setenv(synthesis.BYOAKSEnvVar, "")
		require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))

		result, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "success", first.Attributes["agent.phase.outcome"])
		_, marked := first.Attributes["agent.hosting.byo_aks"]
		require.False(t, marked)
	})

	t.Run("on-disk parameters enabled true marks byo aks", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		synthResult, err := synthesis.Synthesize(synthesis.Input{
			ServiceName: "project",
			RawAzureYAML: []byte(
				"services:\n  project:\n    host: azure.ai.project\n    monitoring:\n      enabled: false\n",
			),
		})
		require.NoError(t, err)
		provider.synthResult = synthResult
		infraDir := filepath.Join(provider.projectPath, "infra")
		require.NoError(t, os.MkdirAll(infraDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.bicep"), []byte("// stub\n"), 0o600))
		paramsJSON, err := json.Marshal(map[string]any{
			"parameters": map[string]any{
				"monitoringMode": map[string]any{"value": "none"},
				"agentHosting": map[string]any{
					"value": map[string]any{
						"enabled":           true,
						"clusterResourceId": offlineClusterID,
					},
				},
			},
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.parameters.json"), paramsJSON, 0o600))
		rawTemplate, err := synthesis.ARMTemplate()
		require.NoError(t, err)
		provider.bicepCliInstance = &stubCompiler{
			buildResult: bicep.BuildResult{Compiled: string(rawTemplate)},
		}
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client
		t.Setenv(synthesis.BYOAKSEnvVar, "")
		require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))

		_, err = provider.Deploy(provisionTimingContext(t), func(string) {})
		requireBYOFeatureNotEnabled(t, err)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "error", first.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", first.Attributes["agent.hosting.byo_aks"])
		assertNoResourceLeak(t, capture.events)
	})

	t.Run("malformed agentHosting envelope omits byo aks", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		provider.onDiskSource = &templateSource{
			mode:        templateModeBicep,
			sourcePath:  filepath.Join(provider.projectPath, "infra", "main.bicep"),
			armTemplate: provider.armTemplate,
			parameters: map[string]any{
				"monitoringMode": map[string]any{"value": "none"},
				"agentHosting":   map[string]any{"value": "not-an-object"},
			},
		}
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		_, err := provider.Deploy(provisionTimingContext(t), func(string) {})
		require.Error(t, err)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.NotEmpty(t, capture.events)
		first := capture.events[0]
		require.Equal(t, "error", first.Attributes["agent.phase.outcome"])
		_, marked := first.Attributes["agent.hosting.byo_aks"]
		require.False(t, marked)
	})

	t.Run("preview emits no phase telemetry", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		client, capture := newProvisionTimingClient(t, codes.OK)
		provider.azdClient = client

		result, err := provider.Preview(provisionTimingContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.Empty(t, capture.events)
	})
}
