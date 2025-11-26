// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestServiceFiltering(t *testing.T) {
	tests := []struct {
		name              string
		services          []*project.ServiceConfig
		targetServiceName string
		expectedServices  int
	}{
		{
			name: "AllServicesNoFilter",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget},
				{Name: "web", Host: project.ContainerAppTarget},
			},
			targetServiceName: "",
			expectedServices:  2,
		},
		{
			name: "MixedServicesNoFilter",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget},
				{Name: "web", Host: project.AppServiceTarget},
				{Name: "worker", Host: project.DotNetContainerAppTarget},
			},
			targetServiceName: "",
			expectedServices:  3,
		},
		{
			name: "FilterByTargetService",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget},
				{Name: "web", Host: project.AppServiceTarget},
				{Name: "worker", Host: project.ContainerAppTarget},
			},
			targetServiceName: "api",
			expectedServices:  1,
		},
		{
			name:              "EmptyServiceList",
			services:          []*project.ServiceConfig{},
			targetServiceName: "",
			expectedServices:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var servicesToDeploy []*project.ServiceConfig

			for _, svc := range tt.services {
				// Skip this service if both cases are true:
				// 1. The user specified a service name
				// 2. This service is not the one the user specified
				if tt.targetServiceName != "" && tt.targetServiceName != svc.Name {
					continue
				}
				servicesToDeploy = append(servicesToDeploy, svc)
			}

			require.Equal(t, tt.expectedServices, len(servicesToDeploy),
				"Expected %d services, got %d", tt.expectedServices, len(servicesToDeploy))
		})
	}
}

func TestServiceDependencyDetection(t *testing.T) {
	tests := []struct {
		name             string
		services         []*project.ServiceConfig
		hasDependencies  bool
	}{
		{
			name: "NoDependencies",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget, Uses: []string{}},
				{Name: "web", Host: project.ContainerAppTarget, Uses: []string{}},
			},
			hasDependencies: false,
		},
		{
			name: "WithServiceDependencies",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget, Uses: []string{}},
				{Name: "web", Host: project.ContainerAppTarget, Uses: []string{"api"}},
			},
			hasDependencies: true,
		},
		{
			name: "OnlyResourceDependencies",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget, Uses: []string{"postgresdb"}},
				{Name: "web", Host: project.ContainerAppTarget, Uses: []string{"redis"}},
			},
			hasDependencies: false, // postgresdb and redis are not services
		},
		{
			name: "MixedDependencies",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget, Uses: []string{"postgresdb"}},
				{Name: "web", Host: project.ContainerAppTarget, Uses: []string{"api"}},
			},
			hasDependencies: true, // web depends on api service
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build service map
			serviceMap := make(map[string]*project.ServiceConfig)
			for _, svc := range tt.services {
				serviceMap[svc.Name] = svc
			}

			// Check for dependencies
			hasDependencies := false
			for _, svc := range tt.services {
				if len(svc.Uses) > 0 {
					for _, dep := range svc.Uses {
						if _, isService := serviceMap[dep]; isService {
							hasDependencies = true
							break
						}
					}
				}
				if hasDependencies {
					break
				}
			}

			require.Equal(t, tt.hasDependencies, hasDependencies,
				"Expected hasDependencies=%v, got %v", tt.hasDependencies, hasDependencies)
		})
	}
}
