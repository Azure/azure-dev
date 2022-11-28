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
	tests := []struct {
		name string
		at   *containerAppTarget
		want string
	}{
		{"Default", &containerAppTarget{
			env: environment.EphemeralWithValues("dev", map[string]string{}),
			config: &ServiceConfig{
				Name: "web",
				Host: "containerapp",
				Project: &ProjectConfig{
					Name: "my-app",
				},
			},
			clock: mockClock},
			fmt.Sprintf("my-app/web-dev:azdev-deploy-%d", mockClock.Now().Unix())},
		{"ImageNameSpecified", &containerAppTarget{
			env: environment.EphemeralWithValues("dev", map[string]string{}),
			config: &ServiceConfig{
				Name: "web",
				Host: "containerapp",
				Docker: DockerProjectOptions{
					ImageName: "contoso-image",
				},
				Project: &ProjectConfig{
					Name: "my-app",
				},
			},
			clock: mockClock},
			fmt.Sprintf("contoso-image:azdev-deploy-%d", mockClock.Now().Unix())},
		{"ImageTagSpecified", &containerAppTarget{
			env: environment.EphemeralWithValues("dev", map[string]string{}),
			config: &ServiceConfig{
				Name: "web",
				Host: "containerapp",
				Docker: DockerProjectOptions{
					ImageTag: "latest",
				},
				Project: &ProjectConfig{
					Name: "my-app",
				},
			},
			clock: mockClock},
			"my-app/web-dev:latest"},
		{"ImageNameAndTagSpecified", &containerAppTarget{
			env: environment.EphemeralWithValues("dev", map[string]string{}),
			config: &ServiceConfig{
				Name: "web",
				Host: "containerapp",
				Docker: DockerProjectOptions{
					ImageName: "contoso-image",
					ImageTag:  "latest",
				},
				Project: &ProjectConfig{
					Name: "my-app",
				},
			},
			clock: mockClock},
			"contoso-image:latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag := tt.at.generateImageTag()
			assert.Equal(t, tt.want, tag)
		})
	}
}
