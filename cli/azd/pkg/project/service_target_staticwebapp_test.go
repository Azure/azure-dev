// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

func TestNewStaticWebAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				string(azapi.AzureResourceTypeStaticWebSite),
			),
			expectError: false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				strings.ToLower(string(azapi.AzureResourceTypeStaticWebSite)),
			),
			expectError: false,
		},
		"ValidateTypeFail": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			expectError:    true,
		},
	}

	for test, data := range tests {
		t.Run(test, func(t *testing.T) {
			serviceTarget := &staticWebAppTarget{}

			err := serviceTarget.validateTargetResource(data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestStaticWebAppOptions_ApiEnvironmentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     StaticWebAppOptions
		expected string
	}{
		{
			name:     "DefaultsToProductionBuildId",
			opts:     StaticWebAppOptions{},
			expected: DefaultStaticWebAppEnvironmentName,
		},
		{
			name:     "UsesConfiguredEnvironment",
			opts:     StaticWebAppOptions{Environment: "staging"},
			expected: "staging",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.opts.apiEnvironmentName())
		})
	}
}

func TestStaticWebAppOptions_SwaCliEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     StaticWebAppOptions
		expected string
	}{
		{
			name:     "EmptyDefaultsToProduction",
			opts:     StaticWebAppOptions{},
			expected: "production",
		},
		{
			name:     "ExplicitDefaultMapsToProduction",
			opts:     StaticWebAppOptions{Environment: "default"},
			expected: "production",
		},
		{
			name:     "ExplicitDefaultCaseInsensitive",
			opts:     StaticWebAppOptions{Environment: "Default"},
			expected: "production",
		},
		{
			name:     "WhitespaceOnlyMapsToProduction",
			opts:     StaticWebAppOptions{Environment: "  "},
			expected: "production",
		},
		{
			name:     "NamedPreviewEnvironment",
			opts:     StaticWebAppOptions{Environment: "staging"},
			expected: "staging",
		},
		{
			name:     "ProductionExplicit",
			opts:     StaticWebAppOptions{Environment: "production"},
			expected: "production",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.opts.swaCliEnvironment())
		})
	}
}

func TestStaticWebAppOptions_YamlUnmarshalWithEnvironment(t *testing.T) {
	t.Parallel()

	yamlInput := `
host: staticwebapp
project: ./src/web
staticwebapp:
  environment: staging
`
	var svc ServiceConfig
	err := yaml.Unmarshal([]byte(yamlInput), &svc)
	require.NoError(t, err)
	require.Equal(t, "staging", svc.StaticWebApp.Environment)
}

func TestStaticWebAppOptions_YamlUnmarshalNoEnvironment(t *testing.T) {
	t.Parallel()

	// When staticwebapp key is absent, Environment should be empty and production is used.
	yamlInput := `
host: staticwebapp
project: ./src/web
`
	var svc ServiceConfig
	err := yaml.Unmarshal([]byte(yamlInput), &svc)
	require.NoError(t, err)
	require.Equal(t, "", svc.StaticWebApp.Environment)
	require.Equal(t, DefaultStaticWebAppEnvironmentName, svc.StaticWebApp.apiEnvironmentName())
	require.Equal(t, "production", svc.StaticWebApp.swaCliEnvironment())
}
