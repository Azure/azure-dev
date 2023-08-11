// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// subareaProvider defines the base behavior from any pipeline provider
type subareaProvider interface {
	// requiredTools return the list of requires external tools required by the provider.
	requiredTools(ctx context.Context) ([]tools.ExternalTool, error)
	// preConfigureCheck validates that the provider's state is ready to be used.
	// a provider would typically use this method for checking if tools are logged in
	// of checking if all expected input data is found.
	// The returned configurationWasUpdated indicates if the current settings were updated during the check,
	// for example, if Azdo prompt for a PAT or OrgName to the user and updated.
	preConfigureCheck(
		ctx context.Context,
		pipelineManagerArgs PipelineManagerArgs,
		infraOptions provisioning.Options,
		projectPath string,
	) (bool, error)
	// Name returns the Name of the provider
	Name() string
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
	//Indicates if the repo was successfully pushed a remote
	pushStatus bool
	// remote is the git-remote, which can be in ssh or https format
	remote string
	// url holds the remote url regardless if the remote is an ssh or https string
	url string
	// branch
	branch string

	details interface{}
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
	configureGitRemote(ctx context.Context, repoPath string, remoteName string) (string, error)
	// preventGitPush is used as a mechanism to stop a push code petition from user in case something
	// some scenario is found which indicates a failure triggering the CI pipeline.
	preventGitPush(
		ctx context.Context,
		gitRepo *gitRepositoryDetails,
		remoteName string,
		branchName string) (bool, error)
	//Hook function to allow SCM providers to handle scenarios after the git push is complete
	GitPush(ctx context.Context,
		gitRepo *gitRepositoryDetails,
		remoteName string,
		branchName string) error
}

// CiPipeline provides the functional contract for a CI/CD provider to define getting the pipeline name and the url to
// access the pipeline.
type CiPipeline interface {
	// name returns a string label that represents the pipeline identifier.
	name() string
	// url provides the web address to access the pipeline.
	url() string
}

// CiProvider defines the base behavior for a continuous integration provider.
type CiProvider interface {
	// compose the behavior from subareaProvider
	subareaProvider
	// configurePipeline set up or create the CI pipeline and return information about it
	configurePipeline(
		ctx context.Context,
		repoDetails *gitRepositoryDetails,
		provisioningProvider provisioning.Options,
	) (CiPipeline, error)
	// configureConnection use the credential to set up the connection from the pipeline
	// to Azure
	configureConnection(
		ctx context.Context,
		gitRepo *gitRepositoryDetails,
		provisioningProvider provisioning.Options,
		credential json.RawMessage,
		authType PipelineAuthType,
	) error
}

func folderExists(folderPath string) bool {
	if _, err := os.Stat(folderPath); err == nil {
		return true
	}
	return false
}

func ymlExists(ymlPath string) bool {
	info, err := os.Stat(ymlPath)
	// if it is a file with no error
	if err == nil && info.Mode().IsRegular() {
		return true
	}
	return false
}

const (
	gitHubLabel     string = "github"
	azdoLabel       string = "azdo"
	envPersistedKey string = "AZD_PIPELINE_PROVIDER"
)

var (
	githubFolder string = filepath.Join(".github", "workflows")
	azdoFolder   string = filepath.Join(".azdo", "pipelines")
	azdoYml      string = filepath.Join(azdoFolder, "azure-dev.yml")
)
