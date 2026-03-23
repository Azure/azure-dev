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
		name            string
		services        []*ServiceConfig
		toolErr         *tools.MissingToolErrors
		serviceFilterFn ServiceFilterPredicate
		wantSuggestion  bool
		wantContains    string
	}{
		{
			name: "ContainerApp_without_remoteBuild_suggests",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api",
		},
		{
			name: "AKS_without_remoteBuild_suggests",
			services: []*ServiceConfig{
				{Name: "worker", Host: AksTarget},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "worker",
		},
		{
			name: "Multiple_services_lists_all",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget},
				{Name: "web", Host: ContainerAppTarget},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api, web",
		},
		{
			name: "ContainerApp_with_remoteBuild_no_suggestion",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget, Docker: DockerProjectOptions{RemoteBuild: true}},
			},
			toolErr:        dockerMissing,
			wantSuggestion: false,
		},
		{
			name: "AppService_no_suggestion",
			services: []*ServiceConfig{
				{Name: "web", Host: AppServiceTarget},
			},
			toolErr:        dockerMissing,
			wantSuggestion: false,
		},
		{
			name: "Non_Docker_tool_missing_no_suggestion",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget},
			},
			toolErr:        bicepMissing,
			wantSuggestion: false,
		},
		{
			name: "Mixed_services_only_suggests_container_targets",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget},
				{Name: "web", Host: AppServiceTarget},
				{Name: "worker", Host: AksTarget},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api, worker",
		},
		{
			name: "Service_filter_excludes_service",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget},
				{Name: "worker", Host: ContainerAppTarget},
			},
			toolErr: dockerMissing,
			serviceFilterFn: func(svc *ServiceConfig) bool {
				return svc.Name == "api"
			},
			wantSuggestion: true,
			wantContains:   "api",
		},
		{
			name: "DotNet_without_Dockerfile_no_suggestion",
			services: func() []*ServiceConfig {
				useDotNet := true
				return []*ServiceConfig{
					{
						Name:                           "api",
						Host:                           ContainerAppTarget,
						Language:                       ServiceLanguageCsharp,
						useDotNetPublishForDockerBuild: &useDotNet,
					},
				}
			}(),
			toolErr:        dockerMissing,
			wantSuggestion: false,
		},
		{
			name: "Docker_not_running_suggests_start",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget},
			},
			toolErr:        dockerNotRunning,
			wantSuggestion: true,
			wantContains:   "start your container runtime",
		},
		{
			name: "Docker_not_installed_suggests_install",
			services: []*ServiceConfig{
				{Name: "api", Host: ContainerAppTarget},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "install Docker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := suggestRemoteBuild(tt.services, tt.toolErr, tt.serviceFilterFn)

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
