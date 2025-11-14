// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	azure_armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	azd_armmsi "github.com/azure/azure-dev/pkg/armmsi"
	azd_github "github.com/azure/azure-dev/pkg/tools/github"
)

var _ azdMSIService = &azd_armmsi.ArmMsiService{}

// azdMSIService is an interface over [azd_armmsi.ArmMsiService], for testing.
type azdMSIService interface {
	ApplyFederatedCredentials(ctx context.Context,
		subscriptionId, msiResourceId string,
		federatedCredentials []azure_armmsi.FederatedIdentityCredential) ([]azure_armmsi.FederatedIdentityCredential, error)
	//nolint:lll
	CreateUserIdentity(
		ctx context.Context,
		subscriptionId, resourceGroup, location, name string,
	) (azure_armmsi.Identity, error)
	ListUserIdentities(
		ctx context.Context, subscriptionId string) ([]azure_armmsi.Identity, error)
}

// resourceService is an interface over [*armresources.ResourceGroupsClient], for testing
type resourceService interface {
	CreateOrUpdate(ctx context.Context,
		resourceGroupName string,
		parameters armresources.ResourceGroup,
		options *armresources.ResourceGroupsClientCreateOrUpdateOptions,
	) (armresources.ResourceGroupsClientCreateOrUpdateResponse, error)
}

// githubCLI is an interface over [azd_github.Cli], for testing
type githubCLI interface {
	CreateEnvironmentIfNotExist(ctx context.Context, repoName string, envName string) error
	GetAuthStatus(ctx context.Context, hostname string) (azd_github.AuthStatus, error)
	Login(ctx context.Context, hostname string) error
	//nolint:lll
	SetVariable(
		ctx context.Context,
		repoSlug string,
		name string,
		value string,
		options *azd_github.SetVariableOptions,
	) error
}

// gitCLI is an interface over [internalGitCLI], for testing
type gitCLI interface {
	AddFile(ctx context.Context, repositoryPath string, filespec string) error
	Commit(ctx context.Context, repositoryPath string, message string) error
	GetRemoteUrl(ctx context.Context, repositoryPath string, remoteName string) (string, error)
	ListRemotes(ctx context.Context, repositoryPath string) ([]string, error)
	PushUpstream(ctx context.Context, repositoryPath string, origin string, branch string) error
}
