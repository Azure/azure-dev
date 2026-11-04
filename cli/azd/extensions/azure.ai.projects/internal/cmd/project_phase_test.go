// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"azure.ai.projects/internal/phasetelemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type projectPhaseServer struct {
	azdext.UnimplementedTelemetryServiceServer
	mu     sync.Mutex
	events []*azdext.ReportUsageRequest
	parent string
}

func (s *projectPhaseServer) ReportUsage(
	ctx context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, req)
	md, _ := metadata.FromIncomingContext(ctx)
	s.parent = strings.Join(md.Get("traceparent"), "")
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

type projectPhasePromptServer struct {
	*selfInitializingProjectServer
	locationCalls atomic.Int32
	locationErr   error
}

func (s *projectPhasePromptServer) PromptLocation(
	ctx context.Context, req *azdext.PromptLocationRequest,
) (*azdext.PromptLocationResponse, error) {
	s.locationCalls.Add(1)
	if s.locationErr != nil {
		return nil, s.locationErr
	}
	return s.selfInitializingProjectServer.PromptLocation(ctx, req)
}

func TestProjectAuthoringCommandEmitsInternalSteps(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	for _, test := range []struct {
		name        string
		noPrompt    bool
		failSave    bool
		deployment  bool
		existing    bool
		locationErr error
		errorText   string
		outcome     string
		projectID   string
		endpoint    string
		wantQuery   bool
	}{
		{name: "Init delegated headless", noPrompt: true},
		{name: "interactive project add"},
		{name: "configuration failure", noPrompt: true, failSave: true},
		{name: "delegated deployment authoring", noPrompt: true, deployment: true},
		{name: "interactive deployment location", deployment: true, existing: true},
		{name: "headless deployment existing location", noPrompt: true, deployment: true, existing: true},
		{
			name: "invalid explicit project ID", noPrompt: true, projectID: "invalid-resource-id",
			errorText: "project ID must be a Microsoft.CognitiveServices project resource ID", outcome: "error",
		},
		{
			name: "valid project ID lookup failure", noPrompt: true, wantQuery: true,
			projectID: "/subscriptions/private-sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/account/projects/project",
			errorText: "failed to lookup tenant", outcome: "error",
		},
		{
			name: "endpoint-only project add", noPrompt: true,
			endpoint: "https://account.services.ai.azure.com/api/projects/project",
		},
		{
			name: "deployment location canceled", deployment: true, existing: true,
			locationErr: status.Error(codes.Canceled, "selection canceled"),
			errorText:   "Azure location selection was cancelled", outcome: "canceled",
		},
		{
			name: "deployment location failure", deployment: true, existing: true,
			locationErr: status.Error(codes.Unavailable, "picker unavailable"),
			errorText:   "select Azure location", outcome: "error",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			t.Setenv("TRACEPARENT", parent)
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
			projects := &selfInitializingProjectServer{
				project: &azdext.ProjectConfig{
					Name: "test", Path: root, Services: map[string]*azdext.ServiceConfig{},
				},
				services:     map[string]map[string]any{},
				subscription: &azdext.Subscription{Id: "private-sub", UserTenantId: "user-tenant"},
				location:     &azdext.Location{Name: "eastus"},
			}
			if test.existing {
				projects.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
				projects.services["project"] = map[string]any{}
			}
			prompts := &projectPhasePromptServer{
				selfInitializingProjectServer: projects, locationErr: test.locationErr,
			}
			env := &selfInitializingEnvironmentServer{current: "dev", values: map[string]string{}}
			if test.noPrompt {
				env.values["AZURE_SUBSCRIPTION_ID"] = "private-sub"
				env.values["AZURE_LOCATION"] = "eastus"
			}
			if test.failSave {
				env.failKey = "USE_EXISTING_AI_PROJECT"
			}
			reporting := &projectPhaseServer{}
			server := grpc.NewServer()
			azdext.RegisterProjectServiceServer(server, projects)
			azdext.RegisterEnvironmentServiceServer(server, env)
			azdext.RegisterPromptServiceServer(server, prompts)
			azdext.RegisterTelemetryServiceServer(server, reporting)
			azdext.RegisterAiModelServiceServer(server, &selfInitializingAIService{location: "eastus", respectRequest: true})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
			t.Setenv("AZD_SERVER", listener.Addr().String())
			cmd := NewRootCommand()
			args := []string{"add", "--new-project", "--output", "none"}
			if test.deployment {
				args = []string{"deployment", "add", "--model", "OpenAI/gpt-4.1", "--output", "none"}
			} else if test.projectID != "" {
				args = []string{"add", "--project-id", test.projectID, "--output", "none"}
			} else if test.endpoint != "" {
				args = []string{"add", "--project-endpoint", test.endpoint, "--output", "none"}
			}
			if test.noPrompt {
				args = append(args, "--no-prompt")
			}
			cmd.SetArgs(args)
			err = cmd.ExecuteContext(t.Context())
			if test.failSave {
				require.ErrorContains(t, err, "environment write failed")
			} else if test.errorText != "" {
				require.ErrorContains(t, err, test.errorText)
			} else {
				require.NoError(t, err)
			}
			reporting.mu.Lock()
			defer reporting.mu.Unlock()
			require.NotEmpty(t, reporting.events)
			complete := reporting.events[0]
			require.Equal(t, "agent.phase.completed", complete.EventName)
			name := "project_authoring"
			if test.deployment {
				name = "model_deployment_authoring"
			}
			require.Equal(t, name, complete.Attributes["agent.phase.name"])
			require.Equal(t, "command", complete.Attributes["agent.phase.scope"])
			expected := "success"
			if test.failSave {
				expected = "error"
			} else if test.outcome != "" {
				expected = test.outcome
			}
			require.Equal(t, expected, complete.Attributes["agent.phase.outcome"])
			require.Equal(t, parent, reporting.parent)
			ids := map[string]int{}
			for _, event := range reporting.events[1:] {
				require.Equal(t, "agent.phase.step", event.EventName)
				require.Equal(t, complete.Attributes["agent.phase.attempt_id"], event.Attributes["agent.phase.attempt_id"])
				id := event.Attributes["agent.phase.step.id"]
				ids[id]++
				if id == "location_selection" {
					duration, parseErr := strconv.ParseFloat(event.Attributes["agent.phase.step.duration_ms"], 64)
					require.NoError(t, parseErr)
					require.GreaterOrEqual(t, duration, float64(0))
				}
				for _, value := range event.Attributes {
					require.NotContains(t, value, "private-sub")
					require.NotContains(t, value, root)
				}
			}
			require.Positive(t, ids["project_resolution"])
			if test.errorText == "" || test.failSave {
				require.Positive(t, ids["configuration_save"])
				if test.deployment {
					require.Positive(t, ids["azure_query"])
					require.Positive(t, ids["deployment_reconciliation"])
				}
			} else {
				require.Zero(t, ids["configuration_save"])
				if test.wantQuery {
					require.Positive(t, ids["azure_query"])
				} else {
					require.Zero(t, ids["azure_query"])
				}
			}
			if !test.deployment {
				require.Zero(t, ids["deployment_reconciliation"], "ineligible project must not report reconciliation")
			}
			if !test.noPrompt {
				require.Positive(t, ids["subscription_selection"])
				require.Equal(t, 1, ids["location_selection"])
				require.Equal(t, int32(1), prompts.locationCalls.Load())
			} else {
				require.Zero(t, ids["location_selection"])
				require.Zero(t, prompts.locationCalls.Load())
			}

		})
	}
}

type projectPhaseEventReporter struct {
	events []foundryTelemetry.Event
}

func (r *projectPhaseEventReporter) Report(_ context.Context, event foundryTelemetry.Event) {
	r.events = append(r.events, event)
}

func TestAdoptedReconciliationTimingRequiresEligibleTarget(t *testing.T) {
	for _, test := range []struct {
		name     string
		target   *resolvedProject
		eligible bool
	}{
		{name: "nil target"},
		{name: "new project", target: &resolvedProject{Mode: projectModeNew}},
		{
			name: "endpoint-only project",
			target: &resolvedProject{
				Mode: projectModeExistingEndpoint, Endpoint: "https://account.services.ai.azure.com/api/projects/project",
			},
		},
		{name: "missing resource ID", target: &resolvedProject{Mode: projectModeExistingID}},
		{
			name:   "eligible project with no declarations",
			target: &resolvedProject{Mode: projectModeExistingID, ResourceId: "project-resource-id"}, eligible: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter := &projectPhaseEventReporter{}
			ctx, timing := phasetelemetry.Begin(
				t.Context(), phasetelemetry.ProjectAuthoring, phasetelemetry.Command, reporter,
			)
			var client *azdext.AzdClient
			root := t.TempDir()
			if test.eligible {
				client, _, _, _, _ = newSelfInitializingDeploymentClient(t, root)
			}
			deployment, changed, restore, err := reconcileAdoptedDeployments(
				ctx, client, root, "test", test.target, "project", true,
			)
			timing.End(ctx, err)
			require.NoError(t, err)
			require.Empty(t, deployment)
			require.False(t, changed)
			require.NotNil(t, restore)
			require.NoError(t, restore())
			require.NotEmpty(t, reporter.events)
			require.Equal(t, "agent.phase.completed", reporter.events[0].Name)
			require.Equal(t, "success", reporter.events[0].Attributes["agent.phase.outcome"])
			if test.eligible {
				require.Len(t, reporter.events, 2)
				require.Equal(t, "deployment_reconciliation", reporter.events[1].Attributes["agent.phase.step.id"])
			} else {
				require.Len(t, reporter.events, 1, "ineligible guards must skip all reconciliation work and timing")
			}
		})
	}
}
