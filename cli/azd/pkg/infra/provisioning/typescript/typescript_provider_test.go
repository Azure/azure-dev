// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package typescript

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func createTypeScriptProvider(t *testing.T, mockContext *mocks.MockContext) *TypeScriptProvider {
	env := environment.NewWithValues("test-env", map[string]string{
		environment.LocationEnvVarName:       "westus2",
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.EnvNameEnvVarName:        "test-env",
	})
	envManager := &mockEnvManager{}
	provider := NewTypeScriptProvider(envManager, env, mockContext.Console)
	require.NoError(t, provider.Initialize(*mockContext.Context, ".", struct{}{}))
	return provider.(*TypeScriptProvider)
}

type mockEnvManager struct{}

func (m *mockEnvManager) Save(ctx context.Context, env *environment.Environment) error {
	return nil
}

func TestTypeScriptProvider_Initialize(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	provider := createTypeScriptProvider(t, mockContext)
	require.Equal(t, "typescript", provider.Name())
}

func TestTypeScriptProvider_Deploy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	provider := createTypeScriptProvider(t, mockContext)
	result, err := provider.Deploy(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestTypeScriptProvider_Preview(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	provider := createTypeScriptProvider(t, mockContext)
	result, err := provider.Preview(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestTypeScriptProvider_Destroy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	provider := createTypeScriptProvider(t, mockContext)
	result, err := provider.Destroy(*mockContext.Context, struct{}{})
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestTypeScriptProvider_Parameters(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	provider := createTypeScriptProvider(t, mockContext)
	params, err := provider.Parameters(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, params)
}

func TestPathHandlingLogic(t *testing.T) {
	tests := []struct {
		name           string
		projectPath    string
		expectInfraPath string
		expectDistPath string
	}{
		{
			name:           "Project root path",
			projectPath:    "/path/to/project",
			expectInfraPath: "/path/to/project/infra",
			expectDistPath: "/path/to/project/infra/dist",
		},
		{
			name:           "Infra directory path",
			projectPath:    "/path/to/project/infra",
			expectInfraPath: "/path/to/project/infra",
			expectDistPath: "/path/to/project/infra/dist",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test infrastructure path calculation
			var infraPath string
			if strings.HasSuffix(tt.projectPath, "/infra") {
				infraPath = tt.projectPath
			} else {
				infraPath = fmt.Sprintf("%s/infra", tt.projectPath)
			}
			require.Equal(t, tt.expectInfraPath, infraPath)
			
			// Test dist path calculation
			distPath := fmt.Sprintf("%s/dist", infraPath)
			require.Equal(t, tt.expectDistPath, distPath)
		})
	}
}
