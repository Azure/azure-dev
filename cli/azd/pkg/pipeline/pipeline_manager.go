// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/google/uuid"
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
	ServiceManagementReference   string
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
	envManager        environment.Manager
	scmProvider       ScmProvider
	ciProvider        CiProvider
	args              *PipelineManagerArgs
	azdCtx            *azdcontext.AzdContext
	env               *environment.Environment
	entraIdService    entraid.EntraIdService
	gitCli            git.GitCli
	console           input.Console
	serviceLocator    ioc.ServiceLocator
	importManager     *project.ImportManager
	configOptions     *configurePipelineOptions
	infra             *project.Infra
	userConfigManager config.UserConfigManager
}

func NewPipelineManager(
	ctx context.Context,
	envManager environment.Manager,
	entraIdService entraid.EntraIdService,
	gitCli git.GitCli,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	console input.Console,
	args *PipelineManagerArgs,
	serviceLocator ioc.ServiceLocator,
	importManager *project.ImportManager,
	userConfigManager config.UserConfigManager,
) (*PipelineManager, error) {
	pipelineProvider := &PipelineManager{
		azdCtx:            azdCtx,
		envManager:        envManager,
		env:               env,
		args:              args,
		entraIdService:    entraIdService,
		gitCli:            gitCli,
		console:           console,
		serviceLocator:    serviceLocator,
		importManager:     importManager,
		userConfigManager: userConfigManager,
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
	args *PipelineManagerArgs, entraIdService entraid.EntraIdService) (*servicePrincipalResult, error) {
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

	servicePrincipal, err := entraIdService.GetServicePrincipal(ctx, subscriptionId, appIdOrName)
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
func (pm *PipelineManager) Configure(ctx context.Context, projectName string) (result *PipelineConfigResult, err error) {
	// check all required tools are installed
	requiredTools, err := pm.requiredTools(ctx)
	if err != nil {
		return result, err
	}
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return result, err
	}

	userConfig, err := pm.userConfigManager.Load()
	if err != nil {
		return result, fmt.Errorf("loading user configuration: %w", err)
	}
	smr := resolveSmr(pm.args.ServiceManagementReference, pm.env.Config, userConfig)
	if smr != nil {
		if _, err := uuid.Parse(*smr); err != nil {
			return result, fmt.Errorf("Invalid service management reference %s: %w", *smr, err)
		}
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

	// see if SP already exists - This step will not create the SP if it doesn't exist.
	spConfig, err := servicePrincipal(
		ctx, pm.env.Getenv(AzurePipelineClientIdEnvVarName), pm.env.GetSubscriptionId(), pm.args, pm.entraIdService)
	if err != nil {
		return result, err
	}

	// Update the message depending on the SP already exists or not
	var displayMsg string
	if spConfig.servicePrincipal == nil {
		displayMsg = fmt.Sprintf("Creating service principal %s", spConfig.applicationName)
	} else {
		displayMsg = fmt.Sprintf("Updating service principal %s (%s)",
			spConfig.servicePrincipal.DisplayName,
			spConfig.servicePrincipal.AppId)
	}

	pm.console.ShowSpinner(ctx, displayMsg, input.Step)
	description := fmt.Sprintf("Created by Azure Developer CLI for project: %s", projectName)
	options := entraid.CreateOrUpdateServicePrincipalOptions{
		RolesToAssign:              pm.args.PipelineRoleNames,
		Description:                &description,
		ServiceManagementReference: smr,
	}
	servicePrincipal, err := pm.entraIdService.CreateOrUpdateServicePrincipal(
		ctx,
		pm.env.GetSubscriptionId(),
		spConfig.appIdOrName,
		options)

	if err != nil {
		pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))
		return result, fmt.Errorf("failed to create or update service principal: %w", err)
	}

	if !strings.Contains(displayMsg, servicePrincipal.AppId) {
		displayMsg += fmt.Sprintf(" (%s)", servicePrincipal.AppId)
	}
	pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))

	// Set in .env to be retrieved for any additional runs
	pm.env.DotenvSet(AzurePipelineClientIdEnvVarName, servicePrincipal.AppId)
	if err := pm.envManager.Save(ctx, pm.env); err != nil {
		return result, fmt.Errorf("failed to save environment: %w", err)
	}

	repoSlug := gitRepoInfo.owner + "/" + gitRepoInfo.repoName
	displayMsg = fmt.Sprintf("Configuring repository %s to use credentials for %s", repoSlug, spConfig.applicationName)
	pm.console.ShowSpinner(ctx, displayMsg, input.Step)

	subscriptionId := pm.env.GetSubscriptionId()
	credentials := &entraid.AzureCredentials{
		ClientId:       servicePrincipal.AppId,
		TenantId:       *servicePrincipal.AppOwnerOrganizationId,
		SubscriptionId: subscriptionId,
	}

	// Get the requested credential options from the CI provider
	credentialOptions, err := pm.ciProvider.credentialOptions(
		ctx,
		gitRepoInfo,
		infra.Options,
		PipelineAuthType(pm.args.PipelineAuthTypeName),
		credentials,
	)
	if err != nil {
		return result, fmt.Errorf("failed to get credential options: %w", err)
	}

	// Enable client credentials if requested
	if credentialOptions.EnableClientCredentials {
		spinnerMessage := "Configuring client credentials for service principal"
		pm.console.ShowSpinner(ctx, spinnerMessage, input.Step)

		creds, err := pm.entraIdService.ResetPasswordCredentials(ctx, subscriptionId, servicePrincipal.AppId)
		pm.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
		if err != nil {
			return result, fmt.Errorf("failed to reset password credentials: %w", err)
		}

		credentials = creds
	}

	// Enable federated credentials if requested
	if credentialOptions.EnableFederatedCredentials {
		createdCredentials, err := pm.entraIdService.ApplyFederatedCredentials(
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
	localEnvConfig, err := json.Marshal(pm.env.Config.ResolvedRaw())
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
			pm.console.Message(ctx, "")
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

// resolveProviderAndDetermine resolves the pipeline provider based on project configuration and environment,
// or determines it if not already set.
func (pm *PipelineManager) resolveProviderAndDetermine(ctx context.Context, projectPath, repoRoot string) (string, error) {
	log.Printf("Loading project configuration from: %s", projectPath)
	prjConfig, err := project.Load(ctx, projectPath)
	if err != nil {
		return "", fmt.Errorf("Loading project configuration: %w", err)
	}
	log.Printf("Loaded project configuration: %+v", prjConfig)

	// 1) Check if provider is set on azure.yaml, it should override the `lastUsedProvider`
	if prjConfig.Pipeline.Provider != "" {
		log.Printf("Provider set in project configuration: %s", prjConfig.Pipeline.Provider)
		return prjConfig.Pipeline.Provider, nil
	}

	// 2) Check if there is a persisted value from a previous run in the environment
	if lastUsedProvider, configExists := pm.env.LookupEnv(envPersistedKey); configExists {
		log.Printf("Using persisted provider from environment: %s", lastUsedProvider)
		return lastUsedProvider, nil
	}

	// 3) No config on azure.yaml or from previous run, so use the determineProvider logic
	log.Printf("No provider set in project configuration or environment. Determining provider based on repository.")
	return pm.determineProvider(ctx, repoRoot)
}

// initialize sets up the SCM and CI providers based on the provided override
// or the detected configuration in the repository.
// Logic:
//   - If the user specifies a provider through the arguments, that provider is used.
//   - If no provider is specified:
//   - If both GitHub and Azure DevOps configurations are detected, prompt the user to choose which one to use.
//   - If only GitHub configuration is found, use GitHub Actions.
//   - If only Azure DevOps configuration is found, use Azure DevOps.
//   - If no configuration is found, prompt the user to select which one to set up.
//   - Default to GitHub Actions if no provider is specified or selected.
//   - Prompt the user to confirm adding the azure-dev file if it’s missing, and inform them where the file is created.
//   - The provider is persisted in the environment so the next time the function is run,
//     the same provider is used directly, unless the overrideProvider is used to change the last used configuration.
func (pm *PipelineManager) initialize(ctx context.Context, override string) error {
	projectDir := pm.azdCtx.ProjectDirectory()
	projectPath := pm.azdCtx.ProjectPath()
	repoRoot, err := pm.gitCli.GetRepoRoot(ctx, projectDir)
	if err != nil {
		repoRoot = projectDir
		log.Printf("using project root as repo root, since git repo wasn't available: %s", err)
	}

	// Use the provided pipeline provider if specified, otherwise resolve or determine the provider
	pipelineProvider := strings.ToLower(override)
	if pipelineProvider == "" {
		pipelineProvider, err = pm.resolveProviderAndDetermine(ctx, projectPath, repoRoot)
		if err != nil {
			return err
		}
	}

	prjConfig, err := project.Load(ctx, projectPath)
	if err != nil {
		return fmt.Errorf("Loading project configuration: %w", err)
	}

	infra, err := pm.importManager.ProjectInfrastructure(ctx, prjConfig)
	if err != nil {
		return err
	}
	defer func() { _ = infra.Cleanup() }()
	pm.infra = infra

	// Check and prompt for missing CI/CD files
	if err := pm.checkAndPromptForProviderFiles(
		ctx, repoRoot, pipelineProvider, string(pm.infra.Options.Provider)); err != nil {
		return err
	}

	// Save the provider to the environment
	if err := pm.savePipelineProviderToEnv(ctx, pipelineProvider, pm.env); err != nil {
		return err
	}

	var scmProviderName, ciProviderName, displayName string
	if pipelineProvider == azdoLabel {
		scmProviderName = azdoLabel
		ciProviderName = azdoLabel
		displayName = azdoDisplayName
	} else {
		scmProviderName = gitHubLabel
		ciProviderName = gitHubLabel
		displayName = gitHubDisplayName
	}
	log.Printf("Using pipeline provider: %s", output.WithHighLightFormat(displayName))

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

func (pm *PipelineManager) checkAndPromptForProviderFiles(
	ctx context.Context, repoRoot, pipelineProvider string, infraProvider string) error {
	if pipelineProvider == "" {
		log.Println("Pipeline provider is empty, no need to check for files.")
		return nil
	}

	log.Printf("Checking for provider files for: %s", pipelineProvider)

	providerFileChecks := map[string]struct {
		ymlPath             string
		dirPath             string
		dirDisplayName      string
		providerDisplayName string
	}{
		gitHubLabel: {
			ymlPath:             filepath.Join(repoRoot, gitHubYml),
			dirPath:             filepath.Join(repoRoot, gitHubWorkflowsDirectory),
			dirDisplayName:      gitHubWorkflowsDirectory,
			providerDisplayName: gitHubDisplayName,
		},
		azdoLabel: {
			ymlPath:             filepath.Join(repoRoot, azdoYml),
			dirPath:             filepath.Join(repoRoot, azdoPipelinesDirectory),
			dirDisplayName:      azdoPipelinesDirectory,
			providerDisplayName: azdoDisplayName,
		},
	}

	providerCheck, exists := providerFileChecks[pipelineProvider]
	if !exists {
		errMsg := fmt.Sprintf("%s is not a known pipeline provider", pipelineProvider)
		log.Println("Error:", errMsg)
		return fmt.Errorf(errMsg)
	}

	log.Printf("YAML path: %s", providerCheck.ymlPath)
	log.Printf("Directory path: %s", providerCheck.dirPath)

	if !osutil.FileExists(providerCheck.ymlPath) {
		log.Printf("%s YAML not found, prompting for creation", providerCheck.providerDisplayName)
		if err := pm.promptForCiFiles(ctx, pipelineProvider, infraProvider, repoRoot); err != nil {
			log.Println("Error prompting for CI files:", err)
			return err
		}
		log.Println("Prompt for CI files completed successfully.")
	}

	log.Printf("Checking if directory %s is empty", providerCheck.dirPath)
	isEmpty, err := osutil.IsDirEmpty(providerCheck.dirPath, true)
	if err != nil {
		log.Println("Error checking if directory is empty:", err)
		return fmt.Errorf("error checking if directory is empty: %w", err)
	}

	if isEmpty {
		if pipelineProvider == azdoLabel {
			message := fmt.Sprintf(
				"%s provider selected, but %s is empty. Please add pipeline files and try again.",
				providerCheck.providerDisplayName, providerCheck.dirDisplayName)
			log.Println("Error:", message)
			return fmt.Errorf(message)
		}
		if pipelineProvider == gitHubLabel {
			message := fmt.Sprintf(
				"%s provider selected, but %s is empty. Please add pipeline files.",
				providerCheck.providerDisplayName, providerCheck.dirDisplayName)
			log.Println("Info:", message)
			pm.console.Message(ctx, message)
		}
		pm.console.Message(ctx, "")
	}

	log.Printf("Provider files are present for: %s", pipelineProvider)
	return nil
}

// promptForCiFiles creates CI/CD files for the specified provider, confirming with the user before creation.
func (pm *PipelineManager) promptForCiFiles(ctx context.Context, pipelineProvider, infraProvider, repoRoot string) error {
	paths := map[string]struct {
		directory string
		yml       string
	}{
		gitHubLabel: {filepath.Join(repoRoot, gitHubWorkflowsDirectory), filepath.Join(repoRoot, gitHubYml)},
		azdoLabel:   {filepath.Join(repoRoot, azdoPipelinesDirectory), filepath.Join(repoRoot, azdoYml)},
	}

	providerPaths, exists := paths[pipelineProvider]
	if !exists {
		errMsg := fmt.Sprintf("Unknown provider: %s", pipelineProvider)
		log.Println("Error:", errMsg)
		return fmt.Errorf(errMsg)
	}

	log.Printf("Directory path: %s", providerPaths.directory)
	log.Printf("YAML path: %s", providerPaths.yml)

	// Confirm with the user before adding the file
	pm.console.Message(ctx, "")
	pm.console.Message(ctx,
		fmt.Sprintf("The default %s file, which contains a basic workflow to help you get started, is missing from your project.",
			output.WithHighLightFormat("azure-dev.yml")))
	pm.console.Message(ctx, "")

	// Prompt the user for confirmation
	confirm, err := pm.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Would you like to add it now?",
		DefaultValue: true,
	})
	if err != nil {
		return fmt.Errorf("prompting to create file: %w", err)
	}
	pm.console.Message(ctx, "")

	if confirm {
		log.Printf("Confirmed creation of %s file at %s", filepath.Base(providerPaths.yml), providerPaths.directory)

		if !osutil.DirExists(providerPaths.directory) {
			log.Printf("Creating directory %s", providerPaths.directory)
			if err := os.MkdirAll(providerPaths.directory, os.ModePerm); err != nil {
				return fmt.Errorf("creating directory %s: %w", providerPaths.directory, err)
			}
		}

		if !osutil.FileExists(providerPaths.yml) {
			embedFilePath := fmt.Sprintf("pipeline/.%s/azure-dev.yml", pipelineProvider)
			if infraProvider == "terraform" {
				embedFilePath = fmt.Sprintf("pipeline/.%s/azure-dev-tf.yml", pipelineProvider)
			}
			contents, err := resources.PipelineFiles.ReadFile(embedFilePath)
			if err != nil {
				return fmt.Errorf("reading embedded file %s: %w", embedFilePath, err)
			}
			log.Printf("Creating file %s", providerPaths.yml)
			if err := os.WriteFile(providerPaths.yml, contents, osutil.PermissionFile); err != nil {
				return fmt.Errorf("creating file %s: %w", providerPaths.yml, err)
			}
			pm.console.Message(ctx,
				fmt.Sprintf(
					"The %s file has been created at %s. You can use it as-is or modify it to suit your needs.",
					output.WithHighLightFormat(filepath.Base(providerPaths.yml)),
					output.WithHighLightFormat(providerPaths.yml)),
			)
			pm.console.Message(ctx, "")

		}

		return nil
	}

	log.Printf("User declined creation of %s file at %s", filepath.Base(providerPaths.yml), providerPaths.directory)

	return nil
}

func (pm *PipelineManager) determineProvider(ctx context.Context, repoRoot string) (string, error) {
	log.Printf("Checking for CI/CD YAML files in the repository root: %s", repoRoot)

	// Check for existence of official YAML files in the repo root
	hasGitHubYml := osutil.FileExists(filepath.Join(repoRoot, gitHubYml))
	hasAzDevOpsYml := osutil.FileExists(filepath.Join(repoRoot, azdoYml))

	log.Printf("GitHub Actions YAML exists: %v", hasGitHubYml)
	log.Printf("Azure DevOps YAML exists: %v", hasAzDevOpsYml)

	switch {
	case (!hasGitHubYml && !hasAzDevOpsYml) || (hasGitHubYml && hasAzDevOpsYml):
		// No official YAML files found for either provider or both are found
		log.Printf("Neither or both YAML files found. Prompting user for provider selection.")
		return pm.promptForProvider(ctx)

	case hasGitHubYml && !hasAzDevOpsYml:
		// GitHub Actions YAML found, Azure DevOps YAML not found
		log.Printf("Only GitHub Actions YAML found. Selecting GitHub Actions as the provider.")
		return gitHubLabel, nil

	case hasAzDevOpsYml && !hasGitHubYml:
		// Azure DevOps YAML found, GitHub Actions YAML not found
		log.Printf("Only Azure DevOps YAML found. Selecting Azure DevOps as the provider.")
		return azdoLabel, nil

	default:
		// Default to GitHub Actions if no provider is specified
		log.Printf("Defaulting to GitHub Actions as the provider.")
		return gitHubLabel, nil
	}
}

// promptForProvider prompts the user to select a CI/CD provider.
func (pm *PipelineManager) promptForProvider(ctx context.Context) (string, error) {
	log.Printf("Prompting user to select a CI/CD provider.")
	pm.console.Message(ctx, "")
	choice, err := pm.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a provider:",
		Options: []string{gitHubDisplayName, azdoDisplayName},
	})
	if err != nil {
		return "", fmt.Errorf("prompting for CI/CD provider: %w", err)
	}

	log.Printf("User selected choice: %d", choice)

	if choice == 0 {
		return gitHubLabel, nil
	} else if choice == 1 {
		return azdoLabel, nil
	}

	return "", nil // This case should never occur with the current options.
}

// resolveSmr resolves the service management reference from the user, project, or environment configuration.
func resolveSmr(smrArg string, projectConfig config.Config, userConfig config.Config) *string {
	if smrArg != "" {
		// If the user has provided a value for the --applicationServiceManagementReference flag, use it
		return &smrArg
	}

	smrFromConfig := func(config config.Config) *string {
		if smr, ok := config.GetString("pipeline.config.applicationServiceManagementReference"); ok {
			return &smr
		}
		return nil
	}

	// per environment configuration
	if smr := smrFromConfig(projectConfig); smr != nil {
		return smr
	}
	// per user configuration
	if smr := smrFromConfig(userConfig); smr != nil {
		return smr
	}
	// no smr configuration
	return nil
}
