// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/phasetelemetry"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/recordproxy"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type phaseTelemetryServer struct {
	azdext.UnimplementedTelemetryServiceServer
	mu     sync.Mutex
	events []*azdext.ReportUsageRequest
	parent string
}

func (s *phaseTelemetryServer) ReportUsage(
	ctx context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, req)
	meta, _ := metadata.FromIncomingContext(ctx)
	s.parent = ""
	if values := meta.Get("traceparent"); len(values) > 0 {
		s.parent = values[0]
	}
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

func TestAgentProviderTimingsUseSharedUpAndStandaloneEntryPoints(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, entry := range []string{"up", "standalone"} {
		t.Run(entry, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			reporter := &phaseTelemetryServer{}
			server := grpc.NewServer()
			azdext.RegisterTelemetryServiceServer(server, reporter)
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
			config := inlineAgentService(t, map[string]any{
				"kind": "prompt", "name": "private-agent", "model": "gpt-4.1-mini", "instructions": "secret prompt",
			})
			provider := NewAgentServiceTargetProvider(client)
			progress := func(string) {}
			_, err = provider.Package(ctx, config, &azdext.ServiceContext{}, progress)
			require.NoError(t, err, "prompt packaging is an intentional no-op")
			_, err = provider.Publish(ctx, config, &azdext.ServiceContext{}, nil, nil, progress)
			require.NoError(t, err, "prompt publishing is an intentional no-op")
			_, err = provider.Deploy(ctx, config, &azdext.ServiceContext{}, nil, progress)
			require.Error(t, err, "the missing Project service must remain a deploy error")
			reporter.mu.Lock()
			defer reporter.mu.Unlock()
			names, attempts := map[string]string{}, map[string]bool{}
			for _, event := range reporter.events {
				if event.EventName == "agent.phase.completed" {
					require.Equal(t, "provider", event.Attributes["agent.phase.scope"])
					require.NotEmpty(t, event.Attributes["agent.phase.start_time"])
					require.NotEmpty(t, event.Attributes["agent.phase.end_time"])
					names[event.Attributes["agent.phase.name"]] = event.Attributes["agent.phase.outcome"]
					id := event.Attributes["agent.phase.attempt_id"]
					require.False(t, attempts[id])
					attempts[id] = true
				}
				for _, value := range event.Attributes {
					require.NotContains(t, value, "private-agent")
					require.NotContains(t, value, "secret prompt")
				}
			}
			require.Equal(t, map[string]string{"package": "success", "publish": "success", "deploy": "error"}, names)
			require.Equal(t, parent, reporter.parent)
		})
	}
}

type phaseEventReporter struct {
	events []foundryTelemetry.Event
}

func (r *phaseEventReporter) Report(_ context.Context, event foundryTelemetry.Event) {
	r.events = append(r.events, event)
}

func TestCodeArtifactUploadTimingRequiresUploadCall(t *testing.T) {
	for _, test := range []struct {
		name       string
		lookupCode int
		uploadCode int
		activity   bool
		wantUpload bool
		wantError  bool
	}{
		{name: "create", lookupCode: http.StatusNotFound, uploadCode: http.StatusOK, wantUpload: true},
		{name: "update", lookupCode: http.StatusOK, uploadCode: http.StatusOK, wantUpload: true},
		{
			name: "create failure", lookupCode: http.StatusNotFound, uploadCode: http.StatusBadRequest,
			wantUpload: true, wantError: true,
		},
		{
			name: "update failure", lookupCode: http.StatusOK, uploadCode: http.StatusBadRequest,
			wantUpload: true, wantError: true,
		},
		{name: "lookup failure", lookupCode: http.StatusForbidden, wantError: true},
		{name: "activity profile mismatch", lookupCode: http.StatusOK, activity: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			reporter := &phaseEventReporter{}
			ctx, timing := phasetelemetry.Begin(t.Context(), phasetelemetry.Deploy, phasetelemetry.Provider, reporter)
			lookups, uploads := 0, 0
			original := recordproxy.Transport
			t.Cleanup(func() { recordproxy.Transport = original })
			recordproxy.Transport = hostedResourceErrorTransport(func(req *http.Request) (*http.Response, error) {
				body := `{"name":"private-agent","versions":{"latest":{"version":"2"}}}`
				code := test.uploadCode
				switch req.Method {
				case http.MethodGet:
					lookups++
					code = test.lookupCode
					if test.activity {
						body = `{"name":"private-agent","digital_worker_type":"future-worker",` +
							`"versions":{"latest":{"version":"1"}}}`
					}
				case http.MethodPost:
					uploads++
					path := "/agents"
					if test.lookupCode == http.StatusOK {
						path += "/private-agent"
					}
					require.Equal(t, path, req.URL.Path)
					require.True(t, strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data"))
				default:
					t.Fatalf("unexpected request method %s", req.Method)
				}
				if code != http.StatusOK {
					body = `{"error":{"code":"request_failed","message":"private error details"}}`
				}
				return &http.Response{
					StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: req,
				}, nil
			})
			env := map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://example.test", "AZURE_LOCATION": "eastus"}
			provider := &AgentServiceTargetProvider{credential: hostedResourceErrorCredential{}, dependencyEnv: env}
			definition := agent_yaml.ContainerAgent{
				AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindHosted, Name: "private-agent"},
				CodeConfiguration: &agent_yaml.CodeConfiguration{
					Runtime: "python_3_13", EntryPoint: "agent.py",
				},
			}
			if test.activity {
				definition.Protocols = []agent_yaml.ProtocolVersionRecord{{Protocol: "activity", Version: "2.0.0"}}
				definition.AgentEndpoint = ComposeActivityAgentEndpoint(nil, definition.Protocols)
			}
			zipPath := filepath.Join(t.TempDir(), "agent.zip")
			require.NoError(t, os.WriteFile(zipPath, []byte("test-zip"), 0o600))
			result, err := provider.deployHostedCodeAgent(ctx, &azdext.ServiceConfig{Name: "private-service"},
				&azdext.ServiceContext{Package: []*azdext.Artifact{{
					Location: zipPath, Metadata: map[string]string{"type": "code-zip", "sha256": "test-sha"},
				}}}, func(string) {}, definition, env)
			timing.End(ctx, err)
			outcome := "success"
			if test.wantError {
				require.Error(t, err)
				outcome = "error"
			} else {
				require.NoError(t, err)
				require.NotNil(t, result.agentVersion)
				require.Equal(t, "2", result.agentVersion.Version)
			}
			require.Equal(t, 1, lookups)
			require.NotEmpty(t, reporter.events)
			require.Equal(t, "agent.phase.completed", reporter.events[0].Name)
			require.Equal(t, outcome, reporter.events[0].Attributes["agent.phase.outcome"])
			if test.wantUpload {
				require.Equal(t, 1, uploads)
				require.Len(t, reporter.events, 2)
				event := reporter.events[1]
				require.Equal(t, "agent.phase.step", event.Name)
				require.Equal(t, "artifact_upload", event.Attributes["agent.phase.step.id"])
				require.Equal(t, reporter.events[0].Attributes["agent.phase.attempt_id"], event.Attributes["agent.phase.attempt_id"])
				duration, parseErr := strconv.ParseFloat(event.Attributes["agent.phase.step.duration_ms"], 64)
				require.NoError(t, parseErr)
				require.GreaterOrEqual(t, duration, float64(0))
			} else {
				require.Zero(t, uploads)
				require.Len(t, reporter.events, 1, "preflight failure must not report an upload")
			}
			for _, event := range reporter.events {
				for _, value := range event.Attributes {
					require.NotContains(t, value, "private-agent")
					require.NotContains(t, value, "private-service")
					require.NotContains(t, value, "private error details")
					require.NotContains(t, value, zipPath)
				}
			}
		})
	}
}
