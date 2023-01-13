// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContainerAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	t.Run("ValidateTypeSuccess", func(t *testing.T) {
		_, err := NewContainerAppTarget(
			nil,
			nil,
			environment.NewTargetResource("SUB_ID", "RG_ID", "res", string(infra.AzureResourceTypeContainerApp)),
			nil,
			nil,
			nil,
			nil,
		)

		require.NoError(t, err)
	})

	t.Run("ValidateTypeLowerCaseSuccess", func(t *testing.T) {
		_, err := NewContainerAppTarget(
			nil,
			nil,
			environment.NewTargetResource(
				"SUB_ID", "RG_ID", "res", strings.ToLower(string(infra.AzureResourceTypeContainerApp)),
			),
			nil,
			nil,
			nil,
			nil,
		)

		require.NoError(t, err)
	})

	t.Run("ValidateTypeFail", func(t *testing.T) {
		_, err := NewContainerAppTarget(
			nil,
			nil,
			environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			nil,
			nil,
			nil,
			nil,
		)

		require.Error(t, err)
	})
}

func Test_containerAppTarget_generateImageTag(t *testing.T) {
	mockClock := clock.NewMock()
	envName := "dev"
	projectName := "my-app"
	serviceName := "web"
	defaultImageName := fmt.Sprintf("%s/%s-%s", projectName, serviceName, envName)
	tests := []struct {
		name         string
		dockerConfig DockerProjectOptions
		want         string
	}{
		{"Default",
			DockerProjectOptions{},
			fmt.Sprintf("%s:azdev-deploy-%d", defaultImageName, mockClock.Now().Unix())},
		{"ImageTagSpecified",
			DockerProjectOptions{
				Tag: NewExpandableString("contoso/contoso-image:latest"),
			},
			"contoso/contoso-image:latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerAppTarget := &containerAppTarget{
				env: environment.EphemeralWithValues(envName, map[string]string{}),
				config: &ServiceConfig{
					Name: serviceName,
					Host: "containerapp",
					Project: &ProjectConfig{
						Name: projectName,
					},
					Docker: tt.dockerConfig,
				},
				clock: mockClock}
			tag, err := containerAppTarget.generateImageTag()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, tag)
		})
	}
}
