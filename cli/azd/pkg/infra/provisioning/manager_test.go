// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestManagerPreview(t *testing.T) {
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	mgr, _ := NewManager(*mockContext.Context, env, "", options, interactive)

	previewResult, err := mgr.Preview(*mockContext.Context)

	require.NotNil(t, previewResult)
	require.Nil(t, err)
	require.Equal(t, previewResult.Deployment.Parameters["location"].Value, env.Values["AZURE_LOCATION"])
}

func TestManagerGetDeployment(t *testing.T) {
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	mgr, _ := NewManager(*mockContext.Context, env, "", options, interactive)

	provisioningScope := NewSubscriptionScope(*mockContext.Context, "eastus2", env.GetSubscriptionId(), env.GetEnvName())
	getResult, err := mgr.GetDeployment(*mockContext.Context, provisioningScope)

	require.NotNil(t, getResult)
	require.Nil(t, err)
}

func TestManagerDeploy(t *testing.T) {
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	mgr, _ := NewManager(*mockContext.Context, env, "", options, interactive)

	previewResult, _ := mgr.Preview(*mockContext.Context)
	provisioningScope := NewSubscriptionScope(*mockContext.Context, "eastus2", env.GetSubscriptionId(), env.GetEnvName())
	deployResult, err := mgr.Deploy(*mockContext.Context, &previewResult.Deployment, provisioningScope)

	require.NotNil(t, deployResult)
	require.Nil(t, err)
}

func TestManagerDestroyWithPositiveConfirmation(t *testing.T) {
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(true)

	mgr, _ := NewManager(*mockContext.Context, env, "", options, interactive)

	previewResult, _ := mgr.Preview(*mockContext.Context)
	destroyOptions := NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, &previewResult.Deployment, destroyOptions)

	require.NotNil(t, destroyResult)
	require.Nil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}

func TestManagerDestroyWithNegativeConfirmation(t *testing.T) {
	env := environment.Environment{Values: make(map[string]string)}
	env.Values["AZURE_LOCATION"] = "eastus2"
	env.SetEnvName("test-env")
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(false)

	mgr, _ := NewManager(*mockContext.Context, env, "", options, interactive)

	previewResult, _ := mgr.Preview(*mockContext.Context)
	destroyOptions := NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, &previewResult.Deployment, destroyOptions)

	require.Nil(t, destroyResult)
	require.NotNil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}
