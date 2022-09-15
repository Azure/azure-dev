package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
)

func showCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		console := input.GetConsole(ctx)

		formatter := output.GetFormatter(ctx)
		writer := output.GetWriter(ctx)

		// Right now this command is hidden and we only expect it to be called by tooling,
		// which passes `--output json`. If for some reason someone ran it directly, just
		// don't do anything.
		if formatter.Kind() != output.JsonFormat {
			return nil
		}

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		env, ctx, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, console)
		if err != nil {
			return fmt.Errorf("loading environment: %w", err)
		}

		prj, err := project.LoadProjectConfig(azdCtx.ProjectPath(), &env)
		if err != nil {
			return fmt.Errorf("loading project: %w", err)
		}

		res := showResult{
			Services: make(map[string]showService, len(prj.Services)),
		}

		for name, svc := range prj.Services {
			path, err := getFullPathToProjectForService(svc)
			if err != nil {
				return err
			}

			showSvc := showService{
				Project: showServiceProject{
					Path: path,
					Type: showTypeFromLanguage(svc.Language),
				},
			}

			res.Services[name] = showSvc
		}

		// Add information about the target of each service, if we can determine it (if the infrastructure has
		// not been deployed, for example, we'll just not include target information)
		resourceManager := infra.NewAzureResourceManager(ctx)
		if resourceGroupName, err := resourceManager.FindResourceGroupForEnvironment(ctx, &env); err == nil {
			for name := range prj.Services {
				if resources, err := project.GetServiceResources(ctx, resourceGroupName, name, &env); err == nil {
					if len(resources) == 1 {
						resSvc := res.Services[name]
						resSvc.Target = &showTargetArm{
							ResourceId: resources[0].Id,
						}
						res.Services[name] = resSvc
					}
				} else {
					log.Printf("ignoring error determining resource id for service %s: %v", name, err)
				}
			}
		} else {
			log.Printf("ignoring error determining resource group for environment %s, resource ids will not be available: %v", env.GetEnvName(), err)
		}

		return formatter.Format(res, writer, nil)
	}

	cmd := commands.Build(
		commands.ActionFunc(action),
		rootOptions,
		"show",
		"Display information about your application and its resources.",
		nil,
	)

	output.AddOutputParam(cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)

	cmd.Hidden = true

	return cmd
}

type showResult struct {
	Services map[string]showService `json:"services"`
}

type showService struct {
	// Project contains information about the project that backs this service.
	Project showServiceProject `json:"project"`
	// Target contains information about the resource that the service is deployed
	// to.
	Target *showTargetArm `json:"target,omitempty"`
}

type showServiceProject struct {
	// Path contains the path to the project for this service.
	// When 'type' is 'dotnet', this includes the project file (i.e. Todo.Api.csproj).
	Path string `json:"path"`
	// The type of this project. One of "dotnet", "python", or "node"
	Type string `json:"language"`
}

type showTargetArm struct {
	ResourceId string `json:"resourceId"`
}

func showTypeFromLanguage(language string) string {
	switch language {
	case "dotnet":
		return "dotnet"
	case "py", "python":
		return "python"
	case "ts", "js":
		return "node"
	default:
		panic(fmt.Sprintf("unknown language %s", language))
	}
}

// getFullPathToProjectForService returns the full path to the source project for a given service. For dotnet services,
// this includes the project file (e.g Todo.Api.csproj). For dotnet services, if the `path` component of the configuration
// does not include the project file, we attempt to determine it by looking for a single .csproj/.vbproj/.fsproj file
// in that directory. If there are multiple, an error is returned.
func getFullPathToProjectForService(svc *project.ServiceConfig) (string, error) {
	if svc.Language == "dotnet" {
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
						return "", fmt.Errorf("multiple .NET project files detected in %s for service %s, please include the name of the .NET project file in 'project' setting in %s for this service", svc.Path(), svc.Name, azdcontext.ProjectFileName)
					} else {
						projectFile = entry.Name()
					}
				}
			}
			if projectFile == "" {
				return "", fmt.Errorf("could not determine the .NET project file for service %s, please include the name of the .NET project file in project setting in %s for this service", svc.Name, azdcontext.ProjectFileName)
			} else {
				if svc.RelativePath != "" {
					svc.RelativePath = filepath.Join(svc.RelativePath, projectFile)
				} else {
					svc.Project.Path = filepath.Join(svc.Project.Path, projectFile)
				}
			}
		}
	}

	return svc.Path(), nil
}
