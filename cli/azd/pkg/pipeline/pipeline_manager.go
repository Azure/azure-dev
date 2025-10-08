// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	msi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/azure/azure-dev/cli/azd/pkg/armmsi"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/google/uuid"
	"github.com/sethvargo/go-retry"
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
	AzurePipelineMsiResourceId      string                     = "AZURE_PIPELINE_MSI_CLIENT_ID"
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
	gitCli            *git.Cli
	console           input.Console
	serviceLocator    ioc.ServiceLocator
	importManager     *project.ImportManager
	configOptions     *configurePipelineOptions
	infra             *project.Infra
	userConfigManager config.UserConfigManager
	keyVaultService   keyvault.KeyVaultService
	prjConfig         *project.ProjectConfig
	ciProviderType    ciProviderType
	msiService        armmsi.ArmMsiService
	prompter          prompt.Prompter
	dotnetCli         *dotnet.Cli
}

func NewPipelineManager(
	ctx context.Context,
	envManager environment.Manager,
	entraIdService entraid.EntraIdService,
	gitCli *git.Cli,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	console input.Console,
	args *PipelineManagerArgs,
	serviceLocator ioc.ServiceLocator,
	importManager *project.ImportManager,
	userConfigManager config.UserConfigManager,
	keyVaultService keyvault.KeyVaultService,
	msiService armmsi.ArmMsiService,
	prompter prompt.Prompter,
	dotnetCli *dotnet.Cli,
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
		keyVaultService:   keyVaultService,
		msiService:        msiService,
		prompter:          prompter,
		dotnetCli:         dotnetCli,
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
func (pm *PipelineManager) Configure(
	ctx context.Context, projectName string, infra *project.Infra) (result *PipelineConfigResult, err error) {
	pm.infra = infra

	// check all required tools are installed
	requiredTools, err := pm.requiredTools(ctx)
	if err != nil {
		return result, err
	}
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return result, err
	}

	// pipeline definition files
	err = pm.ensurePipelineDefinition(ctx)
	if err != nil {
		return result, fmt.Errorf("ensuring pipeline definition: %w", err)
	}

	// ServiceManagementReference can be set as user config (~/.azd/config.json)
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

	// if AZURE_PIPELINE_MSI_CLIENT_ID is defined, the project is using MSI
	// When both AZURE_PIPELINE_CLIENT_ID and AZURE_PIPELINE_MSI_CLIENT_ID are defined, the MSI will be used
	// This could be the case from projects which migrated from SP to MSI
	msiResourceId := pm.env.Getenv(AzurePipelineMsiResourceId)
	usingMsi := msiResourceId != ""
	subscriptionId := pm.env.GetSubscriptionId()

	// see if SP already exists - This step will not create the SP if it doesn't exist.
	spConfig, err := servicePrincipal(
		ctx, pm.env.Getenv(AzurePipelineClientIdEnvVarName), subscriptionId, pm.args, pm.entraIdService)
	if err != nil {
		return result, err
	}

	usingAppRegistration := spConfig.servicePrincipal != nil
	if usingMsi && usingAppRegistration {
		pm.console.Message(ctx, output.WithWarningFormat("Found both SP and MSI client id. Using MSI client id. "+
			"Remove AZURE_PIPELINE_CLIENT_ID from the environment to remove this warning."))
		usingAppRegistration = false // MSI takes precedence over SP
	}

	skipAuth := false
	if !usingMsi && !usingAppRegistration {
		log.Printf("Authentication mode has not been set. Prompt user if they want to set it up now.")
		const optionMsi = "Federated User Managed Identity (MSI + OIDC)"
		const optionOidc = "Federated Service Principal (SP + OIDC)"
		const optionClientSec = "Client Credentials (SP + Secret)"
		const optionSkip = "Skip authentication setup (for manually configured pipelines or existing set up)"
		options := []string{
			optionMsi,
			optionOidc,
			optionClientSec,
			optionSkip,
		}
		selectedOption, err := pm.console.Select(ctx, input.ConsoleOptions{
			Message:      "Select how to authenticate the pipeline to Azure",
			Options:      options,
			DefaultValue: optionMsi,
		})
		if err != nil {
			return result, fmt.Errorf("prompting for authentication type: %w", err)
		}
		switch options[selectedOption] {
		case optionMsi:
			usingMsi = true
		case optionOidc, optionClientSec:
			usingAppRegistration = true
		case optionSkip:
			skipAuth = true
		}
	}

	// Service Principal or MSI are both handled by authConfiguration as a top layer abstraction.
	var authConfig *authConfiguration

	// *************************** Create or update service principal ***************************
	if !skipAuth && usingAppRegistration {
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

		var servicePrincipal *graphsdk.ServicePrincipal
		var err error

		// Loop to handle ServiceTreeNullValueError and prompt user for Service Tree ID
		for {
			servicePrincipal, err = pm.entraIdService.CreateOrUpdateServicePrincipal(
				ctx,
				subscriptionId,
				spConfig.appIdOrName,
				options)

			if err != nil {
				var serviceTreeError *entraid.ServiceTreeNullValueError
				var serviceTreeInvalidError *entraid.ServiceTreeInvalidError
				invalidInput := errors.As(err, &serviceTreeInvalidError)
				if errors.As(err, &serviceTreeError) || invalidInput {
					pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))

					invalidInputNotes := ""
					if invalidInput {
						invalidInputNotes = serviceTreeInvalidError.Error()
					}

					// Prompt user for Service Tree ID (OID)
					serviceTreeId, promptErr := pm.promptForServiceTreeId(ctx, promptForServiceTreeIdOptions{
						PreviousWasInvalid: invalidInputNotes,
					})
					if promptErr != nil {

						return result, fmt.Errorf("failed to prompt for Service Tree ID: %w", promptErr)
					}

					// Update options with the provided Service Tree ID
					options.ServiceManagementReference = &serviceTreeId

					// Restart the spinner and try again
					pm.console.ShowSpinner(ctx, displayMsg, input.Step)
					continue
				}

				// For any other error, stop spinner and return
				pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))
				return result, fmt.Errorf("failed to create or update service principal: %w", err)
			}

			// Success - break out of the loop
			break
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

		authConfig = &authConfiguration{
			AzureCredentials: &entraid.AzureCredentials{
				ClientId:       servicePrincipal.AppId,
				TenantId:       *servicePrincipal.AppOwnerOrganizationId,
				SubscriptionId: subscriptionId,
			},
			sp: servicePrincipal,
		}
	}

	// *************************** Create or update MSI ***************************
	if !skipAuth && usingMsi {
		// ************************** Pick or create a new MSI **************************
		var displayMsg string
		var msIdentity msi.Identity
		if msiResourceId == "" {
			// Prompt for pick or create a new MSI
			const optionCreate = "Create new User Managed Identity (MSI)"
			const optionUseExisting = "Use existing User Managed Identity (MSI)"
			options := []string{
				optionCreate,
				optionUseExisting,
			}
			selectedOption, err := pm.console.Select(ctx, input.ConsoleOptions{
				Message:      "Do you want to create a new User Managed Identity (MSI) or use an existing one?",
				Options:      options,
				DefaultValue: optionCreate,
			})
			if err != nil {
				return result, fmt.Errorf("prompting for MSI option: %w", err)
			}
			if options[selectedOption] == optionCreate {
				// pick a resource group and location for the new MSI
				location, err := pm.prompter.PromptLocation(
					ctx, subscriptionId, "Select the location to create the MSI", nil, nil)
				if err != nil {
					return nil, fmt.Errorf("prompting for MSI location: %w", err)
				}
				rg, err := pm.prompter.PromptResourceGroupFrom(
					ctx, subscriptionId, location, prompt.PromptResourceGroupFromOptions{
						DefaultName:          "rg-" + projectName + "-msi",
						NewResourceGroupHelp: "The name of the new resource group where the MSI will be created.",
					})
				if err != nil {
					return nil, fmt.Errorf("prompting for resource group: %w", err)
				}

				displayMsg = fmt.Sprintf("Creating User Managed Identity (MSI) for %s", projectName)
				pm.console.ShowSpinner(ctx, displayMsg, input.Step)
				// Create a new MSI
				newMsi, err := pm.msiService.CreateUserIdentity(ctx, subscriptionId, rg, location, "msi-"+projectName)
				if err != nil {
					pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))
					return result, fmt.Errorf("failed to create User Managed Identity (MSI): %w", err)
				}
				msIdentity = newMsi
			} else {
				// List existing MSIs and let the user select one
				msIdentities, err := pm.msiService.ListUserIdentities(ctx, subscriptionId)
				if err != nil {
					return result, fmt.Errorf("failed to list User Managed Identities (MSI): %w", err)
				}
				if len(msIdentities) == 0 {
					return result, fmt.Errorf("no User Managed Identities (MSI) found in subscription %s", subscriptionId)
				}
				// Prompt the user to select an existing MSI
				msiOptions := make([]string, len(msIdentities))
				for i, msi := range msIdentities {
					msiData, err := arm.ParseResourceID(*msi.ID)
					if err != nil {
						return result, fmt.Errorf("parsing MSI resource id: %w", err)
					}
					msiOptions[i] = fmt.Sprintf("%2d. %s (%s)", i+1, *msi.Name, msiData.ResourceGroupName)
				}
				selectedOption, err := pm.console.Select(ctx, input.ConsoleOptions{
					Message:      "Select an existing User Managed Identity (MSI) to use:",
					Options:      msiOptions,
					DefaultValue: msiOptions[0],
				})
				if err != nil {
					return result, fmt.Errorf("prompting for existing MSI: %w", err)
				}
				msIdentity = msIdentities[selectedOption]
			}
		} else {
			displayMsg = fmt.Sprintf("Updating MSI %s", msiResourceId)
			pm.console.ShowSpinner(ctx, displayMsg, input.Step)
			// Get the existing MSI by resource ID
			existingMsi, err := pm.msiService.GetUserIdentity(ctx, msiResourceId)
			if err != nil {
				pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))
				return result, fmt.Errorf("failed to create User Managed Identity (MSI): %w", err)
			}
			msIdentity = existingMsi
		}

		displayMsg = fmt.Sprintf("Assigning roles to User Managed Identity (MSI) %s", *msIdentity.Name)
		pm.console.ShowSpinner(ctx, displayMsg, input.Step)
		// ************************** Role Assign **************************
		err = pm.entraIdService.EnsureRoleAssignments(
			ctx,
			subscriptionId,
			pm.args.PipelineRoleNames,
			// EnsureRoleAssignments uses the ServicePrincipal ID and the DisplayName.
			// We are adapting the MSI to work with the same method as a regular Service Principal, by pulling name and ID.
			&graphsdk.ServicePrincipal{
				Id:          msIdentity.Properties.PrincipalID,
				DisplayName: *msIdentity.Name,
			},
			nil,
		)
		pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))
		if err != nil {
			return result, fmt.Errorf("failed to assign role to User Managed Identity (MSI): %w", err)
		}

		// Set in .env to be retrieved for any additional runs
		pm.env.DotenvSet(AzurePipelineMsiResourceId, *msIdentity.ID)
		if err := pm.envManager.Save(ctx, pm.env); err != nil {
			return result, fmt.Errorf("failed to save environment: %w", err)
		}

		authConfig = &authConfiguration{
			AzureCredentials: &entraid.AzureCredentials{
				ClientId:       *msIdentity.Properties.ClientID,
				TenantId:       *msIdentity.Properties.TenantID,
				SubscriptionId: subscriptionId,
			},
			msi: &msIdentity,
		}
	}

	if !skipAuth {
		repoSlug := gitRepoInfo.owner + "/" + gitRepoInfo.repoName
		displayMsg := fmt.Sprintf("Configuring repository %s to use credentials for %s", repoSlug, spConfig.applicationName)
		pm.console.ShowSpinner(ctx, displayMsg, input.Step)

		// Get the requested credential options from the CI provider
		credentialOptions, err := pm.ciProvider.credentialOptions(
			ctx,
			gitRepoInfo,
			infra.Options,
			PipelineAuthType(pm.args.PipelineAuthTypeName),
			authConfig.AzureCredentials,
		)
		if err != nil {
			return result, fmt.Errorf("failed to get credential options: %w", err)
		}

		// Enable client credentials if requested
		if credentialOptions.EnableClientCredentials {
			spinnerMessage := "Configuring client credentials for service principal"
			pm.console.ShowSpinner(ctx, spinnerMessage, input.Step)

			creds, err := pm.entraIdService.ResetPasswordCredentials(ctx, subscriptionId, authConfig.ClientId)
			pm.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
			if err != nil {
				return result, fmt.Errorf("failed to reset password credentials: %w", err)
			}

			authConfig.AzureCredentials = creds
		}

		// Enable federated credentials if requested
		if credentialOptions.EnableFederatedCredentials {
			type fedCredentialData struct{ Name, Subject, Issuer string }
			var createdCredentials []fedCredentialData
			if usingMsi {
				// convert fedCredentials from msGraph to armmsi.FederatedIdentityCredential
				armFedCreds := make([]msi.FederatedIdentityCredential, len(credentialOptions.FederatedCredentialOptions))
				for i, fedCred := range credentialOptions.FederatedCredentialOptions {
					armFedCreds[i] = msi.FederatedIdentityCredential{
						Name: to.Ptr(fedCred.Name),
						Properties: &msi.FederatedIdentityCredentialProperties{
							Subject:   to.Ptr(fedCred.Subject),
							Issuer:    to.Ptr(fedCred.Issuer),
							Audiences: to.SliceOfPtrs(fedCred.Audiences...),
						},
					}
				}

				creds, err := pm.msiService.ApplyFederatedCredentials(ctx, subscriptionId, *authConfig.msi.ID, armFedCreds)
				if err != nil {
					return result, fmt.Errorf("failed to create federated credentials: %w", err)
				}

				// Convert the armmsi.FederatedIdentityCredential to fedCredentialData for display
				for _, c := range creds {
					createdCredentials = append(createdCredentials, fedCredentialData{
						Name:    *c.Name,
						Subject: *c.Properties.Subject,
						Issuer:  *c.Properties.Issuer,
					})
				}
			} else {
				creds, err := pm.entraIdService.ApplyFederatedCredentials(
					ctx, subscriptionId,
					authConfig.ClientId,
					credentialOptions.FederatedCredentialOptions,
				)
				if err != nil {
					return result, fmt.Errorf("failed to create federated credentials: %w", err)
				}
				for _, c := range creds {
					createdCredentials = append(createdCredentials, fedCredentialData{
						Name:    c.Name,
						Subject: c.Subject,
						Issuer:  c.Issuer,
					})
				}
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
			authConfig,
			credentialOptions,
		)

		pm.console.StopSpinner(ctx, "", input.GetStepResultFormat(err))
		if err != nil {
			return result, err
		}
	}

	defaultAzdSecrets := map[string]string{}
	defaultAzdVariables := map[string]string{}
	// If the user has set the resource group name as an environment variable, we need to pass it to the pipeline
	// as this likely means rg-deployment
	if rgGroup, exists := pm.env.LookupEnv(environment.ResourceGroupEnvVarName); exists {
		defaultAzdVariables[environment.ResourceGroupEnvVarName] = rgGroup
	}

	// Merge azd default variables and secrets with the ones defined on azure.yaml
	pm.configOptions.variables, pm.configOptions.secrets, err = mergeProjectVariablesAndSecrets(
		pm.configOptions.projectVariables, pm.configOptions.projectSecrets,
		defaultAzdVariables, defaultAzdSecrets, pm.configOptions.providerParameters, pm.env.Dotenv())
	if err != nil {
		return result, fmt.Errorf("failed to merge variables and secrets: %w", err)
	}

	// resolve akvs secrets
	// For each akvs in the secrets array:
	// azd gets the value from Azure Key Vault and use it as a secret in the pipeline
	for key, value := range pm.configOptions.secrets {
		if !strings.HasPrefix(value, "akvs://") {
			continue
		}
		kvSecret, err := pm.keyVaultService.SecretFromAkvs(ctx, value)
		if err != nil {
			return result, fmt.Errorf("failed to resolve akvs '%s': %w", key, err)
		}
		pm.configOptions.secrets[key] = kvSecret
	}
	// For each akvs in the variables array:
	// azd must grant read access role to the pipelines's identity to read the akvs
	displayMsg := "Assigning read access role for Key Vault"
	pm.console.ShowSpinner(ctx, displayMsg, input.Step)
	kvAccounts := make(map[string]struct{})
	for key, value := range pm.configOptions.variables {
		if !strings.HasPrefix(value, "akvs://") {
			continue
		}

		if skipAuth {
			continue
		}

		akvs, err := keyvault.ParseAzureKeyVaultSecret(value)
		if err != nil {
			return result, fmt.Errorf("failed to parse akvs '%s': %w", key, err)
		}
		kvId := akvs.SubscriptionId + akvs.VaultName
		if _, ok := kvAccounts[kvId]; ok {
			// skip if already assigned role for this key vault
			continue
		}

		// can't use keyvaultService.Get() because it requires the resource group name and we don't save it for akvs
		allKvFromSub, err := pm.keyVaultService.ListSubscriptionVaults(ctx, akvs.SubscriptionId)
		if err != nil {
			return result, fmt.Errorf(
				"assigning read access role for Key Vault for auth: %w", err)
		}
		var vaultResourceId string
		foundKeyVault := slices.ContainsFunc(allKvFromSub, func(kv keyvault.Vault) bool {
			if kv.Name == akvs.VaultName {
				vaultResourceId = kv.Id
				return true
			}
			return false
		})
		if !foundKeyVault {
			return result, fmt.Errorf(
				"assigning read access role for Key Vault to service principal: "+
					"key vault '%s' not found in subscription '%s'", akvs.VaultName, akvs.SubscriptionId)
		}

		var spId string
		if usingMsi {
			spId = *authConfig.msi.Properties.PrincipalID
		} else if usingAppRegistration {
			spId = *authConfig.sp.Id
		} else {
			continue
		}
		// CreateRbac uses the azure-sdk RoleAssignmentsClient.Create() which creates or updates the role assignment
		// We don't need to check if the role assignment already exists, the method will handle it.
		err = pm.entraIdService.CreateRbac(
			ctx, akvs.SubscriptionId, vaultResourceId, keyvault.RoleIdKeyVaultSecretsUser, spId)
		if err != nil {
			return result, fmt.Errorf(
				"assigning read access role for Key Vault to service principal: %w", err)
		}

		// save the kvId to avoid assigning the role multiple times for the same key vault
		kvAccounts[kvId] = struct{}{}
	}
	pm.console.StopSpinner(ctx, displayMsg, input.GetStepResultFormat(err))

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
				"To fully enable pipeline you need to push this repo to the upstream "+
					"using 'git push --set-upstream %s %s'.\n",
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

	// For Aspire, AZD adds gitignore when missing
	if isAspire := pm.importManager.HasAppHost(ctx, pm.prjConfig); isAspire {
		log.Println("Adding .gitignore for Aspire project if missing")
		if err := pm.dotnetCli.GitIgnore(ctx, repoPath, nil); err != nil {
			// log the error but continue. This is not a critical error to hold up the pipeline setup.
			log.Println("Failed to add .gitignore for Aspire project:", err)
		}
	}

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
func (pm *PipelineManager) resolveProviderAndDetermine(
	ctx context.Context, projectPath, repoRoot string) (ciProviderType, error) {
	log.Printf("Loading project configuration from: %s", projectPath)
	prjConfig, err := project.Load(ctx, projectPath)
	if err != nil {
		return "", fmt.Errorf("Loading project configuration: %w", err)
	}
	log.Printf("Loaded project configuration: %+v", prjConfig)

	// 1) Check if provider is set on azure.yaml, it should override the `lastUsedProvider`
	if prjConfig.Pipeline.Provider != "" {
		log.Printf("Provider set in project configuration: %s", prjConfig.Pipeline.Provider)
		return toCiProviderType(prjConfig.Pipeline.Provider)
	}

	// 2) Check if there is a persisted value from a previous run in the environment
	if lastUsedProvider, configExists := pm.env.LookupEnv(envPersistedKey); configExists {
		log.Printf("Using persisted provider from environment: %s", lastUsedProvider)
		return toCiProviderType(lastUsedProvider)
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
	var pipelineProvider ciProviderType
	if override != "" {
		p, err := toCiProviderType(strings.ToLower(override))
		if err != nil {
			return err
		}
		pipelineProvider = p
	} else {
		p, err := pm.resolveProviderAndDetermine(ctx, projectPath, repoRoot)
		if err != nil {
			return err
		}
		pipelineProvider = p
	}
	pm.ciProviderType = pipelineProvider

	prjConfig, err := project.Load(ctx, projectPath)
	if err != nil {
		return fmt.Errorf("Loading project configuration: %w", err)
	}
	pm.prjConfig = prjConfig

	// Save the provider to the environment
	if err := pm.savePipelineProviderToEnv(ctx, pipelineProvider, pm.env); err != nil {
		return err
	}

	var scmProviderName, ciProviderName, displayName string
	if pipelineProvider == ciProviderAzureDevOps {
		scmProviderName = string(ciProviderAzureDevOps)
		ciProviderName = scmProviderName
		displayName = azdoDisplayName
	} else {
		scmProviderName = string(ciProviderGitHubActions)
		ciProviderName = scmProviderName
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

	return nil
}

func (pm *PipelineManager) savePipelineProviderToEnv(
	ctx context.Context,
	provider ciProviderType,
	env *environment.Environment,
) error {
	env.DotenvSet(envPersistedKey, string(provider))
	err := pm.envManager.Save(ctx, env)
	if err != nil {
		return err
	}
	return nil
}

// checkAndPromptForProviderFiles checks if the provider files are present and prompts the user to create them if not.
func (pm *PipelineManager) checkAndPromptForProviderFiles(ctx context.Context, props projectProperties) error {
	log.Printf("Checking for provider files for: %s", props.CiProvider)

	if !hasPipelineFile(props.CiProvider, props.RepoRoot) {
		log.Printf("%s YAML not found, prompting for creation", props.CiProvider)
		if err := pm.promptForCiFiles(ctx, props); err != nil {
			log.Println("Error prompting for CI files:", err)
			return err
		}
		log.Println("Prompt for CI files completed successfully.")
	}

	var dirPaths []string
	for _, dir := range pipelineProviderFiles[props.CiProvider].PipelineDirectories {
		dirPaths = append(dirPaths, filepath.Join(props.RepoRoot, dir))
	}

	for _, dirPath := range dirPaths {
		log.Printf("Checking if directory %s is empty", dirPath)
		isEmpty, err := osutil.IsDirEmpty(dirPath, true)
		if err != nil {
			log.Println("Error checking if directory is empty:", err)
			return fmt.Errorf("error checking if directory is empty: %w", err)
		}
		if !isEmpty {
			log.Printf("Provider files are present in directory: %s", dirPath)
			return nil
		}
	}

	message := fmt.Sprintf(
		"%s provider selected, but no pipeline files were found in any expected directories:\n%s\n"+
			"Please add pipeline files.",
		pipelineProviderFiles[props.CiProvider].DisplayName,
		strings.Join(pipelineProviderFiles[props.CiProvider].PipelineDirectories, "\n"))

	if props.CiProvider == ciProviderAzureDevOps {
		message = fmt.Sprintf(
			"%s provider selected, but no pipeline files were found in any expected directories:\n%s\n"+
				"Please add pipeline files and try again.",
			pipelineProviderFiles[props.CiProvider].DisplayName,
			strings.Join(pipelineProviderFiles[props.CiProvider].PipelineDirectories, "\n"))
		log.Println("Error:", message)
		// Azdo needs a pipeline definition to create a pipeline. Not finding one is an error.
		return errors.New(message)
	}

	log.Println("Info:", message)
	pm.console.Message(ctx, message)
	pm.console.Message(ctx, "")

	log.Printf("Provider files are not present for: %s", props.CiProvider)
	return nil
}

// promptForCiFiles creates CI/CD files for the specified provider, confirming with the user before creation.
func (pm *PipelineManager) promptForCiFiles(ctx context.Context, props projectProperties) error {
	var dirPaths []string
	for _, dir := range pipelineProviderFiles[props.CiProvider].PipelineDirectories {
		dirPaths = append(dirPaths, filepath.Join(props.RepoRoot, dir))
	}

	var defaultFilePath string
	for _, dirPath := range dirPaths {
		defaultFilePath = filepath.Join(dirPath, pipelineProviderFiles[props.CiProvider].DefaultFile)
		if osutil.DirExists(dirPath) || osutil.FileExists(defaultFilePath) {
			break
		}
	}

	log.Printf("Directory paths: %v", dirPaths)
	log.Printf("Default YAML path: %s", defaultFilePath)

	// Confirm with the user before adding the default file
	pm.console.Message(ctx, "")
	pm.console.Message(
		ctx,
		fmt.Sprintf(
			"The default %s file, which contains a basic workflow to help you get started, is missing from your project.",
			output.WithHighLightFormat("azure-dev.yml"),
		),
	)
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
		log.Printf("Confirmed creation of %s file at %s", filepath.Base(defaultFilePath), dirPaths)

		created := false
		for _, dirPath := range dirPaths {
			if !osutil.DirExists(dirPath) {
				log.Printf("Creating directory %s", dirPath)
				if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
					return fmt.Errorf("creating directory %s: %w", dirPath, err)
				}
				created = true
			}

			if !osutil.FileExists(filepath.Join(dirPath, pipelineProviderFiles[props.CiProvider].DefaultFile)) {
				if err := generatePipelineDefinition(filepath.Join(dirPath,
					pipelineProviderFiles[props.CiProvider].DefaultFile), props); err != nil {
					return err
				}
				pm.console.Message(ctx,
					fmt.Sprintf(
						"The %s file has been created at %s. You can use it as-is or modify it to suit your needs.",
						output.WithHighLightFormat(filepath.Base(defaultFilePath)),
						output.WithHighLightFormat(filepath.Join(dirPath,
							pipelineProviderFiles[props.CiProvider].DefaultFile))),
				)
				pm.console.Message(ctx, "")
				created = true
			}

			if created {
				break
			}
		}

		if !created {
			log.Printf("User declined creation of %s file at %s", filepath.Base(defaultFilePath), dirPaths)
		}

		return nil
	}

	log.Printf("User declined creation of %s file at %s", filepath.Base(defaultFilePath), dirPaths)
	return nil
}

func generatePipelineDefinition(path string, props projectProperties) error {
	embedFilePath := fmt.Sprintf("pipeline/.%s/azure-dev.ymlt", props.CiProvider)
	tmpl, err := template.
		New("azure-dev.yml").
		Option("missingkey=error").
		ParseFS(resources.PipelineFiles, embedFilePath)
	if err != nil {
		return fmt.Errorf("parsing embedded file %s: %w", embedFilePath, err)
	}
	builder := strings.Builder{}
	tmplContext := struct {
		BranchName             string
		FedCredLogIn           bool
		InstallDotNetForAspire bool
		Variables              []string
		Secrets                []string
		AlphaFeatures          []string
		IsTerraform            bool
	}{
		BranchName:             props.BranchName,
		FedCredLogIn:           props.AuthType == AuthTypeFederated,
		InstallDotNetForAspire: props.HasAppHost,
		Variables:              props.Variables,
		Secrets:                props.Secrets,
		AlphaFeatures:          props.RequiredAlphaFeatures,
		IsTerraform:            props.InfraProvider == infraProviderTerraform,
	}

	// Apply provider parameters
	for _, param := range props.providerParameters {
		for _, envVarName := range param.EnvVarMapping {
			if param.Secret {
				tmplContext.Secrets = append(tmplContext.Secrets, envVarName)
			} else {
				tmplContext.Variables = append(tmplContext.Variables, envVarName)
			}
		}
	}

	if props.InfraProvider == infraProviderTerraform {
		// terraform provider does not resolve this variables automatically, AZD needs to define them
		tmplContext.Variables = append(tmplContext.Variables, "AZURE_LOCATION")
		tmplContext.Variables = append(tmplContext.Variables, "AZURE_ENV_NAME")

		if props.AuthType == AuthTypeClientCredentials {
			tmplContext.Secrets = append(tmplContext.Secrets, "AZURE_CLIENT_SECRET")
		}
	}

	err = tmpl.Execute(&builder, tmplContext)
	if err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	contents := []byte(builder.String())
	log.Printf("Creating file %s", path)
	if err := os.WriteFile(path, contents, osutil.PermissionFile); err != nil {
		return fmt.Errorf("creating file %s: %w", path, err)
	}
	return nil
}

// hasPipelineFile checks if any pipeline files exist for the given provider in the specified repository root.
func hasPipelineFile(provider ciProviderType, repoRoot string) bool {
	for _, path := range pipelineProviderFiles[provider].Files {
		fullPath := filepath.Join(repoRoot, path)
		if osutil.FileExists(fullPath) {
			return true
		}
	}
	return false
}

func (pm *PipelineManager) determineProvider(ctx context.Context, repoRoot string) (ciProviderType, error) {
	log.Printf("Checking for CI/CD YAML files in the repository root: %s", repoRoot)

	// Check for existence of official YAML files in the repo root
	hasGitHubYml := hasPipelineFile(ciProviderGitHubActions, repoRoot)
	hasAzDevOpsYml := hasPipelineFile(ciProviderAzureDevOps, repoRoot)

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
		return ciProviderGitHubActions, nil

	case hasAzDevOpsYml && !hasGitHubYml:
		// Azure DevOps YAML found, GitHub Actions YAML not found
		log.Printf("Only Azure DevOps YAML found. Selecting Azure DevOps as the provider.")
		return ciProviderAzureDevOps, nil

	default:
		// Default to GitHub Actions if no provider is specified
		log.Printf("Defaulting to GitHub Actions as the provider.")
		return ciProviderGitHubActions, nil
	}
}

// promptForProvider prompts the user to select a CI/CD provider.
func (pm *PipelineManager) promptForProvider(ctx context.Context) (ciProviderType, error) {
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
		return ciProviderGitHubActions, nil
	} else if choice == 1 {
		return ciProviderAzureDevOps, nil
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

// SetParameters adds parameter configuration for the manager to use during pipeline config.
// Parameters passed here are automatically defined as variables or secrets in the pipeline without user explicitly
// defining them in the azure.yaml -> pipeline file.
// This is useful for provisioning providers to define a list of parameters that are required for the pipeline.
// If parameters is nil, it means that the pipeline manager should not set up any parameters automatically.
func (pm *PipelineManager) SetParameters(parameters []provisioning.Parameter) {
	if pm.configOptions == nil {
		pm.configOptions = &configurePipelineOptions{}
	}
	pm.configOptions.providerParameters = parameters
}

func (pm *PipelineManager) ensurePipelineDefinition(ctx context.Context) error {
	// pipeline definition files
	hasAppHost := pm.importManager.HasAppHost(ctx, pm.prjConfig)

	infraProvider, err := toInfraProviderType(string(pm.infra.Options.Provider))
	if err != nil {
		return err
	}

	var requiredAlphaFeatures []string
	if pm.infra.IsCompose {
		requiredAlphaFeatures = append(requiredAlphaFeatures, "compose")
	}
	// There are 2 possible options, for the git branch name, when running azd pipeline config:
	// - There is not a git repo, so the branch name is empty. In this case, we default to "main".
	// - There is a git repo and we can get the name of the current branch.
	branchName := "main"
	projectDir := pm.azdCtx.ProjectDirectory()
	repoRoot, err := pm.gitCli.GetRepoRoot(ctx, projectDir)
	if err != nil {
		repoRoot = projectDir
		log.Printf("using project root as repo root, since git repo wasn't available: %s", err)
	}
	customBranchName, err := pm.gitCli.GetCurrentBranch(ctx, repoRoot)
	// It is fine if we can't get the branch name, we will default to "main"
	if err == nil {
		branchName = customBranchName
	}

	// default auth type for all providers
	authType := AuthTypeFederated

	// Check and prompt for missing CI/CD files
	err = pm.checkAndPromptForProviderFiles(
		ctx, projectProperties{
			CiProvider:            pm.ciProviderType,
			RepoRoot:              repoRoot,
			InfraProvider:         infraProvider,
			HasAppHost:            hasAppHost,
			BranchName:            branchName,
			AuthType:              authType,
			Variables:             pm.prjConfig.Pipeline.Variables,
			Secrets:               pm.prjConfig.Pipeline.Secrets,
			RequiredAlphaFeatures: requiredAlphaFeatures,
			providerParameters:    pm.configOptions.providerParameters,
		})
	if err != nil {
		return err
	}
	pm.configOptions.projectSecrets = slices.Clone(pm.prjConfig.Pipeline.Secrets)
	pm.configOptions.projectVariables = slices.Clone(pm.prjConfig.Pipeline.Variables)
	pm.configOptions.provisioningProvider = &pm.infra.Options
	return nil
}

type promptForServiceTreeIdOptions struct {
	PreviousWasInvalid string
}

// promptForServiceTreeId prompts the user to input a Service Tree ID (OID)
// and validates that it's a valid UUID format
func (pm *PipelineManager) promptForServiceTreeId(ctx context.Context, opts promptForServiceTreeIdOptions) (string, error) {
	if opts.PreviousWasInvalid != "" {
		pm.console.Message(ctx, "  The service tree ID you entered is invalid.")
		pm.console.Message(ctx, "  "+opts.PreviousWasInvalid)
	} else {
		pm.console.Message(ctx, "  A Service Tree ID is required for creating the service principal for your Tenant.")
		pm.console.Message(ctx, "  This should be a valid UUID in the format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
	}
	pm.console.Message(ctx, "")

	for {
		serviceTreeId, err := pm.console.Prompt(ctx, input.ConsoleOptions{
			Message: "Enter Service Tree ID:",
		})
		if err != nil {
			return "", err
		}

		// Validate UUID format using the google/uuid package
		if _, err := uuid.Parse(serviceTreeId); err == nil {
			return serviceTreeId, nil
		}

		pm.console.Message(ctx, "")
		pm.console.Message(ctx,
			"Invalid UUID format. Please enter a valid UUID in the format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
		pm.console.Message(ctx, "")
	}
}
