// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestServiceGrouping(t *testing.T) {
	tests := []struct {
		name                     string
		services                 []*project.ServiceConfig
		targetServiceName        string
		expectedContainerApps    int
		expectedNonContainerApps int
	}{
		{
			name: "AllContainerApps",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget},
				{Name: "web", Host: project.ContainerAppTarget},
			},
			targetServiceName:        "",
			expectedContainerApps:    2,
			expectedNonContainerApps: 0,
		},
		{
			name: "MixedServices",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget},
				{Name: "web", Host: project.AppServiceTarget},
				{Name: "worker", Host: project.DotNetContainerAppTarget},
			},
			targetServiceName:        "",
			expectedContainerApps:    2,
			expectedNonContainerApps: 1,
		},
		{
			name: "AllNonContainerApps",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.AppServiceTarget},
				{Name: "func", Host: project.AzureFunctionTarget},
			},
			targetServiceName:        "",
			expectedContainerApps:    0,
			expectedNonContainerApps: 2,
		},
		{
			name: "FilterByTargetService",
			services: []*project.ServiceConfig{
				{Name: "api", Host: project.ContainerAppTarget},
				{Name: "web", Host: project.AppServiceTarget},
				{Name: "worker", Host: project.ContainerAppTarget},
			},
			targetServiceName:        "api",
			expectedContainerApps:    1,
			expectedNonContainerApps: 0,
		},
		{
			name:                     "EmptyServiceList",
			services:                 []*project.ServiceConfig{},
			targetServiceName:        "",
			expectedContainerApps:    0,
			expectedNonContainerApps: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var containerAppServices []*project.ServiceConfig
			var otherServices []*project.ServiceConfig

			for _, svc := range tt.services {
				// Skip this service if both cases are true:
				// 1. The user specified a service name
				// 2. This service is not the one the user specified
				if tt.targetServiceName != "" && tt.targetServiceName != svc.Name {
					continue
				}

				// Check if this is a container app service
				if svc.Host == project.ContainerAppTarget || svc.Host == project.DotNetContainerAppTarget {
					containerAppServices = append(containerAppServices, svc)
				} else {
					otherServices = append(otherServices, svc)
				}
			}

			require.Equal(t, tt.expectedContainerApps, len(containerAppServices),
				"Expected %d container app services, got %d", tt.expectedContainerApps, len(containerAppServices))
			require.Equal(t, tt.expectedNonContainerApps, len(otherServices),
				"Expected %d non-container app services, got %d", tt.expectedNonContainerApps, len(otherServices))
		})
	}
}
