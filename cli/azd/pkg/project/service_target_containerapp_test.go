// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContainerAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", string(infra.AzureResourceTypeContainerApp)),
			expectError:    false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", strings.ToLower(string(infra.AzureResourceTypeContainerApp))),
			expectError:    false,
		},
		"ValidateTypeFail": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			expectError:    true,
		},
	}

	for test, data := range tests {
		t.Run(test, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			serviceTarget := NewContainerAppTarget(environment.Ephemeral(), nil, mockazcli.NewAzCliFromMockContext(mockContext), nil, nil, nil, nil, nil, nil)
			serviceConfig := &ServiceConfig{}

			err := serviceTarget.ValidateTargetResource(*mockContext.Context, serviceConfig, data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_containerAppTarget_generateImageTag(t *testing.T) {
	mockClock := clock.NewMock()
	envName := "dev"
	projectName := "my-app"
	serviceName := "web"
	serviceConfig := &ServiceConfig{
		Name: serviceName,
		Host: "containerapp",
		Project: &ProjectConfig{
			Name: projectName,
		},
	}
	defaultImageName := fmt.Sprintf("%s/%s-%s", projectName, serviceName, envName)

	tests := []struct {
		name         string
		dockerConfig DockerProjectOptions
		want         string
	}{
		{"Default",
			DockerProjectOptions{},
			fmt.Sprintf("%s:azd-deploy-%d", defaultImageName, mockClock.Now().Unix())},
		{"ImageTagSpecified",
			DockerProjectOptions{
				Tag: NewExpandableString("contoso/contoso-image:latest"),
			},
			"contoso/contoso-image:latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerAppTarget := &containerAppTarget{
				env:   environment.EphemeralWithValues(envName, map[string]string{}),
				clock: mockClock,
			}
			serviceConfig.Docker = tt.dockerConfig

			tag, err := containerAppTarget.generateImageTag(serviceConfig)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, tag)
		})
	}
}
