// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning_test

import (
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/require"
)

func TestEnsureConfiguredInitializesEnvironment(t *testing.T) {
	env := environment.EphemeralWithValues("test-env", nil)
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Please select an Azure Subscription to use")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		// Select the first from the list
		return 0, nil
	})
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Please select an Azure location")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		// Select the first from the list
		return 0, nil
	})

	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	mgr, err := NewManager(
		*mockContext.Context,
		env,
		"",
		options,
		interactive,
		azCli,
		mockContext.Console,
		mockContext.CommandRunner,
		&mockaccount.MockAccountManager{
			Subscriptions: []account.Subscription{
				{
					Id:   "00000000-0000-0000-0000-000000000000",
					Name: "test",
				},
			},
			Locations: []account.Location{
				{
					Name:                "location",
					DisplayName:         "Test Location",
					RegionalDisplayName: "(US) Test Location",
				},
			},
		},
		azcli.NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			mockContext.HttpClient,
		),
		&mockSubscriptionTenantResolver{},
	)
	require.NoError(t, err)

	err = mgr.EnsureConfigured(*mockContext.Context)
	require.NoError(t, err)

	require.Equal(t, "00000000-0000-0000-0000-000000000000", env.GetSubscriptionId())
	require.Equal(t, "location", env.GetLocation())
}

func TestManagerPlan(t *testing.T) {
	env := environment.EphemeralWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
		"AZURE_PRINCIPAL_ID":    "PRINCIPAL_ID",
	})
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	mgr, err := NewManager(
		*mockContext.Context,
		env,
		"",
		options,
		interactive,
		azCli,
		mockContext.Console,
		mockContext.CommandRunner,
		&mockaccount.MockAccountManager{},
		azcli.NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			mockContext.HttpClient,
		),
		&mockSubscriptionTenantResolver{},
	)
	require.NoError(t, err)

	err = mgr.EnsureConfigured(*mockContext.Context)
	require.NoError(t, err)

	deploymentPlan, err := mgr.Plan(*mockContext.Context)

	require.NotNil(t, deploymentPlan)
	require.Nil(t, err)
	require.Equal(t, deploymentPlan.Deployment.Parameters["location"].Value, env.Values["AZURE_LOCATION"])
}

func TestManagerGetState(t *testing.T) {
	env := environment.EphemeralWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
		"AZURE_PRINCIPAL_ID":    "PRINCIPAL_ID",
	})
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	mgr, err := NewManager(
		*mockContext.Context,
		env,
		"",
		options,
		interactive,
		azCli,
		mockContext.Console,
		mockContext.CommandRunner,
		&mockaccount.MockAccountManager{},
		azcli.NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			mockContext.HttpClient,
		),
		&mockSubscriptionTenantResolver{},
	)
	require.NoError(t, err)

	err = mgr.EnsureConfigured(*mockContext.Context)
	require.NoError(t, err)

	provisioningScope := infra.NewSubscriptionScope(
		azCli,
		"eastus2",
		env.GetSubscriptionId(),
		env.GetEnvName(),
	)
	getResult, err := mgr.State(*mockContext.Context, provisioningScope)

	require.NotNil(t, getResult)
	require.Nil(t, err)
}

func TestManagerDeploy(t *testing.T) {
	env := environment.EphemeralWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
		"AZURE_PRINCIPAL_ID":    "PRINCIPAL_ID",
	})
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	mgr, err := NewManager(
		*mockContext.Context,
		env,
		"",
		options,
		interactive,
		azCli,
		mockContext.Console,
		mockContext.CommandRunner,
		&mockaccount.MockAccountManager{},
		azcli.NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			mockContext.HttpClient,
		),
		&mockSubscriptionTenantResolver{},
	)
	require.NoError(t, err)

	err = mgr.EnsureConfigured(*mockContext.Context)
	require.NoError(t, err)

	deploymentPlan, _ := mgr.Plan(*mockContext.Context)
	provisioningScope := infra.NewSubscriptionScope(
		azCli,
		"eastus2",
		env.GetSubscriptionId(),
		env.GetEnvName(),
	)
	deployResult, err := mgr.Deploy(*mockContext.Context, deploymentPlan, provisioningScope)

	require.NotNil(t, deployResult)
	require.Nil(t, err)
}

func TestManagerDestroyWithPositiveConfirmation(t *testing.T) {
	env := environment.EphemeralWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
		"AZURE_PRINCIPAL_ID":    "PRINCIPAL_ID",
	})
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(true)

	mgr, err := NewManager(
		*mockContext.Context, env, "", options, interactive, azCli,
		mockContext.Console,
		mockContext.CommandRunner,
		&mockaccount.MockAccountManager{},
		azcli.NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			mockContext.HttpClient,
		),
		&mockSubscriptionTenantResolver{},
	)
	require.NoError(t, err)

	err = mgr.EnsureConfigured(*mockContext.Context)
	require.NoError(t, err)

	deploymentPlan, _ := mgr.Plan(*mockContext.Context)
	destroyOptions := NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, &deploymentPlan.Deployment, destroyOptions)

	require.NotNil(t, destroyResult)
	require.Nil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}

func TestManagerDestroyWithNegativeConfirmation(t *testing.T) {
	env := environment.EphemeralWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
		"AZURE_PRINCIPAL_ID":    "PRINCIPAL_ID",
	})
	options := Options{Provider: "test"}
	interactive := false

	mockContext := mocks.NewMockContext(context.Background())
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Are you sure you want to destroy?")
	}).Respond(false)

	mgr, err := NewManager(
		*mockContext.Context,
		env,
		"",
		options,
		interactive,
		azCli,
		mockContext.Console,
		mockContext.CommandRunner,
		&mockaccount.MockAccountManager{},
		azcli.NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			mockContext.HttpClient,
		),
		&mockSubscriptionTenantResolver{},
	)
	require.NoError(t, err)

	err = mgr.EnsureConfigured(*mockContext.Context)
	require.NoError(t, err)

	deploymentPlan, _ := mgr.Plan(*mockContext.Context)
	destroyOptions := NewDestroyOptions(false, false)
	destroyResult, err := mgr.Destroy(*mockContext.Context, &deploymentPlan.Deployment, destroyOptions)

	require.Nil(t, destroyResult)
	require.NotNil(t, err)
	require.Contains(t, mockContext.Console.Output(), "Are you sure you want to destroy?")
}

type mockSubscriptionTenantResolver struct {
}

func (m *mockSubscriptionTenantResolver) LookupTenant(
	ctx context.Context, subscriptionId string) (tenantId string, err error) {
	return "00000000-0000-0000-0000-000000000000", nil
}
