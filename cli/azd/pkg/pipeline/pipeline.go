// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
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
	// providerParameters can be used to automatically set variables and secrets by the provisioning provider.
	// This is useful for fully-managed scenarios like Aspire, where user is not manually defining the variables and secrets
	// in the azure.yaml file. The provider can provide the parameters and values required in CI.
	providerParameters []provisioning.Parameter
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
		authConfig *authConfiguration,
		credentialOptions *CredentialOptions,
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
// The initial values reference azd known values, which are merged with the ones defined on azure.yaml by the user and the
// provider parameters.
func mergeProjectVariablesAndSecrets(
	projectVariables, projectSecrets []string,
	initialVariables, initialSecrets map[string]string,
	providerParameters []provisioning.Parameter,
	env map[string]string) (variables, secrets map[string]string, err error) {

	// initial state comes from the list of initial variables and secrets
	variables = maps.Clone(initialVariables)
	secrets = maps.Clone(initialSecrets)

	// second override is based on the provider parameters
	for _, parameter := range providerParameters {
		envVarsCount := len(parameter.EnvVarMapping)
		if envVarsCount == 0 {
			if parameter.LocalPrompt {
				return nil, nil,
					fmt.Errorf(
						"parameter %s got its value from a local prompt and it has not a mapped environment variable. "+
							"The local value can't be configured in CI without having a map to one ENV VAR. "+
							"Define a mapping for %s to one ENV VAR as part of the infra parameters definition",
						parameter.Name, parameter.Name)
			}
			// env var == 0 AND no local prompt, ignore it
			continue
		}
		if envVarsCount > 1 {
			if parameter.LocalPrompt {
				return nil, nil,
					fmt.Errorf(
						"parameter %s got its value from a local prompt and it has more than one mapped environment "+
							"variable. "+
							"The value can't be configured in CI mapped to multiple ENV VARS if AZD prompt for its value. "+
							"Define a single mapping for %s to one ENV VAR as part of the infra parameters definition",
						parameter.Name, parameter.Name)
			}
			// env var > 1 AND no local prompt, ignore it
			// for parameters mapped to more than one ENV VAR, each env var becomes either a variable or a secret
			for _, envVar := range parameter.EnvVarMapping {
				// see if the env var is set in the system env or azd env
				// NOTE: provider parameters have access to system env vars but not project env vars/secrets.
				value := env[envVar]
				if value == "" {
					value = os.Getenv(envVar)
				}
				if value == "" {
					// env var not set, ignore it
					continue
				}
				if parameter.Secret {
					secrets[envVar] = value
				} else {
					variables[envVar] = value
				}
			}
			// nothing else to do for parameters mapped to multiple env vars
			continue
		}
		// Param mapped to a single env var, use that ENV VAR to set the link in CI
		// marshall the value to a string
		envVar := parameter.EnvVarMapping[0]
		if !parameter.LocalPrompt && !parameter.UsingEnvVarMapping {
			//For non-prompt params, use it only if the env var mapping is defined.
			continue
		}
		// At this point, either LocalPrompt is true or the env var is set.
		// This means we have what we need to set the variable in CI as string.
		strValue := fmt.Sprintf("%v", parameter.Value)
		if parameter.Secret {
			secrets[envVar] = strValue
		} else {
			variables[envVar] = strValue
		}
	}

	// Last override is based on the user explicitly defined variables and secrets
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

	// Escape values for safe transmission to pipeline providers.
	// This ensures that values containing JSON-like content (e.g., `["api://..."]`)
	// are properly escaped (e.g., `[\"api://...\"]`) before being sent to GitHub Actions or Azure DevOps.
	// Without this, the remote pipeline may incorrectly parse the value as JSON instead of treating it as a string.
	escapeValuesForPipeline(variables)
	escapeValuesForPipeline(secrets)

	return variables, secrets, nil
}

// escapeValuesForPipeline applies JSON escaping to values to ensure they are correctly
// interpreted as strings by pipeline providers (GitHub Actions, Azure DevOps).
//
// When a value contains special characters (e.g., quotes, backslashes, brackets), it needs
// to be escaped before being sent to the remote pipeline. This function uses JSON marshaling
// to properly escape the value, then strips the outer quotes added by marshaling.
//
// Example: the value `["api://guid"]` becomes `[\"api://guid\"]` after escaping.
func escapeValuesForPipeline(values map[string]string) {
	for key, value := range values {
		// Use JSON marshaling to properly escape special characters
		escapedBytes, err := json.Marshal(value)
		if err != nil {
			// If marshaling fails, keep the original value
			continue
		}

		escapedStr := string(escapedBytes)
		// JSON marshaling wraps the string in quotes; remove them
		// Example: json.Marshal("test") produces "\"test\"", we want just the inner content
		if len(escapedStr) >= 2 && escapedStr[0] == '"' && escapedStr[len(escapedStr)-1] == '"' {
			escapedStr = escapedStr[1 : len(escapedStr)-1]
		}

		values[key] = escapedStr
	}
}

const (
	gitHubDisplayName string = "GitHub"
	gitHubCode               = "github"
	gitHubRoot        string = ".github"
	gitHubWorkflows   string = "workflows"
	azdoDisplayName   string = "Azure DevOps"
	azdoCode                 = "azdo"
	azdoRoot          string = ".azdo"
	azdoRootAlt       string = ".azuredevops"
	azdoPipelines     string = "pipelines"
	envPersistedKey   string = "AZD_PIPELINE_PROVIDER"
)

var (
	pipelineFileNames = []string{"azure-dev.yml", "azure-dev.yaml"}
)

var (
	pipelineProviderFiles = map[ciProviderType]struct {
		RootDirectories     []string
		PipelineDirectories []string
		Files               []string
		DefaultFile         string
		DisplayName         string
		Code                string
	}{
		ciProviderGitHubActions: {
			RootDirectories:     []string{gitHubRoot},
			PipelineDirectories: []string{filepath.Join(gitHubRoot, gitHubWorkflows)},
			Files:               generateFilePaths([]string{filepath.Join(gitHubRoot, gitHubWorkflows)}, pipelineFileNames),
			DefaultFile:         pipelineFileNames[0],
			DisplayName:         gitHubDisplayName,
		},
		ciProviderAzureDevOps: {
			RootDirectories:     []string{azdoRoot, azdoRootAlt},
			PipelineDirectories: []string{filepath.Join(azdoRoot, azdoPipelines), filepath.Join(azdoRootAlt, azdoPipelines)},
			Files: generateFilePaths([]string{filepath.Join(azdoRoot, azdoPipelines),
				filepath.Join(azdoRootAlt, azdoPipelines)}, pipelineFileNames),
			DefaultFile: pipelineFileNames[0],
			DisplayName: azdoDisplayName,
		},
	}
)

func generateFilePaths(directories []string, fileNames []string) []string {
	var paths []string
	for _, dir := range directories {
		for _, file := range fileNames {
			paths = append(paths, filepath.Join(dir, file))
		}
	}
	return paths
}

type ciProviderType string

const (
	ciProviderGitHubActions ciProviderType = gitHubCode
	ciProviderAzureDevOps   ciProviderType = azdoCode
)

func toCiProviderType(provider string) (ciProviderType, error) {
	result := ciProviderType(provider)
	if result == ciProviderGitHubActions || result == ciProviderAzureDevOps {
		return result, nil
	}
	return "", fmt.Errorf("invalid ci provider type %s", provider)
}

type infraProviderType string

const (
	infraProviderBicep     infraProviderType = "bicep"
	infraProviderTerraform infraProviderType = "terraform"
	infraProviderUndefined infraProviderType = ""
)

func toInfraProviderType(provider string) (infraProviderType, error) {
	result := infraProviderType(provider)
	if result == infraProviderBicep || result == infraProviderTerraform || result == infraProviderUndefined {
		return result, nil
	}
	return "", fmt.Errorf("invalid infra provider type %s", provider)
}

type projectProperties struct {
	CiProvider            ciProviderType
	InfraProvider         infraProviderType
	RepoRoot              string
	HasAppHost            bool
	BranchName            string
	AuthType              PipelineAuthType
	Variables             []string
	Secrets               []string
	RequiredAlphaFeatures []string
	providerParameters    []provisioning.Parameter
}

type authConfiguration struct {
	*entraid.AzureCredentials
	sp  *graphsdk.ServicePrincipal
	msi *armmsi.Identity
}
