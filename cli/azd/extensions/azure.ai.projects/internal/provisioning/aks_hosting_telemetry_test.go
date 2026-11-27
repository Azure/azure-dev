// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS kubelet traceparent

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type hostingTelemetryServer struct {
	azdext.UnimplementedTelemetryServiceServer
	azdext.UnimplementedEnvironmentServiceServer
	mu      sync.Mutex
	events  []*azdext.ReportUsageRequest
	parents []string
}

func (s *hostingTelemetryServer) ReportUsage(
	ctx context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, req)
	var parent string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("traceparent"); len(values) > 0 {
			parent = values[0]
		}
	}
	s.parents = append(s.parents, parent)
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

func (s *hostingTelemetryServer) GetValue(
	context.Context, *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{}, nil
}

func (s *hostingTelemetryServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	return &azdext.KeyValueListResponse{}, nil
}

func (s *hostingTelemetryServer) SetValue(
	context.Context, *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	return &azdext.EmptyResponse{}, nil
}

func (s *hostingTelemetryServer) SetConfig(
	context.Context, *azdext.SetConfigRequest,
) (*azdext.EmptyResponse, error) {
	return &azdext.EmptyResponse{}, nil
}

func (s *hostingTelemetryServer) GetConfig(
	context.Context, *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	return &azdext.GetConfigResponse{}, nil
}

func newHostingTelemetryClient(t *testing.T) (*azdext.AzdClient, *hostingTelemetryServer) {
	t.Helper()
	capture := &hostingTelemetryServer{}
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

const hostingTelemetryTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func hostingTelemetryContext(t *testing.T) context.Context {
	t.Helper()
	ctx := propagation.TraceContext{}.Extract(
		t.Context(),
		propagation.MapCarrier{"traceparent": hostingTelemetryTraceParent},
	)
	return azdext.WithAccessToken(ctx, "test-token")
}

func TestProvisionProviderBYOAKSHostingTelemetry(t *testing.T) {
	const invalidIdentityID = "/subscriptions/id-sub/resourceGroups/id-rg/providers/Microsoft.Compute/disks/bad-id"

	assertEvents := func(
		t *testing.T,
		capture *hostingTelemetryServer,
		wantHostingSelected bool,
		wantPhaseOutcome string,
		forbidden ...string,
	) {
		t.Helper()
		capture.mu.Lock()
		defer capture.mu.Unlock()

		payload := fmt.Sprint(capture.events)
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

		var hostingEvents []*azdext.ReportUsageRequest
		var phaseEvents []*azdext.ReportUsageRequest
		for i, event := range capture.events {
			require.Equal(t, hostingTelemetryTraceParent, capture.parents[i])
			switch event.EventName {
			case "agent.hosting.selected":
				hostingEvents = append(hostingEvents, event)
			case "agent.phase.completed":
				phaseEvents = append(phaseEvents, event)
				for key := range event.Attributes {
					require.False(
						t,
						strings.HasPrefix(key, "agent.hosting."),
						"unexpected hosting attribute %q on agent.phase.completed",
						key,
					)
				}
			default:
				for key := range event.Attributes {
					require.False(
						t,
						strings.HasPrefix(key, "agent.hosting."),
						"unexpected hosting attribute %q on %s",
						key,
						event.EventName,
					)
				}
			}
		}

		require.Len(t, phaseEvents, 1)
		require.Equal(t, "provision", phaseEvents[0].Attributes["agent.phase.name"])
		require.Equal(t, "provider", phaseEvents[0].Attributes["agent.phase.scope"])
		require.Equal(t, wantPhaseOutcome, phaseEvents[0].Attributes["agent.phase.outcome"])

		if wantHostingSelected {
			require.Len(t, hostingEvents, 1)
			require.Equal(
				t,
				map[string]string{"agent.hosting.mode": "byo_aks"},
				hostingEvents[0].Attributes,
			)
		} else {
			require.Empty(t, hostingEvents)
		}
	}

	t.Run("byo aks deploy success", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		result, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		assertEvents(t, capture, true, "success")
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
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client
		t.Setenv(synthesis.BYOAKSEnvVar, "")
		require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))

		_, err = provider.Deploy(hostingTelemetryContext(t), func(string) {})
		requireBYOFeatureNotEnabled(t, err)

		assertEvents(t, capture, true, "error", refPath)
	})

	t.Run("byo aks invalid cluster id", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		const badClusterID = "invalid-cluster-resource-id"
		provider.synthResult.Parameters["agentHosting"] = map[string]any{
			"enabled":           true,
			"clusterResourceId": badClusterID,
		}
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		_, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.Error(t, err)

		assertEvents(t, capture, true, "error", badClusterID)
	})

	t.Run("byo aks invalid managed identity id", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		provider.synthResult.Parameters["agentHosting"] = map[string]any{
			"enabled":                             true,
			"clusterResourceId":                   offlineClusterID,
			"hostingManagementIdentityResourceId": invalidIdentityID,
		}
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		_, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.Error(t, err)

		assertEvents(t, capture, true, "error")
	})

	t.Run("byo aks preflight failure", func(t *testing.T) {
		provider, fake := offlineBYOProvider(t)
		fake.clusterVersion = "1.27.0"
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		_, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.Error(t, err)

		assertEvents(t, capture, true, "error", "1.27.0")
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
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		_, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.ErrorContains(t, err, "policy check rejected")

		assertEvents(t, capture, true, "error", "secret-model", policyProjectID)
	})

	t.Run("ordinary foundry project omits hosting selected", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		synthResult, err := synthesis.Synthesize(synthesis.Input{
			ServiceName: "project",
			RawAzureYAML: []byte(
				"services:\n  project:\n    host: azure.ai.project\n    monitoring:\n      enabled: false\n",
			),
		})
		require.NoError(t, err)
		provider.synthResult = synthResult
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		result, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		assertEvents(t, capture, false, "success")
	})

	t.Run("on-disk parameters override enabled false omits hosting selected", func(t *testing.T) {
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
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client
		t.Setenv(synthesis.BYOAKSEnvVar, "")
		require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))

		result, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		assertEvents(t, capture, false, "success")
	})

	t.Run("on-disk parameters enabled true emits hosting selected", func(t *testing.T) {
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
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client
		t.Setenv(synthesis.BYOAKSEnvVar, "")
		require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))

		_, err = provider.Deploy(hostingTelemetryContext(t), func(string) {})
		requireBYOFeatureNotEnabled(t, err)

		assertEvents(t, capture, true, "error")
	})

	t.Run("malformed agentHosting envelope omits hosting selected", func(t *testing.T) {
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
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		_, err := provider.Deploy(hostingTelemetryContext(t), func(string) {})
		require.Error(t, err)

		assertEvents(t, capture, false, "error")
	})

	t.Run("preview emits no usage events", func(t *testing.T) {
		provider, _ := offlineBYOProvider(t)
		client, capture := newHostingTelemetryClient(t)
		provider.azdClient = client

		result, err := provider.Preview(hostingTelemetryContext(t), func(string) {})
		require.NoError(t, err)
		require.NotNil(t, result)

		capture.mu.Lock()
		defer capture.mu.Unlock()
		require.Empty(t, capture.events)
	})
}
