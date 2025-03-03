// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
)

func Test_StandardDeployments_GenerateDeploymentName(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Clock.Set(time.Unix(1683303710, 0))

	deploymentService := NewStandardDeployments(
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
		NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions),
		cloud.AzurePublic(),
		mockContext.Clock,
	)

	tcs := []struct {
		envName  string
		expected string
	}{
		{
			envName:  "simple-name",
			expected: "simple-name-1683303710",
		},
		{
			envName:  "azd-template-test-apim-todo-csharp-sql-swa-func-2750207-2",
			expected: "template-test-apim-todo-csharp-sql-swa-func-2750207-2-1683303710",
		},
	}

	for _, tc := range tcs {
		deploymentName := deploymentService.GenerateDeploymentName(tc.envName)
		assert.Equal(t, tc.expected, deploymentName)
		assert.LessOrEqual(t, len(deploymentName), 64)
	}
}
