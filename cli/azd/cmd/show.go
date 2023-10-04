package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type showFlags struct {
	global *internal.GlobalCommandOptions
	envFlag
}

func (s *showFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	s.envFlag.Bind(local, global)
	s.global = global
}

func newShowFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *showFlags {
	flags := &showFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "show --output json",
		Short:  "Display information about your app and its resources.",
		Hidden: true,
	}

	return cmd
}

type showAction struct {
	projectConfig        *project.ProjectConfig
	console              input.Console
	formatter            output.Formatter
	writer               io.Writer
	azCli                azcli.AzCli
	envManager           environment.Manager
	deploymentOperations azapi.DeploymentOperations
	azdCtx               *azdcontext.AzdContext
	flags                *showFlags
}

func newShowAction(
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	azCli azcli.AzCli,
	envManager environment.Manager,
	deploymentOperations azapi.DeploymentOperations,
	projectConfig *project.ProjectConfig,
	azdCtx *azdcontext.AzdContext,
	flags *showFlags,
) actions.Action {
	return &showAction{
		projectConfig:        projectConfig,
		console:              console,
		formatter:            formatter,
		writer:               writer,
		azCli:                azCli,
		envManager:           envManager,
		deploymentOperations: deploymentOperations,
		azdCtx:               azdCtx,
		flags:                flags,
	}
}

func (s *showAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	res := contracts.ShowResult{
		Name:     s.projectConfig.Name,
		Services: make(map[string]contracts.ShowService, len(s.projectConfig.Services)),
	}

	for name, svc := range s.projectConfig.Services {
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

		res.Services[name] = showSvc
	}

	// Add information about the target of each service, if we can determine it (if the infrastructure has
	// not been deployed, for example, we'll just not include target information)
	//
	// Before we can discover resources, we need to load the current environment.  We do this ourselves instead of
	// having an environment injected into us so we can handle cases where the current environment doesn't exist (if we
	// injected an environment, we'd prompt the user to see if they want to created one and we'd prefer not to have show
	// interact with the user).
	environmentName := s.flags.environmentName

	if environmentName == "" {
		var err error
		environmentName, err = s.azdCtx.GetDefaultEnvironmentName()
		if err != nil {
			log.Printf("could not determine current environment: %s, resource ids will not be available", err)
		}

	}

	if env, err := s.envManager.Get(ctx, environmentName); err != nil {
		log.Printf("could not load environment: %s, resource ids will not be available", err)
	} else {
		if subId := env.GetSubscriptionId(); subId == "" {
			log.Printf("provision has not been run, resource ids will not be available")
		} else {
			azureResourceManager := infra.NewAzureResourceManager(s.azCli, s.deploymentOperations)
			resourceManager := project.NewResourceManager(env, s.azCli, s.deploymentOperations)
			envName := env.GetEnvName()

			rgName, err := azureResourceManager.FindResourceGroupForEnvironment(ctx, subId, envName)
			if err == nil {
				for svcName, serviceConfig := range s.projectConfig.Services {
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
						res.Services[svcName] = resSvc
					} else {
						log.Printf("ignoring error determining resource id for service %s: %v", svcName, err)
					}
				}
			} else {
				log.Printf(
					"ignoring error determining resource group for environment %s, resource ids will not be available: %v",
					env.GetEnvName(),
					err)
			}
		}
	}

	return nil, s.formatter.Format(res, s.writer, nil)
}

func showTypeFromLanguage(language project.ServiceLanguageKind) contracts.ShowType {
	switch language {
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
