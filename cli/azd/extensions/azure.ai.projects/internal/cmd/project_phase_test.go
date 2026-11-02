// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
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

func TestProjectAuthoringCommandEmitsInternalSteps(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	for _, test := range []struct {
		name       string
		noPrompt   bool
		failSave   bool
		deployment bool
	}{
		{name: "Init delegated headless", noPrompt: true},
		{name: "interactive project add"},
		{name: "configuration failure", noPrompt: true, failSave: true},
		{name: "delegated deployment authoring", noPrompt: true, deployment: true},
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
			azdext.RegisterPromptServiceServer(server, projects)
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
			}
			if test.noPrompt {
				args = append(args, "--no-prompt")
			}
			cmd.SetArgs(args)
			err = cmd.ExecuteContext(t.Context())
			if test.failSave {
				require.ErrorContains(t, err, "environment write failed")
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
			}
			require.Equal(t, expected, complete.Attributes["agent.phase.outcome"])
			require.Equal(t, parent, reporting.parent)
			ids := map[string]bool{}
			for _, event := range reporting.events[1:] {
				require.Equal(t, "agent.phase.step", event.EventName)
				require.Equal(t, complete.Attributes["agent.phase.attempt_id"], event.Attributes["agent.phase.attempt_id"])
				ids[event.Attributes["agent.phase.step.id"]] = true
				for _, value := range event.Attributes {
					require.NotContains(t, value, "private-sub")
					require.NotContains(t, value, root)
				}
			}
			require.True(t, ids["project_resolution"])
			require.True(t, ids["configuration_save"])
			if test.deployment {
				require.True(t, ids["azure_query"])
				require.True(t, ids["deployment_reconciliation"])
			}
			if !test.noPrompt {
				require.True(t, ids["subscription_selection"])
				require.True(t, ids["location_selection"])
			}
		})
	}
}
