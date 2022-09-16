// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// subareaProvider defines the base behavior from any pipeline provider
type subareaProvider interface {
	// requiredTools return the list of requires external tools required by the provider.
	requiredTools() []tools.ExternalTool
	// preConfigureCheck validates that the provider's state is ready to be used.
	// a provider would typically use this method for checking if tools are logged in
	// of checking if all expected input data is found.
	preConfigureCheck(ctx context.Context, console input.Console) error
	// name returns the name of the provider
	name() string
}

// gitRepositoryDetails provides a common abstraction across all scm providers.
// each provider implements the code to extract this fields from a remote url.
type gitRepositoryDetails struct {
	// Repository owner
	owner string
	// Repository name
	repoName string
	// System path where the git project is
	gitProjectPath string
}

// ScmProvider defines the base behavior for a source control manager provider.
type ScmProvider interface {
	// compose the behavior from subareaProvider
	subareaProvider
	// gitRepoDetails extracts the common abstraction gitRepositoryDetails fields from a remoteUrl
	gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error)
	// configureGitRemote makes sure that the remoteName is created and added to the git project.
	// The provider can use the console to interact with the user and define how to get or create a remote url
	// to set as the value for the remote name.
	configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error)
	// preventGitPush is used as a mechanism to stop a push code petition from user in case something
	// some scenario is found which indicates a failure triggering the CI pipeline.
	preventGitPush(
		ctx context.Context,
		gitRepo *gitRepositoryDetails,
		remoteName string,
		branchName string,
		console input.Console) (bool, error)
}

// CiProvider defines the base behavior for a continuous integration provider.
type CiProvider interface {
	// compose the behavior from subareaProvider
	subareaProvider
	// configurePipeline set up or create the CI pipeline.
	configurePipeline(ctx context.Context) error
	// configureConnection use the credential to set up the connection from the pipeline
	// to Azure
	configureConnection(
		ctx context.Context,
		azdEnvironment *environment.Environment,
		gitRepo *gitRepositoryDetails,
		provisioningProvider provisioning.Options,
		credential json.RawMessage,
		console input.Console) error
}
