package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type showFlags struct {
	outputFormat string
	global       *internal.GlobalCommandOptions
}

func (s *showFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	output.AddOutputFlag(local, &s.outputFormat, []output.Format{output.JsonFormat}, output.NoneFormat)
	s.global = global
}

func showCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *showFlags) {
	cmd := &cobra.Command{
		Use:    "show",
		Short:  "Display information about your application and its resources.",
		Hidden: true,
	}

	flags := &showFlags{}
	flags.Bind(cmd.Flags(), global)
	return cmd, flags
}

type showAction struct {
	console   input.Console
	formatter output.Formatter
	writer    io.Writer
	azdCtx    *azdcontext.AzdContext
	flags     showFlags
}

func newShowAction(
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	azdCtx *azdcontext.AzdContext,
	flags showFlags,
) *showAction {
	return &showAction{
		console:   console,
		formatter: formatter,
		writer:    writer,
		azdCtx:    azdCtx,
		flags:     flags,
	}
}

func (s *showAction) Run(ctx context.Context) error {
	if err := ensureProject(s.azdCtx.ProjectPath()); err != nil {
		return err
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &s.flags.global.EnvironmentName, s.azdCtx, s.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(s.azdCtx.ProjectPath(), env)
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	res := contracts.ShowResult{
		Name:     prj.Name,
		Services: make(map[string]contracts.ShowService, len(prj.Services)),
	}

	for name, svc := range prj.Services {
		path, err := getFullPathToProjectForService(svc)
		if err != nil {
			return err
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
	resourceManager := infra.NewAzureResourceManager(ctx)

	if resourceGroupName, err := resourceManager.FindResourceGroupForEnvironment(ctx, env); err == nil {
		for name := range prj.Services {
			if resources, err := project.GetServiceResources(ctx, resourceGroupName, name, env); err == nil {
				resourceIds := make([]string, len(resources))
				for idx, res := range resources {
					resourceIds[idx] = res.Id
				}

				resSvc := res.Services[name]
				resSvc.Target = &contracts.ShowTargetArm{
					ResourceIds: resourceIds,
				}
				res.Services[name] = resSvc
			} else {
				log.Printf("ignoring error determining resource id for service %s: %v", name, err)
			}
		}
	} else {
		log.Printf("ignoring error determining resource group for environment %s, resource ids will not be available: %v",
			env.GetEnvName(),
			err)
	}

	return s.formatter.Format(res, s.writer, nil)
}

func showTypeFromLanguage(language string) contracts.ShowType {
	switch language {
	case "dotnet":
		return contracts.ShowTypeDotNet
	case "py", "python":
		return contracts.ShowTypePython
	case "ts", "js":
		return contracts.ShowTypeNode
	default:
		panic(fmt.Sprintf("unknown language %s", language))
	}
}

// getFullPathToProjectForService returns the full path to the source project for a given service. For dotnet services,
// this includes the project file (e.g Todo.Api.csproj). For dotnet services, if the `path` component of the configuration
// does not include the project file, we attempt to determine it by looking for a single .csproj/.vbproj/.fsproj file
// in that directory. If there are multiple, an error is returned.
func getFullPathToProjectForService(svc *project.ServiceConfig) (string, error) {
	if svc.Language != "dotnet" {
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
							"please include the name of the .NET project file in 'project' "+
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
					" please include the name of the .NET project file in project setting in %s for"+
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
