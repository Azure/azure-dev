// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_suggestRemoteBuild(t *testing.T) {
	dockerMissing := &tools.MissingToolErrors{
		ToolNames: []string{"Docker"},
		Errs:      []error{fmt.Errorf("neither docker nor podman is installed")},
	}
	dockerNotRunning := &tools.MissingToolErrors{
		ToolNames: []string{"Docker"},
		Errs:      []error{fmt.Errorf("the Docker service is not running, please start it")},
	}
	bicepMissing := &tools.MissingToolErrors{
		ToolNames: []string{"bicep"},
		Errs:      []error{assert.AnError},
	}

	tests := []struct {
		name           string
		svcTools       []svcToolInfo
		toolErr        *tools.MissingToolErrors
		wantSuggestion bool
		wantContains   string
	}{
		{
			name: "Service_needing_Docker_suggests",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api",
		},
		{
			name: "Multiple_services_lists_all",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
				{svc: &ServiceConfig{Name: "web"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api, web",
		},
		{
			name: "Service_not_needing_Docker_no_suggestion",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: false},
			},
			toolErr:        dockerMissing,
			wantSuggestion: false,
		},
		{
			name: "Non_Docker_tool_missing_no_suggestion",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        bicepMissing,
			wantSuggestion: false,
		},
		{
			name: "Mixed_services_only_Docker_ones",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
				{svc: &ServiceConfig{Name: "web"}, needsDocker: false},
				{svc: &ServiceConfig{Name: "worker"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api, worker",
		},
		{
			name: "Docker_not_running_suggests_start",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        dockerNotRunning,
			wantSuggestion: true,
			wantContains:   "start your container runtime",
		},
		{
			name: "Docker_not_installed_suggests_install",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "install Docker",
		},
		{
			name:           "Empty_services_no_suggestion",
			svcTools:       []svcToolInfo{},
			toolErr:        dockerMissing,
			wantSuggestion: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := suggestRemoteBuild(tt.svcTools, tt.toolErr)

			if !tt.wantSuggestion {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			assert.Contains(t, result.Suggestion, tt.wantContains)
			assert.Contains(t, result.Suggestion, "remoteBuild")
		})
	}
}
