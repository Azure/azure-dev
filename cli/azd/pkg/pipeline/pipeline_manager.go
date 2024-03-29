// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/sethvargo/go-retry"
	"golang.org/x/exp/slices"
)

type PipelineAuthType string

// servicePrincipalLookupKind is the type of lookup to use when resolving the service principal.
type servicePrincipalLookupKind string

const (
	AuthTypeFederated               PipelineAuthType           = "federated"
	AuthTypeClientCredentials       PipelineAuthType           = "client-credentials"
	lookupKindPrincipalId           servicePrincipalLookupKind = "principal-id"
	lookupKindPrincipleName         servicePrincipalLookupKind = "principal-name"
	lookupKindEnvironmentVariable   servicePrincipalLookupKind = "environment-variable"
	AzurePipelineClientIdEnvVarName string                     = "AZURE_PIPELINE_CLIENT_ID"
)

var (
	ErrAuthNotSupported = errors.New("pipeline authentication configuration is not supported")
	DefaultRoleNames    = []string{"Contributor", "User Access Administrator"}
)

// PipelineManagerArgs represents the arguments passed to the pipeline manager from Azd CLI
type PipelineManagerArgs struct {
	PipelineServicePrincipalId   string
	PipelineServicePrincipalName string
	PipelineRemoteName           string
	PipelineRoleNames            []string
	PipelineProvider             string
	PipelineAuthTypeName         string
}

// CredentialOptions represents the options for configuring credentials for a pipeline.
type CredentialOptions struct {
	EnableClientCredentials    bool
	EnableFederatedCredentials bool
	FederatedCredentialOptions []*graphsdk.FederatedIdentityCredential
}

type PipelineConfigResult struct {
	RepositoryLink string
	PipelineLink   string
}

// PipelineManager takes care of setting up the scm and pipeline.
// The manager allows to use and test scm providers without a cobra command.
type PipelineManager struct {
	envManager     environment.Manager
	scmProvider    ScmProvider
	ciProvider     CiProvider
	args           *PipelineManagerArgs
	azdCtx         *azdcontext.AzdContext
	env            *environment.Environment
	adService      azcli.AdService
	gitCli         git.GitCli
	console        input.Console
	serviceLocator ioc.ServiceLocator
	importManager  *project.ImportManager
	configOptions  *configurePipelineOptions
	infra          *project.Infra
}

func NewPipelineManager(
	ctx context.Context,
	envManager environment.Manager,
	adService azcli.AdService,
	gitCli git.GitCli,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	console input.Console,
	args *PipelineManagerArgs,
	serviceLocator ioc.ServiceLocator,
	importManager *project.ImportManager,
) (*PipelineManager, error) {
	pipelineProvider := &PipelineManager{
		azdCtx:         azdCtx,
		envManager:     envManager,
		env:            env,
		args:           args,
		adService:      adService,
		gitCli:         gitCli,
		console:        console,
		serviceLocator: serviceLocator,
		importManager:  importManager,
	}

	// check that scm and ci providers are set
	if err := pipelineProvider.initialize(ctx, args.PipelineProvider); err != nil {
		return nil, err
	}

	return pipelineProvider, nil
}

func (pm *PipelineManager) CiProviderName() string {
	return pm.ciProvider.Name()
}

func (pm *PipelineManager) ScmProviderName() string {
	return pm.scmProvider.Name()
}

type servicePrincipalResult struct {
	appIdOrName      string
	applicationName  string
	lookupKind       servicePrincipalLookupKind
	servicePrincipal *graphsdk.ServicePrincipal
}

func servicePrincipal(
	ctx context.Context,
	envClientId,
	subscriptionId string,
	args *PipelineManagerArgs, adService azcli.AdService) (*servicePrincipalResult, error) {
	// Existing Service Principal Lookup strategy
	// 1. --principal-id
	// 2. --principal-name
	// 3. AZURE_PIPELINE_CLIENT_ID environment variable
	// 4. Create new service principal with default naming convention
	var appIdOrName, applicationName string
	var lookupKind servicePrincipalLookupKind

	if args.PipelineServicePrincipalId != "" {
		appIdOrName = args.PipelineServicePrincipalId
		lookupKind = lookupKindPrincipalId
	} else if args.PipelineServicePrincipalName != "" {
		appIdOrName = args.PipelineServicePrincipalName
		lookupKind = lookupKindPrincipleName
	} else if envClientId != "" {
		appIdOrName = envClientId
		lookupKind = lookupKindEnvironmentVariable
	}

	if appIdOrName == "" {
		// Fall back to convention based naming
		applicationName = fmt.Sprintf("az-dev-%s", time.Now().UTC().Format("01-02-2006-15-04-05"))
		return &servicePrincipalResult{
			appIdOrName:      applicationName,
			applicationName:  applicationName,
			servicePrincipal: nil,
			lookupKind:       lookupKind,
		}, nil
	}

	servicePrincipal, err := adService.GetServicePrincipal(ctx, subscriptionId, appIdOrName)
	if err != nil {
		// If an explicit client id was specified but not found then fail
		if lookupKind == lookupKindPrincipalId {
			return nil, fmt.Errorf(
				"service principal with client id '%s' specified in '--principal-id' parameter was not found. Error: %w",
				args.PipelineServicePrincipalId,
				err,
			)
		}

		// If an explicit client id was specified but not found then fail
		if lookupKind == lookupKindEnvironmentVariable {
			return nil, fmt.Errorf(
				"service principal with client id '%s' specified in environment variable '%s' was not found Error: %w",
				envClientId,
				AzurePipelineClientIdEnvVarName,
				err,
			)
		}

		// Return the name of the service principal that was not found. It will be use to create a new one.
		return &servicePrincipalResult{
			appIdOrName:      appIdOrName,
			applicationName:  appIdOrName,
			servicePrincipal: servicePrincipal,
			lookupKind:       lookupKind,
		}, nil
	}

	return &servicePrincipalResult{
		appIdOrName:      servicePrincipal.AppId,
		applicationName:  servicePrincipal.DisplayName,
		servicePrincipal: servicePrincipal,
		lookupKind:       lookupKind,
	}, nil
}

// Configure is the main function from the pipeline manager which takes care
// of creating or setting up the git project, the ci pipeline and the Azure connection.
func (pm *PipelineManager) Configure(ctx context.Context) (result *PipelineConfigResult, err error) {
	// check all required tools are installed
	requiredTools, err := pm.requiredTools(ctx)
	if err != nil {
		return result, err
	}
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return result, err
	}

	infra := pm.infra
	// run pre-config validations.
	rootPath := pm.azdCtx.ProjectDirectory()
	updatedConfig, errorsFromPreConfig := pm.preConfigureCheck(ctx, infra.Options, rootPath)
	if errorsFromPreConfig != nil {
		return result, errorsFromPreConfig
	}
	if updatedConfig {
		pm.console.Message(ctx, "")
	}

	// Get git repo details
	gitRepoInfo, err := pm.getGitRepoDetails(ctx)
	if err != nil {
		return result, fmt.Errorf("ensuring git remote: %w", err)
	}

	if pm.args.PipelineServicePrincipalName != "" && pm.args.PipelineServicePrincipalId != "" {
		//nolint:lll
		return result, fmt.Errorf(
			"you have specified both --principal-id and --principal-name, but only one of these parameters should be used at a time.",
		)
	}

	spConfig, err := servicePrincipal(
		ctx, pm.env.Getenv(AzurePipelineClientIdEnvVarName), pm.env.GetSubscriptionId(), pm.args, pm.adService)
	if err != nil {
		return result, err
	}

	var displayMsg string
	if spConfig.servicePrincipal == nil {
		displayMsg = fmt.Sprintf("Creating service principal %s", spConfig.applicationName)
	} else {
		displayMsg = fmt.Sprintf("Updating service principal %s (%s)",
			spConfig.servicePrincipal.DisplayName,
			spConfig.servicePrincipal.AppId)
	}

	pm.console.ShowSpinner(ctx, displayMsg, input.Step)
	servicePrincipal, err := pm.adService.CreateOrUpdateServicePrincipal(
		ctx,
		pm.env.GetSubscriptionId(),
		spConfig.appIdOrName,
		pm.args.PipelineRoleNames)

	if err != nil {
		return result, fmt.Errorf("failed to create or update service principal: %w", err)
	}

	// Update new service principal to include client id
	if !strings.Contains(displayMsg, servicePrincipal.AppId) {
		displayMsg += fmt.Sprintf(" (%s)", servicePrincipal.AppId)
	}
	pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))
	if err != nil {
		return result, fmt.Errorf("failed to create or update service principal: %w", err)
	}

	// Set in .env to be retrieved for any additional runs
	pm.env.DotenvSet(AzurePipelineClientIdEnvVarName, servicePrincipal.AppId)
	if err := pm.envManager.Save(ctx, pm.env); err != nil {
		return result, fmt.Errorf("failed to save environment: %w", err)
	}

	repoSlug := gitRepoInfo.owner + "/" + gitRepoInfo.repoName
	displayMsg = fmt.Sprintf("Configuring repository %s to use credentials for %s", repoSlug, spConfig.applicationName)
	pm.console.ShowSpinner(ctx, displayMsg, input.Step)

	// Get the requested credential options from the CI provider
	credentialOptions := pm.ciProvider.credentialOptions(
		ctx,
		gitRepoInfo,
		infra.Options,
		PipelineAuthType(pm.args.PipelineAuthTypeName),
	)

	subscriptionId := pm.env.GetSubscriptionId()
	credentials := &azcli.AzureCredentials{
		ClientId:       servicePrincipal.AppId,
		TenantId:       *servicePrincipal.AppOwnerOrganizationId,
		SubscriptionId: subscriptionId,
	}

	// Enable client credentials if requested
	if credentialOptions.EnableClientCredentials {
		spinnerMessage := "Configuring client credentials for service principal"
		pm.console.ShowSpinner(ctx, spinnerMessage, input.Step)

		creds, err := pm.adService.ResetPasswordCredentials(ctx, subscriptionId, servicePrincipal.AppId)
		pm.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
		if err != nil {
			return result, fmt.Errorf("failed to reset password credentials: %w", err)
		}

		credentials = creds
	}

	// Enable federated credentials if requested
	if credentialOptions.EnableFederatedCredentials {
		createdCredentials, err := pm.adService.ApplyFederatedCredentials(
			ctx, subscriptionId,
			servicePrincipal.AppId,
			credentialOptions.FederatedCredentialOptions,
		)
		if err != nil {
			return result, fmt.Errorf("failed to create federated credentials: %w", err)
		}

		for _, credential := range createdCredentials {
			pm.console.MessageUxItem(
				ctx,
				&ux.DisplayedResource{
					Type: fmt.Sprintf("Federated identity credential for %s", pm.ciProvider.Name()),
					Name: fmt.Sprintf("subject %s", credential.Subject),
				},
			)
		}
	}

	err = pm.ciProvider.configureConnection(
		ctx,
		gitRepoInfo,
		infra.Options,
		servicePrincipal,
		PipelineAuthType(pm.args.PipelineAuthTypeName),
		credentials,
	)

	pm.console.StopSpinner(ctx, "", input.GetStepResultFormat(err))
	if err != nil {
		return result, err
	}

	// Adding environment.AzdInitialEnvironmentConfigName as a secret to the pipeline as the base configuration for
	// whenever a new environment is created. This means loading the local environment config into a pipeline secret which
	// azd will use to restore the the config on CI
	localEnvConfig, err := json.Marshal(pm.env.Config.Raw())
	if err != nil {
		return result, fmt.Errorf("failed to marshal environment config: %w", err)
	}

	defaultAzdSecrets := map[string]string{
		environment.AzdInitialEnvironmentConfigName: string(localEnvConfig),
	}

	defaultAzdVariables := map[string]string{}
	// If the user has set the resource group name as an environment variable, we need to pass it to the pipeline
	// as this likely means rg-deployment
	if rgGroup, exists := pm.env.LookupEnv(environment.ResourceGroupEnvVarName); exists {
		defaultAzdVariables[environment.ResourceGroupEnvVarName] = rgGroup
	}

	// Merge azd default variables and secrets with the ones defined on azure.yaml
	pm.configOptions.variables, pm.configOptions.secrets = mergeProjectVariablesAndSecrets(
		pm.configOptions.projectVariables, pm.configOptions.projectSecrets,
		defaultAzdVariables, defaultAzdSecrets, pm.env.Dotenv())

	// config pipeline handles setting or creating the provider pipeline to be used
	ciPipeline, err := pm.ciProvider.configurePipeline(ctx, gitRepoInfo, pm.configOptions)
	if err != nil {
		return result, err
	}

	// The CI pipeline should be set-up and ready at this point.
	// azd offers to push changes to the scm to start a new pipeline run
	doPush, err := pm.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Would you like to commit and push your local changes to start the configured CI pipeline?",
		DefaultValue: true,
	})
	if err != nil {
		return result, fmt.Errorf("prompting to push: %w", err)
	}

	// scm provider can prevent from pushing changes and/or use the
	// interactive console for setting up any missing details.
	// For example, GitHub provider would check if GH-actions are disabled.
	if doPush {
		preventPush, err := pm.scmProvider.preventGitPush(
			ctx,
			gitRepoInfo,
			pm.args.PipelineRemoteName,
			gitRepoInfo.branch)
		if err != nil {
			return result, fmt.Errorf("check git push prevent: %w", err)
		}
		// revert user's choice when prevent git push returns true
		doPush = !preventPush
	}

	if doPush {
		err = pm.pushGitRepo(ctx, gitRepoInfo, gitRepoInfo.branch)
		if err != nil {
			return result, fmt.Errorf("git push: %w", err)
		}

		// The spinner can't run during `pushing changes` the next UX messages are purely simulated
		displayMsg := "Pushing changes"
		pm.console.Message(ctx, "") // new line before the step
		pm.console.ShowSpinner(ctx, displayMsg, input.Step)
		pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))

		displayMsg = "Queuing pipeline"
		pm.console.ShowSpinner(ctx, displayMsg, input.Step)
		gitRepoInfo.pushStatus = true
		pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))
	} else {
		pm.console.Message(ctx,
			fmt.Sprintf(
				"To fully enable pipeline you need to push this repo to the upstream using 'git push --set-upstream %s %s'.\n",
				pm.args.PipelineRemoteName,
				gitRepoInfo.branch))
	}

	return &PipelineConfigResult{
		RepositoryLink: gitRepoInfo.url,
		PipelineLink:   ciPipeline.url(),
	}, nil
}

// requiredTools get all the provider's required tools.
func (pm *PipelineManager) requiredTools(ctx context.Context) ([]tools.ExternalTool, error) {
	scmReqTools, err := pm.scmProvider.requiredTools(ctx)
	if err != nil {
		return nil, err
	}
	ciReqTools, err := pm.ciProvider.requiredTools(ctx)
	if err != nil {
		return nil, err
	}
	reqTools := append(scmReqTools, ciReqTools...)
	return reqTools, nil
}

// preConfigureCheck invoke the validations from each provider.
// the returned configurationWasUpdated indicates if the current settings were updated during the check,
// for example, if Azdo prompt for a PAT or OrgName to the user and updated.
func (pm *PipelineManager) preConfigureCheck(ctx context.Context, infraOptions provisioning.Options, projectPath string) (
	configurationWasUpdated bool,
	err error) {
	// Validate the authentication types
	// auth-type argument must either be an empty string or one of the following values.
	validAuthTypes := []string{string(AuthTypeFederated), string(AuthTypeClientCredentials)}
	pipelineAuthType := strings.TrimSpace(pm.args.PipelineAuthTypeName)
	if pipelineAuthType != "" && !slices.Contains(validAuthTypes, pipelineAuthType) {
		return configurationWasUpdated, fmt.Errorf(
			"pipeline authentication type '%s' is not valid. Valid authentication types are '%s'",
			pm.args.PipelineAuthTypeName,
			strings.Join(validAuthTypes, ", "),
		)
	}

	ciConfigurationWasUpdated, err := pm.ciProvider.preConfigureCheck(
		ctx, *pm.args, infraOptions, projectPath)
	if err != nil {
		return configurationWasUpdated, fmt.Errorf("pre-config check error from %s provider: %w", pm.ciProvider.Name(), err)
	}

	scmConfigurationWasUpdated, err := pm.scmProvider.preConfigureCheck(
		ctx, *pm.args, infraOptions, projectPath)
	if err != nil {
		return configurationWasUpdated, fmt.Errorf("pre-config check error from %s provider: %w", pm.scmProvider.Name(), err)
	}

	configurationWasUpdated = ciConfigurationWasUpdated || scmConfigurationWasUpdated
	return configurationWasUpdated, nil
}

// ensureRemote get the git project details from a path and remote name using the scm provider.
func (pm *PipelineManager) ensureRemote(
	ctx context.Context,
	repositoryPath string,
	remoteName string,
) (*gitRepositoryDetails, error) {
	remoteUrl, err := pm.gitCli.GetRemoteUrl(ctx, repositoryPath, remoteName)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote url: %w", err)
	}

	currentBranch, err := pm.gitCli.GetCurrentBranch(ctx, repositoryPath)
	if err != nil {
		return nil, fmt.Errorf("getting current branch: %w", err)
	}

	// each provider knows how to extract the Owner and repo name from a remoteUrl
	gitRepoDetails, err := pm.scmProvider.gitRepoDetails(ctx, remoteUrl)

	if err != nil {
		return nil, err
	}
	gitRepoDetails.gitProjectPath = pm.azdCtx.ProjectDirectory()
	gitRepoDetails.branch = currentBranch
	return gitRepoDetails, nil
}

// getGitRepoDetails get the details about a git project using the azd context to discover the project path.
func (pm *PipelineManager) getGitRepoDetails(ctx context.Context) (*gitRepositoryDetails, error) {
	repoPath := pm.azdCtx.ProjectDirectory()

	checkGitMessage := "Checking current directory for Git repository"
	var err error
	pm.console.ShowSpinner(ctx, checkGitMessage, input.Step)
	defer pm.console.StopSpinner(ctx, checkGitMessage, input.GetStepResultFormat(err))

	// the warningCount makes sure we only ever show one single warning for the repo missing setup
	// if there is no git repo, the warning is for no git repo detected, but if there is a git repo
	// and the remote is not setup, the warning is for the remote. But we don't want double warning
	// if git repo and remote are missing.
	var warningCount int
	for {
		repoRemoteDetails, err := pm.ensureRemote(ctx, repoPath, pm.args.PipelineRemoteName)
		switch {
		case errors.Is(err, git.ErrNotRepository):
			// remove spinner and display warning
			pm.console.StopSpinner(ctx, checkGitMessage, input.StepWarning)
			pm.console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: "No GitHub repository detected.\n",
				HidePrefix:  true,
			})
			warningCount++

			// Offer the user a chance to init a new repository if one does not exist.
			initRepo, err := pm.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Do you want to initialize a new Git repository in this directory?",
				DefaultValue: true,
			})
			if err != nil {
				return nil, fmt.Errorf("prompting for git init: %w", err)
			}

			if !initRepo {
				return nil, errors.New("confirmation declined")
			}

			initRepoMsg := "Creating Git repository locally."
			pm.console.Message(ctx, "") // we need a new line here
			pm.console.ShowSpinner(ctx, initRepoMsg, input.Step)
			if err := pm.gitCli.InitRepo(ctx, repoPath); err != nil {
				pm.console.StopSpinner(ctx, initRepoMsg, input.StepFailed)
				return nil, fmt.Errorf("initializing repository: %w", err)
			}
			pm.console.StopSpinner(ctx, initRepoMsg, input.StepDone)
			pm.console.Message(ctx, "") // any next line should be one line apart from the step finish

			// Recovered from this error, try again
			continue
		case errors.Is(err, git.ErrNoSuchRemote):
			// Show warning only if no other warning was shown before.
			if warningCount == 0 {
				pm.console.StopSpinner(ctx, checkGitMessage, input.StepWarning)
				pm.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: fmt.Sprintf("Remote \"%s\" is not configured.\n", pm.args.PipelineRemoteName),
					HidePrefix:  true,
				})
				warningCount++
			}

			// the scm provider returns the repo url that is used as git remote
			remoteUrl, err := pm.scmProvider.configureGitRemote(ctx, repoPath, pm.args.PipelineRemoteName)
			if err != nil {
				return nil, err
			}

			// set the git remote for local git project
			if err := pm.gitCli.AddRemote(ctx, repoPath, pm.args.PipelineRemoteName, remoteUrl); err != nil {
				return nil, fmt.Errorf("initializing repository: %w", err)
			}
			pm.console.Message(ctx, "") // any next line should be one line apart from the step finish

			continue
		case err != nil:
			return nil, err
		default:
			return repoRemoteDetails, nil
		}
	}
}

// pushGitRepo commit all changes in the git project and push it to upstream.
func (pm *PipelineManager) pushGitRepo(ctx context.Context, gitRepoInfo *gitRepositoryDetails, currentBranch string) error {
	if err := pm.gitCli.AddFile(ctx, pm.azdCtx.ProjectDirectory(), "."); err != nil {
		return fmt.Errorf("adding files: %w", err)
	}

	if err := pm.gitCli.Commit(ctx, pm.azdCtx.ProjectDirectory(), "Configure Azure Developer Pipeline"); err != nil {
		return fmt.Errorf("commit changes: %w", err)
	}

	// If user has a git credential manager with some cached credentials
	// and the credentials are rotated, the push operation will fail and the credential manager would remove the cache
	// Then, on the next intent to push code, there should be a prompt for credentials.
	// Due to this, we use retry here, so we can run the second intent to prompt for credentials one more time
	return retry.Do(ctx, retry.WithMaxRetries(3, retry.NewConstant(100*time.Millisecond)), func(ctx context.Context) error {
		if err := pm.scmProvider.GitPush(
			ctx,
			gitRepoInfo,
			pm.args.PipelineRemoteName,
			currentBranch); err != nil {
			return retry.RetryableError(fmt.Errorf("pushing changes: %w", err))
		}
		return nil
	})
}

func (pm *PipelineManager) resolveProvider(
	ctx context.Context, prj *project.ProjectConfig) (string, error) {
	// 1) if provider is set on azure.yaml, it should override the `lastUsedProvider`, as it can be changed by customer
	// at any moment.
	if prj.Pipeline.Provider != "" {
		return prj.Pipeline.Provider, nil
	}

	// 2) check if there is a persisted value from a previous run in env
	if lastUsedProvider, configExists := pm.env.LookupEnv(envPersistedKey); configExists {
		// Setting override value based on last run. This will force detector to use the same
		// configuration.
		return lastUsedProvider, nil
	}

	// 3) No config on azure.yaml or from previous run. The provider will be set after
	// inspecting the existing project folders.
	return "", nil
}

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
func (pm *PipelineManager) initialize(ctx context.Context, override string) error {
	projectDir := pm.azdCtx.ProjectDirectory()
	projectPath := pm.azdCtx.ProjectPath()
	pm.args.PipelineProvider = override
	pipelineProvider := strings.ToLower(pm.args.PipelineProvider)

	// detecting pipeline folder configuration
	hasGitHubFolder := folderExists(filepath.Join(projectDir, githubFolder))
	hasAzDevOpsFolder := folderExists(filepath.Join(projectDir, azdoFolder))
	hasAzDevOpsYml := ymlExists(filepath.Join(projectDir, azdoYml))

	// Error missing config for any provider
	if !hasGitHubFolder && !hasAzDevOpsFolder {
		return fmt.Errorf(
			"no CI/CD provider configuration found. Expecting either %s and/or %s folder in the project root directory.",
			gitHubLabel,
			azdoLabel)
	}

	// Figure out what is the expected provider to use for provisioning
	prjConfig, err := project.Load(ctx, projectPath)
	if err != nil {
		return fmt.Errorf("Loading project configuration: %w", err)
	}

	// overrideWith is the last overriding mode. When it is empty
	// we can re-assign it based on a previous run (persisted data)
	// or based on the azure.yaml
	if pipelineProvider == "" {
		resolved, err := pm.resolveProvider(ctx, prjConfig)
		if err != nil {
			return fmt.Errorf("resolving provider when no provider arg was used: %w", err)
		}
		pipelineProvider = resolved
	}

	// Check override errors for missing folder
	if pipelineProvider == gitHubLabel && !hasGitHubFolder {
		return fmt.Errorf("%s folder is missing. Can't use selected provider", githubFolder)
	}
	if pipelineProvider == azdoLabel && !hasAzDevOpsFolder {
		return fmt.Errorf("%s folder is missing. Can't use selected provider", azdoFolder)
	}
	// pipeline yml file is not in azdo folder
	if pipelineProvider == azdoLabel && !hasAzDevOpsYml {
		return fmt.Errorf("%s file is missing in %s folder. Can't use selected provider", azdoYml, azdoFolder)
	}
	// using wrong override value
	if pipelineProvider != "" && pipelineProvider != azdoLabel && pipelineProvider != gitHubLabel {
		return fmt.Errorf("%s is not a known pipeline provider", pipelineProvider)
	}

	var scmProviderName, ciProviderName string

	// At this point, we know that override value has either:
	// - github or azdo value
	// - OR is not set
	// And we know that github and azdo folders are present.
	// checking positive cases for overriding
	if pipelineProvider == azdoLabel || hasAzDevOpsFolder && !hasGitHubFolder {
		// Azdo only either by override or by finding only that folder
		log.Printf("Using pipeline provider: %s", output.WithHighLightFormat("Azure DevOps"))

		scmProviderName = azdoLabel
		ciProviderName = azdoLabel
	} else {
		// Both folders exists and no override value. Default to GitHub
		// Or override value is github and the folder is available
		log.Printf("Using pipeline provider: %s", output.WithHighLightFormat("GitHub"))

		scmProviderName = gitHubLabel
		ciProviderName = gitHubLabel
	}

	_ = pm.savePipelineProviderToEnv(ctx, scmProviderName, pm.env)

	var scmProvider ScmProvider
	if err := pm.serviceLocator.ResolveNamed(scmProviderName+"-scm", &scmProvider); err != nil {
		return fmt.Errorf("resolving scm provider: %w", err)
	}

	var ciProvider CiProvider
	if err := pm.serviceLocator.ResolveNamed(ciProviderName+"-ci", &ciProvider); err != nil {
		return fmt.Errorf("resolving ci provider: %w", err)
	}

	pm.scmProvider = scmProvider
	pm.ciProvider = ciProvider

	infra, err := pm.importManager.ProjectInfrastructure(ctx, prjConfig)
	if err != nil {
		return err
	}
	defer func() { _ = infra.Cleanup() }()
	pm.infra = infra

	pm.configOptions = &configurePipelineOptions{
		projectVariables:     slices.Clone(prjConfig.Pipeline.Variables),
		projectSecrets:       slices.Clone(prjConfig.Pipeline.Secrets),
		provisioningProvider: &pm.infra.Options,
	}

	return nil
}

func (pm *PipelineManager) savePipelineProviderToEnv(
	ctx context.Context,
	provider string,
	env *environment.Environment,
) error {
	env.DotenvSet(envPersistedKey, provider)
	err := pm.envManager.Save(ctx, env)
	if err != nil {
		return err
	}
	return nil
}
