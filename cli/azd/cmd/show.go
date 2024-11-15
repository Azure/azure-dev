package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type showFlags struct {
	global      *internal.GlobalCommandOptions
	showSecrets bool
	internal.EnvFlag
}

func (s *showFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	s.EnvFlag.Bind(local, global)
	local.BoolVar(
		&s.showSecrets,
		"show-secrets",
		false,
		"Unmask secrets in output.",
	)
	s.global = global
}

func newShowFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *showFlags {
	flags := &showFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Short: "Display information about your app and its resources.",
	}

	return cmd
}

type showAction struct {
	projectConfig        *project.ProjectConfig
	importManager        *project.ImportManager
	console              input.Console
	formatter            output.Formatter
	writer               io.Writer
	resourceService      *azapi.ResourceService
	envManager           environment.Manager
	infraResourceManager infra.ResourceManager
	azdCtx               *azdcontext.AzdContext
	flags                *showFlags
	args                 []string
	creds                account.SubscriptionCredentialProvider
	armClientOptions     *arm.ClientOptions
	featureManager       *alpha.FeatureManager
	lazyServiceManager   *lazy.Lazy[project.ServiceManager]
	lazyResourceManager  *lazy.Lazy[project.ResourceManager]
	portalUrlBase        string
}

func newShowAction(
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	resourceService *azapi.ResourceService,
	envManager environment.Manager,
	infraResourceManager infra.ResourceManager,
	projectConfig *project.ProjectConfig,
	importManager *project.ImportManager,
	featureManager *alpha.FeatureManager,
	armClientOptions *arm.ClientOptions,
	creds account.SubscriptionCredentialProvider,
	azdCtx *azdcontext.AzdContext,
	flags *showFlags,
	args []string,
	lazyServiceManager *lazy.Lazy[project.ServiceManager],
	lazyResourceManager *lazy.Lazy[project.ResourceManager],
	cloud *cloud.Cloud,
) actions.Action {
	return &showAction{
		projectConfig:        projectConfig,
		importManager:        importManager,
		console:              console,
		formatter:            formatter,
		writer:               writer,
		resourceService:      resourceService,
		envManager:           envManager,
		infraResourceManager: infraResourceManager,
		featureManager:       featureManager,
		armClientOptions:     armClientOptions,
		creds:                creds,
		azdCtx:               azdCtx,
		args:                 args,
		flags:                flags,
		lazyServiceManager:   lazyServiceManager,
		lazyResourceManager:  lazyResourceManager,
		portalUrlBase:        cloud.PortalUrlBase,
	}
}

func (s *showAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	s.console.ShowSpinner(ctx, "Gathering information about your app and its resources...", input.Step)
	defer s.console.StopSpinner(ctx, "", input.Step)

	res := contracts.ShowResult{
		Name:     s.projectConfig.Name,
		Services: make(map[string]contracts.ShowService),
	}

	stableServices, err := s.importManager.ServiceStable(ctx, s.projectConfig)
	if err != nil {
		return nil, err
	}

	for _, svc := range stableServices {
		path, err := getFullPathToProjectForService(svc)
		if err != nil {
			return nil, err
		}

		showSvc := contracts.ShowService{
			Project: contracts.ShowServiceProject{
				Path: path,
				Type: showTypeFromLanguage(svc.Language),
			},
		}

		res.Services[svc.Name] = showSvc
	}

	// Add information about the target of each service, if we can determine it (if the infrastructure has
	// not been deployed, for example, we'll just not include target information)
	//
	// Before we can discover resources, we need to load the current environment.  We do this ourselves instead of
	// having an environment injected into us so we can handle cases where the current environment doesn't exist (if we
	// injected an environment, we'd prompt the user to see if they want to created one and we'd prefer not to have show
	// interact with the user).
	environmentName := s.flags.EnvironmentName

	if environmentName == "" {
		var err error
		environmentName, err = s.azdCtx.GetDefaultEnvironmentName()
		if err != nil {
			log.Printf("could not determine current environment: %s, resource ids will not be available", err)
		}

	}

	var subId, rgName string
	if env, err := s.envManager.Get(ctx, environmentName); err != nil {
		if errors.Is(err, environment.ErrNotFound) && s.flags.EnvironmentName != "" {
			return nil, fmt.Errorf(
				`"environment '%s' does not exist. You can create it with "azd env new"`, environmentName,
			)
		}
		log.Printf("could not load environment: %s, resource ids will not be available", err)
	} else {
		if subId = env.GetSubscriptionId(); subId == "" {
			log.Printf("provision has not been run, resource ids will not be available")
		} else {
			resourceManager, err := s.lazyResourceManager.GetValue()
			if err != nil {
				return nil, err
			}

			envName := env.Name()

			if s.featureManager.IsEnabled(composeFeature) && len(s.args) > 0 {
				name := s.args[0]
				err := s.showResource(ctx, name, env)
				if err != nil {
					return nil, err
				}

				return nil, nil
			}

			rgName, err = s.infraResourceManager.FindResourceGroupForEnvironment(ctx, subId, envName)
			if err == nil {
				for _, serviceConfig := range stableServices {
					svcName := serviceConfig.Name
					resources, err := resourceManager.GetServiceResources(ctx, subId, rgName, serviceConfig)
					if err == nil {
						resourceIds := make([]string, len(resources))
						for idx, res := range resources {
							resourceIds[idx] = res.Id
						}

						resSvc := res.Services[svcName]
						resSvc.Target = &contracts.ShowTargetArm{
							ResourceIds: resourceIds,
						}
						resSvc.IngresUrl = s.serviceEndpoint(ctx, subId, serviceConfig, env)
						res.Services[svcName] = resSvc
					} else {
						log.Printf("ignoring error determining resource id for service %s: %v", svcName, err)
					}
				}
			} else {
				log.Printf(
					"ignoring error determining resource group for environment %s, resource ids will not be available: %v",
					env.Name(),
					err)
			}
		}
	}

	if s.formatter.Kind() == output.JsonFormat {
		return nil, s.formatter.Format(res, s.writer, nil)
	}

	appEnvironments, err := s.envManager.List(ctx)
	if err != nil {
		return nil, err
	}

	uxEnvironments := make([]*ux.ShowEnvironment, len(appEnvironments))
	for index, environment := range appEnvironments {
		uxEnvironments[index] = &ux.ShowEnvironment{
			Name:      environment.Name,
			IsCurrent: environment.Name == environmentName,
			IsRemote:  !environment.HasLocal && environment.HasRemote,
		}
	}

	uxServices := make([]*ux.ShowService, len(res.Services))
	var index int
	for serviceName, service := range res.Services {
		uxServices[index] = &ux.ShowService{
			Name:      serviceName,
			IngresUrl: service.IngresUrl,
		}
		index++
	}

	s.console.MessageUxItem(ctx, &ux.Show{
		AppName:         s.projectConfig.Name,
		Services:        uxServices,
		Environments:    uxEnvironments,
		AzurePortalLink: azurePortalLink(s.portalUrlBase, subId, rgName),
	})

	return nil, nil
}

func (s *showAction) showResource(ctx context.Context, name string, env *environment.Environment) error {
	id, err := infra.ResourceId(name, env)
	if err != nil {
		return fmt.Errorf("resolving '%s': %w", name, err)
	}

	subscriptionId := id.SubscriptionID
	armOptions := s.armClientOptions

	resourceOptions := showResourceOptions{
		showSecrets: s.flags.showSecrets,
		clientOpts:  armOptions,
	}

	credential, err := s.creds.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return err
	}

	resType := id.ResourceType.Namespace + "/" + id.ResourceType.Type
	var item ux.UxItem
	switch {
	case strings.EqualFold(resType, "Microsoft.App/containerApps"):
		item, err = showContainerApp(ctx, credential, id, resourceOptions)
		if err != nil {
			return err
		}
	case strings.EqualFold(resType, "Microsoft.CognitiveServices/accounts/deployments"):
		err = showModelDeployment(ctx, s.console, credential, id.Parent, resourceOptions)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("resource type '%s' is not currently supported in alpha", resType)
	}

	if item != nil {
		s.console.MessageUxItem(ctx, item)
	}
	return nil
}

type showResourceOptions struct {
	showSecrets bool
	clientOpts  *arm.ClientOptions
}

func showContainerApp(
	ctx context.Context,
	cred azcore.TokenCredential,
	id *arm.ResourceID,
	opts showResourceOptions) (*ux.ShowService, error) {
	service := &ux.ShowService{
		Name: id.Name,
		Env:  make(map[string]string),
	}
	client, err := armappcontainers.NewContainerAppsClient(id.SubscriptionID, cred, opts.clientOpts)
	if err != nil {
		return nil, fmt.Errorf("creating container-apps client: %w", err)
	}

	app, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("getting container app: %w", err)
	}

	var secrets []*armappcontainers.ContainerAppSecret // secret name to value translations
	if opts.showSecrets {
		secretsRes, err := client.ListSecrets(ctx, id.ResourceGroupName, id.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("listing secrets: %w", err)
		}
		secrets = secretsRes.Value
	}

	if len(app.Properties.Template.Containers) == 0 {
		return service, nil
	}

	service.IngresUrl = fmt.Sprintf("https://%s", *app.Properties.Configuration.Ingress.Fqdn)

	var container *armappcontainers.Container
	if len(app.Properties.Template.Containers) == 1 {
		container = app.Properties.Template.Containers[0]
	} else {
		for _, c := range app.Properties.Template.Containers {
			if c.Name != nil && (strings.EqualFold(*c.Name, id.Name) || strings.EqualFold(*c.Name, "main")) {
				container = c
				break
			}
		}

		if container == nil {
			return nil, fmt.Errorf(
				"container app %s has more than one container, and no containers match the name 'main' or '%s'",
				id.Name,
				id.Name)
		}
	}

	envVar := container.Env
	for _, env := range envVar {
		if env.Name == nil {
			continue
		}

		key := *env.Name
		val := env.Value

		if env.SecretRef != nil {
			val = to.Ptr("*******")

			// dereference the secret ref
			for _, secret := range secrets {
				if *env.SecretRef == *secret.Name {
					val = secret.Value
					break
				}
			}
		}

		service.Env[key] = *val
	}

	return service, nil
}

func showModelDeployment(
	ctx context.Context,
	console input.Console,
	cred azcore.TokenCredential,
	id *arm.ResourceID,
	opts showResourceOptions) error {
	client, err := armcognitiveservices.NewAccountsClient(id.SubscriptionID, cred, opts.clientOpts)
	if err != nil {
		return fmt.Errorf("creating accounts client: %w", err)
	}

	account, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return fmt.Errorf("getting account: %w", err)
	}

	if account.Properties.Endpoint != nil {
		console.Message(ctx, color.HiMagentaString("%s (Azure AI Services Model Deployment)", id.Name))
		console.Message(ctx, "  Endpoint:")
		console.Message(ctx, color.HiBlueString(fmt.Sprintf("    AZURE_OPENAI_ENDPOINT=%s", *account.Properties.Endpoint)))
		console.Message(ctx, "  Access:")
		console.Message(ctx, "    Keyless (Microsoft Entra ID)")
		//nolint:lll
		console.Message(ctx, output.WithGrayFormat("        Hint: To access locally, use DefaultAzureCredential. To learn more, visit https://learn.microsoft.com/en-us/azure/ai-services/openai/supported-languages"))

		console.Message(ctx, "")
	}

	return nil
}

func (s *showAction) serviceEndpoint(
	ctx context.Context, subId string, serviceConfig *project.ServiceConfig, env *environment.Environment) string {
	resourceManager, err := s.lazyResourceManager.GetValue()
	if err != nil {
		log.Printf("error: getting lazy target-resource. Endpoints will be empty: %v", err)
		return ""
	}
	targetResource, err := resourceManager.GetTargetResource(ctx, subId, serviceConfig)
	if err != nil {
		log.Printf("error: getting target-resource. Endpoints will be empty: %v", err)
		return ""
	}

	serviceManager, err := s.lazyServiceManager.GetValue()
	if err != nil {
		log.Printf("error: getting lazy service manager. Endpoints will be empty: %v", err)
		return ""
	}
	st, err := serviceManager.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		log.Printf("error: getting service target. Endpoints will be empty: %v", err)
		return ""
	}
	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		log.Printf("error: getting service endpoints. Endpoints might be empty: %v", err)
	}

	overriddenEndpoints := project.OverriddenEndpoints(ctx, serviceConfig, env)
	if len(overriddenEndpoints) > 0 {
		endpoints = overriddenEndpoints
	}

	if len(endpoints) == 0 {
		return ""
	}

	return endpoints[0]
}

func showTypeFromLanguage(language project.ServiceLanguageKind) contracts.ShowType {
	switch language {
	case project.ServiceLanguageNone:
		return contracts.ShowTypeNone
	case project.ServiceLanguageDotNet, project.ServiceLanguageCsharp, project.ServiceLanguageFsharp:
		return contracts.ShowTypeDotNet
	case project.ServiceLanguagePython:
		return contracts.ShowTypePython
	case project.ServiceLanguageTypeScript, project.ServiceLanguageJavaScript:
		return contracts.ShowTypeNode
	case project.ServiceLanguageJava:
		return contracts.ShowTypeJava
	default:
		panic(fmt.Sprintf("unknown language %s", language))
	}
}

// getFullPathToProjectForService returns the full path to the source project for a given service. For dotnet services,
// this includes the project file (e.g Todo.Api.csproj). For dotnet services, if the `path` component of the configuration
// does not include the project file, we attempt to determine it by looking for a single .csproj/.vbproj/.fsproj file
// in that directory. If there are multiple, an error is returned.
func getFullPathToProjectForService(svc *project.ServiceConfig) (string, error) {
	if showTypeFromLanguage(svc.Language) != contracts.ShowTypeDotNet {
		return svc.Path(), nil
	}

	stat, err := os.Stat(svc.Path())
	if err != nil {
		return "", fmt.Errorf("stating project %s: %w", svc.Path(), err)
	} else if stat.IsDir() {
		entries, err := os.ReadDir(svc.Path())
		if err != nil {
			return "", fmt.Errorf("listing files for service %s: %w", svc.Name, err)
		}
		var projectFile string
		for _, entry := range entries {
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".csproj", ".fsproj", ".vbproj":
				if projectFile != "" {
					// we found multiple project files, we need to ask the user to specify which one
					// corresponds to the service.
					return "", fmt.Errorf(
						"multiple .NET project files detected in %s for service %s, "+
							"include the name of the .NET project file in 'project' "+
							"setting in %s for this service",
						svc.Path(),
						svc.Name,
						azdcontext.ProjectFileName)
				} else {
					projectFile = entry.Name()
				}
			}
		}
		if projectFile == "" {
			return "", fmt.Errorf(
				"could not determine the .NET project file for service %s,"+
					" include the name of the .NET project file in project setting in %s for"+
					" this service",
				svc.Name,
				azdcontext.ProjectFileName)
		} else {
			if svc.RelativePath != "" {
				svc.RelativePath = filepath.Join(svc.RelativePath, projectFile)
			} else {
				svc.Project.Path = filepath.Join(svc.Project.Path, projectFile)
			}
		}
	}

	return svc.Path(), nil
}
