// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package typescript

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
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
	mockPrompter := &mockPrompter{}
	provider := NewTypeScriptProvider(envManager, env, mockContext.Console, mockPrompter)
	require.NoError(t, provider.Initialize(*mockContext.Context, ".", provisioning.Options{}))
	return provider.(*TypeScriptProvider)
}

type mockEnvManager struct{}

func (m *mockEnvManager) Save(ctx context.Context, env *environment.Environment) error {
	return nil
}

func (m *mockEnvManager) SaveWithOptions(ctx context.Context, env *environment.Environment, options *environment.SaveOptions) error {
	return nil
}

func (m *mockEnvManager) Get(ctx context.Context, name string) (*environment.Environment, error) {
	return environment.NewWithValues(name, nil), nil
}

func (m *mockEnvManager) Reload(ctx context.Context, env *environment.Environment) error {
	return nil
}

func (m *mockEnvManager) EnvPath(env *environment.Environment) string {
	return ""
}

func (m *mockEnvManager) ConfigPath(env *environment.Environment) string {
	return ""
}

func (m *mockEnvManager) List(ctx context.Context) ([]*environment.Description, error) {
	return nil, nil
}

func (m *mockEnvManager) Delete(ctx context.Context, name string) error {
	return nil
}

func (m *mockEnvManager) Create(ctx context.Context, spec environment.Spec) (*environment.Environment, error) {
	return environment.NewWithValues(spec.Name, nil), nil
}

func (m *mockEnvManager) LoadOrInitInteractive(ctx context.Context, name string) (*environment.Environment, error) {
	return environment.NewWithValues(name, nil), nil
}

type mockPrompter struct{}

func (m *mockPrompter) PromptSubscription(ctx context.Context, message string) (string, error) {
	return "00000000-0000-0000-0000-000000000000", nil
}

func (m *mockPrompter) PromptLocation(
	ctx context.Context,
	subscriptionId string,
	message string,
	filter prompt.LocationFilterPredicate,
	defaultValue *string,
) (string, error) {
	return "westus2", nil
}

func (m *mockPrompter) PromptResourceGroup(ctx context.Context, options prompt.PromptResourceOptions) (string, error) {
	return "test-rg", nil
}

func (m *mockPrompter) PromptResourceGroupFrom(ctx context.Context, subscriptionId string, message string, options prompt.PromptResourceGroupFromOptions) (string, error) {
	return "test-rg", nil
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
	result, err := provider.Destroy(*mockContext.Context, provisioning.DestroyOptions{})
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
		name            string
		projectPath     string
		expectInfraPath string
		expectDistPath  string
	}{
		{
			name:            "Project root path",
			projectPath:     "/path/to/project",
			expectInfraPath: "/path/to/project/infra",
			expectDistPath:  "/path/to/project/infra/dist",
		},
		{
			name:            "Infra directory path",
			projectPath:     "/path/to/project/infra",
			expectInfraPath: "/path/to/project/infra",
			expectDistPath:  "/path/to/project/infra/dist",
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
