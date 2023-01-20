// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// subareaProvider defines the base behavior from any pipeline provider
type subareaProvider interface {
	// requiredTools return the list of requires external tools required by the provider.
	requiredTools(ctx context.Context) []tools.ExternalTool
	// preConfigureCheck validates that the provider's state is ready to be used.
	// a provider would typically use this method for checking if tools are logged in
	// of checking if all expected input data is found.
	preConfigureCheck(
		ctx context.Context,
		console input.Console,
		pipelineManagerArgs PipelineManagerArgs,
		infraOptions provisioning.Options,
	) error
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
	configurePipeline(
		ctx context.Context,
		repoDetails *gitRepositoryDetails,
		provisioningProvider provisioning.Options,
	) error
	// configureConnection use the credential to set up the connection from the pipeline
	// to Azure
	configureConnection(
		ctx context.Context,
		azdEnvironment *environment.Environment,
		gitRepo *gitRepositoryDetails,
		provisioningProvider provisioning.Options,
		credential json.RawMessage,
		authType PipelineAuthType,
		console input.Console,
	) error
}

func folderExists(folderPath string) bool {
	if _, err := os.Stat(folderPath); err == nil {
		return true
	}
	return false
}

const (
	gitHubLabel     string = "github"
	githubFolder    string = ".github"
	azdoLabel       string = "azdo"
	azdoFolder      string = ".azdo"
	envPersistedKey string = "AZD_PIPELINE_PROVIDER"
)

// DetectProviders get azd context from the context and pulls the project directory from it.
// Depending on the project directory, returns pipeline scm and ci providers based on:
//   - if .github folder is found and .azdo folder is missing: GitHub scm and ci as provider
//   - if .azdo folder is found and .github folder is missing: Azdo scm and ci as provider
//   - both .github and .azdo folders found: GitHub scm and ci as provider
//   - overrideProvider set to github (regardless of folders): GitHub scm and ci as provider
//   - overrideProvider set to azdo (regardless of folders): Azdo scm and ci as provider
//   - none of the folders found: return error
//   - no azd context in the ctx: return error
//   - overrideProvider set to neither github or azdo: return error
//   - Note: The provider is persisted in the environment so the next time the function is run
//     the same provider is used directly, unless the overrideProvider is used to change
//     the last used configuration
func DetectProviders(
	ctx context.Context,
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
	overrideProvider string,
	console input.Console,
	credential azcore.TokenCredential,
	commandRunner exec.CommandRunner,
) (ScmProvider, CiProvider, error) {
	projectDir := azdContext.ProjectDirectory()

	// get the override value
	overrideWith := strings.ToLower(overrideProvider)

	// detecting pipeline folder configuration
	hasGitHubFolder := folderExists(path.Join(projectDir, githubFolder))
	hasAzDevOpsFolder := folderExists(path.Join(projectDir, azdoFolder))

	// Error missing config for any provider
	if !hasGitHubFolder && !hasAzDevOpsFolder {
		return nil, nil, fmt.Errorf(
			"no CI/CD provider configuration found. Expecting either %s and/or %s folder in the project root directory.",
			gitHubLabel,
			azdoLabel)
	}

	// overrideWith is the last overriding mode. When it is empty
	// we can re-assign it based on a previous run (persisted data)
	// or based on the azure.yaml
	if overrideWith == "" {
		// check if there is a persisted value from a previous run in env
		lastUsedProvider, configExists := env.Values[envPersistedKey]
		if configExists {
			// Setting override value based on last run. This will force detector to use the same
			// configuration.
			overrideWith = lastUsedProvider
		}
		// Figure out what is the expected provider to use for provisioning
		prj, err := project.LoadProjectConfig(azdContext.ProjectPath())
		if err != nil {
			return nil, nil, fmt.Errorf("finding pipeline provider: %w", err)
		}
		if prj.Pipeline.Provider != "" {
			overrideWith = prj.Pipeline.Provider
		}

	}

	// Check override errors for missing folder
	if overrideWith == gitHubLabel && !hasGitHubFolder {
		return nil, nil, fmt.Errorf("%s folder is missing. Can't use selected provider.", githubFolder)
	}
	if overrideWith == azdoLabel && !hasAzDevOpsFolder {
		return nil, nil, fmt.Errorf("%s folder is missing. Can't use selected provider.", azdoFolder)
	}
	// using wrong override value
	if overrideWith != "" && overrideWith != azdoLabel && overrideWith != gitHubLabel {
		return nil, nil, fmt.Errorf("%s is not a known pipeline provider.", overrideWith)
	}

	// At this point, we know that override value has either:
	// - github or azdo value
	// - OR is not set
	// And we know that github and azdo folders are present.
	// checking positive cases for overriding
	if overrideWith == azdoLabel || hasAzDevOpsFolder && !hasGitHubFolder {
		// Azdo only either by override or by finding only that folder
		_ = savePipelineProviderToEnv(azdoLabel, env)
		console.Message(ctx, fmt.Sprintf("Using pipeline provider: %s", output.WithHighLightFormat("Azure DevOps")))
		scmProvider := createAzdoScmProvider(env, azdContext, commandRunner, console)
		ciProvider := createAzdoCiProvider(env, azdContext, console)

		return scmProvider, ciProvider, nil
	}

	// Both folders exists and no override value. Default to GitHub
	// Or override value is github and the folder is available
	_ = savePipelineProviderToEnv(gitHubLabel, env)
	console.Message(ctx, fmt.Sprintf("Using pipeline provider: %s", output.WithHighLightFormat("GitHub")))
	scmProvider := NewGitHubScmProvider(commandRunner)
	ciProvider := NewGitHubCiProvider(credential, commandRunner)
	return scmProvider, ciProvider, nil
}

func savePipelineProviderToEnv(provider string, env *environment.Environment) error {
	env.Values[envPersistedKey] = provider
	err := env.Save()
	if err != nil {
		return err
	}
	return nil
}

func createAzdoCiProvider(
	env *environment.Environment, azdCtx *azdcontext.AzdContext, console input.Console,
) *AzdoCiProvider {
	return &AzdoCiProvider{
		Env:        env,
		AzdContext: azdCtx,
		console:    console,
	}
}

func createAzdoScmProvider(
	env *environment.Environment,
	azdCtx *azdcontext.AzdContext,
	commandRunner exec.CommandRunner,
	console input.Console,
) *AzdoScmProvider {
	return &AzdoScmProvider{
		Env:           env,
		AzdContext:    azdCtx,
		commandRunner: commandRunner,
		console:       console,
	}
}
