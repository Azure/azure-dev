// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
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
	//Indicates if the repo was successfully pushed a remote
	pushStatus bool

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
	configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error)
	// preventGitPush is used as a mechanism to stop a push code petition from user in case something
	// some scenario is found which indicates a failure triggering the CI pipeline.
	preventGitPush(
		ctx context.Context,
		gitRepo *gitRepositoryDetails,
		remoteName string,
		branchName string,
		console input.Console) (bool, error)
	//Hook function to allow SCM providers to handle scenarios after the git push is complete
	postGitPush(ctx context.Context,
		gitRepo *gitRepositoryDetails,
		remoteName string,
		branchName string,
		console input.Console) error
}

// CiProvider defines the base behavior for a continuous integration provider.
type CiProvider interface {
	// compose the behavior from subareaProvider
	subareaProvider
	// configurePipeline set up or create the CI pipeline.
	configurePipeline(ctx context.Context, repoDetails *gitRepositoryDetails, provisioningProvider provisioning.Options) error
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

func folderExists(folderPath string) bool {
	if _, err := os.Stat(folderPath); err == nil {
		return true
	}
	return false
}

// DetectProviders get azd context from the context and pulls the project directory from it.
// Depending on the project directory, returns pipeline scm and ci providers based on:
// - if .github folder is found and .azdo folder is missing: GitHub scm and ci as provider
// - if .azdo folder is found and .github folder is missing: Azdo scm and ci as provider
// - both .github and .azdo folders found: prompt user to choose provider for scm and ci
// - none of the folders found: return error
// - no azd context in the ctx: return error
func DetectProviders(ctx context.Context, console input.Console, env *environment.Environment) (ScmProvider, CiProvider, error) {
	azdContext, err := azdcontext.GetAzdContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	projectDir := azdContext.ProjectDirectory()

	hasGitHubFolder := folderExists(path.Join(projectDir, ".github"))
	hasAzDevOpsFolder := folderExists(path.Join(projectDir, ".azdo"))

	if !hasGitHubFolder && !hasAzDevOpsFolder {
		return nil, nil, fmt.Errorf("no CI/CD provider configuration found. Expecting either .github and/or .azdo folder in the project root directory")
	}

	if !hasAzDevOpsFolder && hasGitHubFolder {
		// GitHub only
		return &GitHubScmProvider{}, &GitHubCiProvider{}, nil
	}

	if hasAzDevOpsFolder && !hasGitHubFolder {
		// Azdo only
		return createAzdoScmProvider(env, azdContext), createAzdoCiProvider(env, azdContext), nil
	}

	// Both folders exist. Prompt to select SCM first
	scmElection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Select what SCM provider to use",
		Options: []string{
			"GitHub",
			"Azure DevOps",
		},
		DefaultValue: "GitHub",
	})

	if err != nil {
		return nil, nil, err
	}

	if scmElection == 1 {
		// using azdo for scm would only support using azdo pipelines
		return createAzdoScmProvider(env, azdContext), createAzdoCiProvider(env, azdContext), nil
	}

	// GitHub selected for SCM, prompt for CI provider
	ciElection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Select what CI provider to use",
		Options: []string{
			"GitHub Actions",
			"Azure DevOps Pipelines",
		},
		DefaultValue: "GitHub Actions",
	})

	if err != nil {
		return nil, nil, err
	}

	if ciElection == 0 {
		return &GitHubScmProvider{}, &GitHubCiProvider{}, nil
	}

	// GitHub plus azdo pipelines otherwise
	return &GitHubScmProvider{}, createAzdoCiProvider(env, azdContext), nil
}

func createAzdoCiProvider(env *environment.Environment, azdCtx *azdcontext.AzdContext) *AzdoCiProvider {
	return &AzdoCiProvider{
		Env:        env,
		AzdContext: azdCtx,
	}
}

func createAzdoScmProvider(env *environment.Environment, azdCtx *azdcontext.AzdContext) *AzdoHubScmProvider {
	return &AzdoHubScmProvider{
		Env:        env,
		AzdContext: azdCtx,
	}
}
