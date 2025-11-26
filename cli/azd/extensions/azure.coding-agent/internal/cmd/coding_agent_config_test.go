// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	azure_armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	rm_armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	exec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	azd_github "github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

//go:generate go tool mockgen -destination mocks_internal_test.go -copyright_file ./testdata/mock_copyright.txt -package cmd . githubCLI,resourceService,azdMSIService,gitCLI

//go:generate go tool mockgen -destination mocks_azdext_test.go -copyright_file ./testdata/mock_copyright.txt -package cmd github.com/azure/azure-dev/cli/azd/pkg/azdext PromptServiceClient

//go:generate go tool mockgen -destination mocks_azd_entraid.go -copyright_file ./testdata/mock_copyright.txt -package cmd github.com/azure/azure-dev/cli/azd/pkg/entraid EntraIdService

//go:generate go tool mockgen -destination mocks_azdexec_test.go -copyright_file ./testdata/mock_copyright.txt -package cmd github.com/azure/azure-dev/cli/azd/pkg/exec CommandRunner

//go:generate go tool mockgen -destination mocks_azdinput_test.go -copyright_file ./testdata/mock_copyright.txt -package cmd github.com/azure/azure-dev/cli/azd/pkg/input Console

const repoSlugForTests = "richardpark-msft/copilot-auth-tests"

func TestCodingAgent_setCopilotEnvVars(t *testing.T) {
	ctrl := gomock.NewController(t)
	githubCLI := NewMockgithubCLI(ctrl)

	githubCLI.EXPECT().
		CreateEnvironmentIfNotExist(gomock.Any(), repoSlugForTests, "copilot").
		Return(nil)

	githubCLI.EXPECT().
		SetVariable(gomock.Any(), repoSlugForTests, "AZURE_CLIENT_ID", "client-id", GitHubEnvMatcher{}).
		Return(nil)

	githubCLI.EXPECT().
		SetVariable(gomock.Any(), repoSlugForTests, "AZURE_TENANT_ID", "tenant-id", GitHubEnvMatcher{}).
		Return(nil)

	githubCLI.EXPECT().
		//nolint:lll
		SetVariable(gomock.Any(), repoSlugForTests, "AZURE_SUBSCRIPTION_ID", "subscription-id", GitHubEnvMatcher{}).
		Return(nil)

	err := setCopilotEnvVars(context.Background(), githubCLI, repoSlugForTests, authConfiguration{
		ClientId:       "client-id",
		SubscriptionId: "subscription-id",
		TenantId:       "tenant-id",
		ResourceID:     "resource-id",
	})
	require.NoError(t, err)
}

func TestCodingAgent_createFederatedCredential(t *testing.T) {
	ctrl := gomock.NewController(t)
	msiService := NewMockazdMSIService(ctrl)

	const subscriptionID = "00000000-0000-0000-0000-000000000000"
	//nolint:lll
	const msiResourceID = "/subscriptions/" + subscriptionID + "/these-are-a-few-of-my-favorite-things/providers/Microsoft.ManagedIdentity/userAssignedIdentities/msi-copilot-azd-starter"

	msiService.EXPECT().ApplyFederatedCredentials(gomock.Any(),
		subscriptionID,
		msiResourceID,
		FedCredentialMatcher{
			T: t,
			Expected: azure_armmsi.FederatedIdentityCredential{
				Name: to.Ptr("richardpark-msft-copilot-auth-tests-copilot-env"),
				Properties: &azure_armmsi.FederatedIdentityCredentialProperties{
					Subject:   to.Ptr("repo:" + repoSlugForTests + ":environment:copilot-hi"),
					Issuer:    to.Ptr("https://token.actions.githubusercontent.com"),
					Audiences: []*string{to.Ptr("api://AzureADTokenExchange")},
				},
			},
		},
	)

	err := createFederatedCredential(context.Background(),
		msiService,
		repoSlugForTests,
		"copilot-hi",
		subscriptionID,
		msiResourceID)
	require.NoError(t, err)
}

func TestCodingAgent_promptForRepoSlug(t *testing.T) {
	t.Run("repoSlugFromCommandLineDoesntPrompt", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		promptClient := NewMockPromptServiceClient(ctrl)
		gitCLI := NewMockgitCLI(ctrl)

		slug, err := promptForCodingAgentRepoSlug(context.Background(), promptClient, gitCLI, "repo-root-ignored", repoSlugForTests) //nolint:lll

		require.Equal(t, slug, repoSlugForTests)
		require.NoError(t, err)
	})

	t.Run("noSlugShouldPrompt", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		promptClient := NewMockPromptServiceClient(ctrl)
		gitCLI := NewMockgitCLI(ctrl)

		gitCLI.EXPECT().ListRemotes(gomock.Any(), "repo-root-used").Return([]string{"origin", "upstream"}, nil)
		//nolint:lll
		gitCLI.EXPECT().GetRemoteUrl(gomock.Any(), "repo-root-used", "origin").Return("https://github.com/richardpark-msft/tawnygardenslug", nil)
		//nolint:lll
		gitCLI.EXPECT().GetRemoteUrl(gomock.Any(), "repo-root-used", "upstream").Return("https://github.com/slugs/tawnygardenslug", nil)

		promptClient.EXPECT().Select(gomock.Any(), gomock.Any()).Return(&azdext.SelectResponse{
			// simulate they chose option 1
			Value: to.Ptr(int32(1)),
		}, nil)

		slug, err := promptForCodingAgentRepoSlug(context.Background(), promptClient, gitCLI, "repo-root-used", "")

		require.Equal(t, slug, "slugs/tawnygardenslug")
		require.NoError(t, err)
	})
}

func TestCodingAgent_loginToGitHubIfNeeded(t *testing.T) {
	t.Run("LoggedIn", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ghCLIMock := NewMockgithubCLI(ctrl)

		ghCLIMock.EXPECT().GetAuthStatus(gomock.Any(), "example.com").Return(azd_github.AuthStatus{
			LoggedIn: true,
		}, nil)

		err := loginToGitHubIfNeeded(context.Background(), "example.com",
			func(showOutput bool) (exec.CommandRunner, input.Console) {
				require.True(t, showOutput, "we must allow showing output for `gh auth login`")

				// unused for the actual test.
				return NewMockCommandRunner(ctrl), NewMockConsole(ctrl)
			},
			func(_ context.Context, _ input.Console, _ exec.CommandRunner) (githubCLI, error) {
				return ghCLIMock, nil
			})
		require.NoError(t, err)
	})

	t.Run("NotLoggedIn", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ghCLIMock := NewMockgithubCLI(ctrl)

		ghCLIMock.EXPECT().GetAuthStatus(gomock.Any(), "example.com").Return(azd_github.AuthStatus{
			LoggedIn: false,
		}, nil)

		ghCLIMock.EXPECT().Login(gomock.Any(), "example.com").Return(nil)

		err := loginToGitHubIfNeeded(context.Background(), "example.com",
			func(showOutput bool) (exec.CommandRunner, input.Console) {
				require.True(t, showOutput, "we must allow showing output for `gh auth login`")

				// unused for the actual test.
				return NewMockCommandRunner(ctrl), NewMockConsole(ctrl)
			},
			func(_ context.Context, _ input.Console, _ exec.CommandRunner) (githubCLI, error) {
				return ghCLIMock, nil
			})
		require.NoError(t, err)
	})
}

func TestCodingAgent_pickOrCreateMSI(t *testing.T) {
	const fakeSubscriptionID = "fake-subscription-id"
	const fakeResourceGroup = "fake-resource-group"
	const fakeLocation = "fake-location"
	const fakeTenantID = "tenant-id"
	const fakeClientID = "client-id"

	setup := func(t *testing.T) (*MockPromptServiceClient, *MockazdMSIService, *MockEntraIdService, *MockresourceService) {
		ctrl := gomock.NewController(t)
		promptService := NewMockPromptServiceClient(ctrl)
		msiService := NewMockazdMSIService(ctrl)
		entraService := NewMockEntraIdService(ctrl)
		resourceService := NewMockresourceService(ctrl)

		promptService.EXPECT().
			PromptLocation(gomock.Any(), gomock.Any()).
			Return(&azdext.PromptLocationResponse{
				Location: &azdext.Location{
					Name: fakeLocation,
				},
			}, nil).
			AnyTimes()

		promptService.EXPECT().
			PromptResourceGroup(gomock.Any(), gomock.Any()).
			Return(&azdext.PromptResourceGroupResponse{
				ResourceGroup: &azdext.ResourceGroup{
					Name: fakeResourceGroup,
				},
			}, nil).
			AnyTimes()

		return promptService, msiService, entraService, resourceService
	}

	t.Run("UsingDefaultIdentity_createNewIdentity", func(t *testing.T) {
		// the simplest "create" flow - they're going to create a new identity, using all the defaults.
		promptService, msiService, entraService, resourceService := setup(t)

		promptService.EXPECT().Select(gomock.Any(),
			SelectMatcher{T: t, ExpectedQuestion: "Do you want to create a new Azure user-assigned managed identity or use an existing one?"}).
			Return(&azdext.SelectResponse{
				Value: to.Ptr(int32(0)), // ie, "create a new one"
			}, nil)

		msiService.EXPECT().
			CreateUserIdentity(gomock.Any(), fakeSubscriptionID, fakeResourceGroup, fakeLocation, defaultManagedIdentityName).
			Return(azure_armmsi.Identity{
				Properties: &azure_armmsi.UserAssignedIdentityProperties{
					PrincipalID: to.Ptr("principal-id"),
					ClientID:    to.Ptr(fakeClientID),
					TenantID:    to.Ptr(fakeTenantID),
				},
				Name: to.Ptr(string(defaultManagedIdentityName)),
				ID:   to.Ptr(fmt.Sprintf("/subscriptions/%s/resourcegroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s", fakeSubscriptionID, fakeResourceGroup, defaultManagedIdentityName)),
			}, nil)

		entraService.EXPECT().EnsureRoleAssignments(gomock.Any(),
			fakeSubscriptionID,
			[]string{"Reader"},
			&graphsdk.ServicePrincipal{
				Id:          to.Ptr("principal-id"),
				DisplayName: defaultManagedIdentityName,
			}, gomock.Any()).
			Return(nil)

		authConfig, err := pickOrCreateMSI(context.Background(),
			promptService,
			msiService,
			entraService,
			resourceService,
			defaultManagedIdentityName,
			fakeSubscriptionID,
			[]string{"Reader"})
		require.NoError(t, err)

		require.Equal(t, fakeClientID, authConfig.ClientId)
		require.Equal(t, defaultManagedIdentityName, authConfig.Name)
		require.Equal(t, fakeTenantID, authConfig.TenantId)
		require.Equal(t, fakeResourceGroup, authConfig.ResourceGroup)
	})

	t.Run("UsingCustomManagedIdentityNameAndRoles_assumesCreate", func(t *testing.T) {
		// similar to the create flow above, except that specifying a custom name for the identity
		// automaticaly chooses the "create" path.
		promptService, msiService, entraService, resourceService := setup(t)
		const customManagedIdentityName = "identity-name"

		msiService.EXPECT().
			CreateUserIdentity(gomock.Any(), fakeSubscriptionID, fakeResourceGroup, fakeLocation, customManagedIdentityName).
			Return(azure_armmsi.Identity{
				Properties: &azure_armmsi.UserAssignedIdentityProperties{
					PrincipalID: to.Ptr("principal-id"),
					ClientID:    to.Ptr(fakeClientID),
					TenantID:    to.Ptr(fakeTenantID),
				},
				Name: to.Ptr(customManagedIdentityName),
				ID:   to.Ptr(fmt.Sprintf("/subscriptions/%s/resourcegroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s", fakeSubscriptionID, fakeResourceGroup, customManagedIdentityName)),
			}, nil)

		entraService.EXPECT().EnsureRoleAssignments(gomock.Any(), fakeSubscriptionID, []string{"custom-role-name"}, &graphsdk.ServicePrincipal{
			Id:          to.Ptr("principal-id"),
			DisplayName: customManagedIdentityName,
		}, gomock.Any()).
			Return(nil)

		authConfig, err := pickOrCreateMSI(context.Background(),
			promptService,
			msiService,
			entraService,
			resourceService,
			customManagedIdentityName,
			fakeSubscriptionID,
			[]string{"custom-role-name"})
		require.NoError(t, err)

		require.Equal(t, fakeClientID, authConfig.ClientId)
		require.Equal(t, customManagedIdentityName, authConfig.Name)
		require.Equal(t, fakeTenantID, authConfig.TenantId)
		require.Equal(t, fakeResourceGroup, authConfig.ResourceGroup)
	})

	t.Run("UsingDefaultIdentity_useExisting", func(t *testing.T) {
		// uses an existing identity - no identity should be created!
		promptService, msiService, entraService, resourceService := setup(t)

		promptService.EXPECT().Select(gomock.Any(),
			SelectMatcher{T: t, ExpectedQuestion: "Do you want to create a new Azure user-assigned managed identity or use an existing one?"}).
			Return(&azdext.SelectResponse{
				Value: to.Ptr(int32(1)), // ie, "use an existing one"
			}, nil)

		msiService.EXPECT().
			ListUserIdentities(gomock.Any(), fakeSubscriptionID).
			Return([]rm_armmsi.Identity{
				{
					Properties: &azure_armmsi.UserAssignedIdentityProperties{
						PrincipalID: to.Ptr("principal-id"),
						ClientID:    to.Ptr(fakeClientID),
						TenantID:    to.Ptr(fakeTenantID),
					},
					Name:     to.Ptr(string(defaultManagedIdentityName)),
					ID:       to.Ptr(fmt.Sprintf("/subscriptions/%s/resourcegroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s", fakeSubscriptionID, fakeResourceGroup, defaultManagedIdentityName)),
					Location: to.Ptr(fakeLocation),
				},
			}, nil)

		promptService.EXPECT().
			Select(gomock.Any(), SelectMatcher{T: t, ExpectedQuestion: "Select an existing User Managed Identity (MSI) to use"}).
			Return(&azdext.SelectResponse{
				Value: to.Ptr(int32(0)), // pick the first ID in the list (also, the only ID I returned)
			}, nil)

		entraService.EXPECT().EnsureRoleAssignments(gomock.Any(),
			fakeSubscriptionID,
			[]string{"Reader"},
			&graphsdk.ServicePrincipal{
				Id:          to.Ptr("principal-id"),
				DisplayName: defaultManagedIdentityName,
			}, gomock.Any()).
			Return(nil)

		authConfig, err := pickOrCreateMSI(context.Background(),
			promptService,
			msiService,
			entraService,
			resourceService,
			defaultManagedIdentityName,
			fakeSubscriptionID,
			[]string{"Reader"})
		require.NoError(t, err)

		require.Equal(t, fakeClientID, authConfig.ClientId)
		require.Equal(t, defaultManagedIdentityName, authConfig.Name)
		require.Equal(t, fakeTenantID, authConfig.TenantId)
		require.Equal(t, fakeResourceGroup, authConfig.ResourceGroup)
	})
}

type FedCredentialMatcher struct {
	T        *testing.T
	Expected azure_armmsi.FederatedIdentityCredential
}

func (m FedCredentialMatcher) Matches(x any) bool {
	creds := x.([]azure_armmsi.FederatedIdentityCredential)
	require.Equal(m.T, creds[0], m.Expected)
	return true
}

func (m FedCredentialMatcher) String() string { return "Checks federated credentials" }

type GitHubEnvMatcher struct{}

func (m GitHubEnvMatcher) Matches(x any) bool {
	switch options := x.(type) {
	case *azd_github.SetVariableOptions:
		return options.Environment == "copilot"
	default:
		return false
	}
}

// String describes what the matcher matches.
func (m GitHubEnvMatcher) String() string { return "Checks copilot env was specified" }

type SelectMatcher struct {
	T                *testing.T
	ExpectedQuestion string
}

func (m SelectMatcher) Matches(x any) bool {
	req := x.(*azdext.SelectRequest)
	require.Equal(m.T, m.ExpectedQuestion, req.Options.Message)
	return true
}

func (m SelectMatcher) String() string {
	return "Checks that the select prompt was the one we're expecting"
}
