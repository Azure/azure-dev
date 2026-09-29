// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/stretchr/testify/require"
)

func TestSuggestRemoteBuildErrorAttribution(t *testing.T) {
	unavailable := &docker.ContainerEngineUnavailableError{
		Engine: tools.ContainerEngineDocker,
		Err:    errors.New("permission denied"),
	}
	tests := []struct {
		name       string
		err        *tools.MissingToolErrors
		wantAdvice string
	}{
		{
			name: "wrapped readiness error",
			err: &tools.MissingToolErrors{
				ToolNames: []string{"Docker"},
				Errs:      []error{fmt.Errorf("checking tool: %w", unavailable)},
			},
			wantAdvice: "running and accessible",
		},
		{
			name: "legacy external runtime",
			err: &tools.MissingToolErrors{
				ToolNames: []string{"Docker"},
				Errs:      []error{errors.New("Docker is not running")},
			},
			wantAdvice: "running and accessible",
		},
		{
			name: "unrelated tool must not select runtime advice",
			err: &tools.MissingToolErrors{
				ToolNames: []string{"Docker", "other"},
				Errs:      []error{errors.New("Docker is not installed"), errors.New("other service is not running")},
			},
			wantAdvice: "install Docker",
		},
		{
			name: "canceled probe has no runtime advice",
			err: &tools.MissingToolErrors{
				ToolNames: []string{"Docker"}, Errs: []error{fmt.Errorf("checking tool: %w", context.Canceled)},
			},
		},
		{
			name: "expired probe has no runtime advice",
			err: &tools.MissingToolErrors{
				ToolNames: []string{"Docker"}, Errs: []error{context.DeadlineExceeded},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			suggestion := suggestRemoteBuild([]svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, tools: []tools.ExternalTool{&failingTool{toolName: "Docker"}}},
				{svc: &ServiceConfig{Name: "unaffected"}, tools: []tools.ExternalTool{&failingTool{toolName: "Podman"}}},
			}, tt.err)
			if tt.wantAdvice == "" {
				require.Nil(t, suggestion)
				return
			}
			require.NotNil(t, suggestion)
			require.Contains(t, suggestion.Suggestion, tt.wantAdvice)
			require.Contains(t, suggestion.Suggestion, "Services [api]")
			require.NotContains(t, suggestion.Suggestion, "unaffected")
			require.Same(t, tt.err, suggestion.Err)
		})
	}
}
