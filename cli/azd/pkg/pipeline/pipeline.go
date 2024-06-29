// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"maps"
	"path/filepath"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
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

// configurePipelineOptions holds the configuration options for the configurePipeline method.
type configurePipelineOptions struct {
	// provisioningProvider provides the information about eh project infrastructure
	provisioningProvider *provisioning.Options
	// secrets are the key-value pairs to be set as secrets in the CI provider
	secrets map[string]string
	// variables are the key-value pairs to be set as variables in the CI provider
	variables map[string]string
	// projectVariables are the keys defined on the project (azure.yaml) to be collected form the env and set it as
	// variables in the CI provider when their values are not empty.
	projectVariables []string
	// projectSecrets are the keys defined on the project (azure.yaml) to be collected form the env and set it as
	// secrets in the CI provider when their values are not empty.
	projectSecrets []string
}

// CiProvider defines the base behavior for a continuous integration provider.
type CiProvider interface {
	// compose the behavior from subareaProvider
	subareaProvider
	// configurePipeline set up or create the CI pipeline and return information about it
	configurePipeline(
		ctx context.Context,
		repoDetails *gitRepositoryDetails,
		options *configurePipelineOptions,
	) (CiPipeline, error)
	// configureConnection use the credential to set up the connection from the pipeline
	// to Azure
	configureConnection(
		ctx context.Context,
		gitRepo *gitRepositoryDetails,
		provisioningProvider provisioning.Options,
		servicePrincipal *graphsdk.ServicePrincipal,
		authType PipelineAuthType,
		credentials *entraid.AzureCredentials,
	) error
	// Gets the credential options that should be configured for the provider
	credentialOptions(
		ctx context.Context,
		repoDetails *gitRepositoryDetails,
		infraOptions provisioning.Options,
		authType PipelineAuthType,
		credentials *entraid.AzureCredentials,
	) (*CredentialOptions, error)
}

// mergeProjectVariablesAndSecrets returns the list of variables and secrets to be used in the pipeline
// The initial values reference azd known values, which are merged with the ones defined on azure.yaml by the user.
func mergeProjectVariablesAndSecrets(
	projectVariables, projectSecrets []string,
	initialVariables, initialSecrets, env map[string]string) (variables, secrets map[string]string) {
	variables = maps.Clone(initialVariables)
	secrets = maps.Clone(initialSecrets)

	for key, value := range env {
		if value == "" {
			// skip empty values
			continue
		}
		if slices.Contains(projectVariables, key) {
			variables[key] = value
		}
		if slices.Contains(projectSecrets, key) {
			secrets[key] = value
		}
	}

	return variables, secrets
}

const (
	gitHubDisplayName       string = "GitHub"
	azdoDisplayName         string = "Azure DevOps"
	gitHubLabel             string = "github"
	azdoLabel               string = "azdo"
	envPersistedKey         string = "AZD_PIPELINE_PROVIDER"
	defaultPipelineFileName string = "azure-dev.yml"
	gitHubDirectory         string = ".github"
	azdoDirectory           string = ".azdo"
)

var (
	gitHubWorkflowsDirectory string = filepath.Join(gitHubDirectory, "workflows")
	azdoPipelinesDirectory   string = filepath.Join(azdoDirectory, "pipelines")
	gitHubYml                string = filepath.Join(gitHubWorkflowsDirectory, defaultPipelineFileName)
	azdoYml                  string = filepath.Join(azdoPipelinesDirectory, defaultPipelineFileName)
)
