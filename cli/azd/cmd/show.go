package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
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
	global  *internal.GlobalCommandOptions
	secrets bool
	internal.EnvFlag
}

func (s *showFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	s.EnvFlag.Bind(local, global)
	local.BoolVar(&s.secrets, "secrets", false, "Display secrets.")
	s.global = global
}

func newShowFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *showFlags {
	flags := &showFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <resource path>",
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
	lazyServiceManager   *lazy.Lazy[project.ServiceManager]
	lazyResourceManager  *lazy.Lazy[project.ResourceManager]
	account              account.SubscriptionCredentialProvider
	portalUrlBase        string
	args                 []string
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
	azdCtx *azdcontext.AzdContext,
	flags *showFlags,
	args []string,
	lazyServiceManager *lazy.Lazy[project.ServiceManager],
	lazyResourceManager *lazy.Lazy[project.ResourceManager],
	account account.SubscriptionCredentialProvider,
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
		azdCtx:               azdCtx,
		flags:                flags,
		lazyServiceManager:   lazyServiceManager,
		lazyResourceManager:  lazyResourceManager,
		args:                 args,
		account:              account,
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

	showService := ""
	if len(s.args) >= 1 {
		for _, ss := range stableServices {
			if ss.Name == s.args[0] {
				showService = ss.Name
				break
			}
		}
	}

	showResource := ""
	if showService == "" && len(s.args) >= 1 {
		if _, ok := s.projectConfig.Resources[s.args[0]]; ok {
			showResource = s.args[0]
		}
	}

	if len(s.args) >= 1 && showService == "" && showResource == "" {
		return nil, fmt.Errorf("service/resource %s not found", s.args[0])
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
	env, err := s.envManager.Get(ctx, environmentName)
	if err != nil {
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

			rgName, err = s.infraResourceManager.FindResourceGroupForEnvironment(ctx, subId, envName)
			if err == nil {
				for _, serviceConfig := range stableServices {
					if showService != "" && showService != serviceConfig.Name {
						continue
					}

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
						resSvc.RemoteEnviron = s.serviceEnviron(ctx, subId, serviceConfig, project.EnvironOptions{
							Secrets: s.flags.secrets,
						})
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

	// TODO(weilim): prototype only

	if len(showResource) > 0 {
		res := s.projectConfig.Resources[showResource]
		switch res.Type {
		case project.ResourceTypeOpenAiModel:
			accountId := env.Dotenv()["AZURE_COGNITIVE_ACCOUNT_ID"]
			cred, err := s.account.CredentialForSubscription(ctx, subId)
			if err != nil {
				return nil, err
			}
			client, err := armcognitiveservices.NewAccountsClient(subId, cred, nil)
			if err != nil {
				return nil, fmt.Errorf("creating accounts client: %w", err)
			}

			resId, err := arm.ParseResourceID(accountId)
			if err != nil {
				return nil, fmt.Errorf("parsing resource id: %w", err)
			}
			account, err := client.Get(ctx, rgName, resId.Name, nil)
			if err != nil {
				return nil, fmt.Errorf("getting account: %w", err)
			}

			if account.Properties.Endpoint != nil {
				s.console.Message(ctx, color.HiMagentaString("%s (deployed model)", res.Name))
				s.console.Message(ctx, "  Endpoint:")
				s.console.Message(ctx, fmt.Sprintf("    AZURE_OPENAI_ENDPOINT=%s", *account.Properties.Endpoint))
				s.console.Message(ctx, "  Access:")
				s.console.Message(ctx, "      Keyless (Microsoft Entra ID)")
				s.console.Message(ctx, output.WithGrayFormat("        Hint: To access locally, use DefaultAzureCredential. To learn more, visit https://learn.microsoft.com/en-us/azure/ai-services/openai/supported-languages"))

				s.console.Message(ctx, "")
			}

			return nil, nil
		default:
			return nil, fmt.Errorf("resource type %s not supported", res.Type)
		}
	}

	if len(showService) > 0 {
		s.console.Message(ctx, "Blueprint")
		if res, ok := s.projectConfig.Resources[showService]; ok {
			s.console.Message(ctx, fmt.Sprintf("%s (%s)", res.Name, "Azure Container App"))
			for _, dep := range res.Uses {
				if r, ok := s.projectConfig.Resources[dep]; ok {
					host := ""
					switch r.Type {
					case project.ResourceTypeDbRedis:
						host = "Azure Redis for Cache"
					case project.ResourceTypeDbMongo:
						host = "Azure Cosmos DB for MongoDB"
					case project.ResourceTypeDbPostgres:
						host = "Azure Database for PostgreSQL flexible server"
					}
					s.console.Message(ctx, fmt.Sprintf("  ╰─ %s (%s)", r.Name, host))
				}
			}
		}
		return nil, nil
	}

	if len(showService) > 0 && s.args[1] == "env" {
		s.console.Message(ctx, fmt.Sprintf("Environment variables for %s", showService))
		environ := res.Services[showService].RemoteEnviron
		show := []string{}
		show = append(show, "Key\tValue")
		show = append(show, "------\t-----")

		env := []string{}
		for k, v := range environ {
			env = append(env, fmt.Sprintf("%s\t%s", k, v))
		}
		slices.Sort(env)
		show = append(show, env...)

		formatted, err := tabWrite(show, 10)
		if err != nil {
			return nil, err
		}
		s.console.Message(ctx, strings.Join(formatted, "\n"))
		return nil, nil
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
		AppName:         s.azdCtx.GetDefaultProjectName(),
		Services:        uxServices,
		Environments:    uxEnvironments,
		AzurePortalLink: azurePortalLink(s.portalUrlBase, subId, rgName),
	})

	return nil, nil
}

// tabWrite transforms tabbed output into formatted strings with a given minimal padding.
// For more information, refer to the tabwriter package.
func tabWrite(selections []string, padding int) ([]string, error) {
	tabbed := strings.Builder{}
	tabW := tabwriter.NewWriter(&tabbed, 0, 0, padding, ' ', 0)
	_, err := tabW.Write([]byte(strings.Join(selections, "\n")))
	if err != nil {
		return nil, err
	}
	err = tabW.Flush()
	if err != nil {
		return nil, err
	}

	return strings.Split(tabbed.String(), "\n"), nil
}

func (s *showAction) serviceEnviron(
	ctx context.Context, subId string, serviceConfig *project.ServiceConfig,
	environOptions project.EnvironOptions) map[string]string {
	resourceManager, err := s.lazyResourceManager.GetValue()
	if err != nil {
		log.Printf("error: getting lazy target-resource. Environ will be empty: %v", err)
		return map[string]string{}
	}
	targetResource, err := resourceManager.GetTargetResource(ctx, subId, serviceConfig)
	if err != nil {
		log.Printf("error: getting target-resource. Environ will be empty: %v", err)
		return map[string]string{}
	}

	serviceManager, err := s.lazyServiceManager.GetValue()
	if err != nil {
		log.Printf("error: getting lazy service manager. Environ will be empty: %v", err)
		return map[string]string{}
	}
	st, err := serviceManager.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		log.Printf("error: getting service target. Environ will be empty: %v", err)
		return map[string]string{}
	}

	environ, err := st.Environ(ctx, serviceConfig, targetResource, environOptions)
	if err != nil {
		log.Printf("error: getting service target. Environ will be empty: %v", err)
		return map[string]string{}
	}

	return environ
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
