// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestNewSpringAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				string(infra.AzureResourceTypeSpringApp),
			),
			expectError: false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				strings.ToLower(string(infra.AzureResourceTypeSpringApp)),
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
			mockContext := mocks.NewMockContext(context.Background())
			serviceTarget := &springAppTarget{}
			serviceConfig := &ServiceConfig{}

			err := serviceTarget.validateTargetResource(*mockContext.Context, serviceConfig, data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExtractEndpoint(t *testing.T) {
	serviceTarget := &springAppTarget{}

	assert.Equal(t, "https://mock-appname.azuremicroservices.io",
		serviceTarget.extractEndpoint("https://mock.azuremicroservices.io", "appname"))

	assert.NotEqual(t, "https://mock.azuremicroservices.io",
		serviceTarget.extractEndpoint("https://mock.azuremicroservices.io", "appname"))
}
