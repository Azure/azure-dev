package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// appHostServiceForProject returns the ServiceConfig of the service for the AppHost project for the given azd project.
func appHostForProject(
	ctx context.Context, pc *project.ProjectConfig, dotnetCli *dotnet.Cli,
) (*project.ServiceConfig, error) {
	for _, service := range pc.Services {
		if service.Language == project.ServiceLanguageDotNet {
			isAppHost, err := dotnetCli.IsAspireHostProject(ctx, service.Path())
			if err != nil {
				log.Printf("error checking if %s is an app host project: %v", service.Path(), err)
			}
			if isAppHost {
				return service, nil
			}
		}
	}

	return nil, fmt.Errorf("no app host project found for project: %s", pc.Name)
}

func servicesFromManifest(manifest *apphost.Manifest) []*Service {
	var services []*Service

	for name, res := range manifest.Resources {
		if res.Type == "project.v0" {
			services = append(services, &Service{
				Name: name,
				Path: *res.Path,
			})
		}
	}

	return services
}

// azdContext resolves the azd context directory to use.
//
//   - If the host project directory contains azure.yaml, the host project directory is used.
//   - If the nearest project directory contains azure.yaml, and the azure.yaml has services matching the given host project,
//     the nearest project directory is used.
//   - Otherwise, the host project directory directory is used by default.
func azdContext(hostProjectPath string) (*azdcontext.AzdContext, error) {
	hostProjectDir := filepath.Dir(hostProjectPath)
	azdCtx, err := azdcontext.NewAzdContextFromWd(hostProjectDir)
	if errors.Is(err, azdcontext.ErrNoProject) {
		// no project exists, use host project directory as the default
		return azdcontext.NewAzdContextWithDirectory(hostProjectDir), nil
	} else if err != nil {
		return nil, err
	}

	// nearest project is in host project directory, use it
	if azdCtx.ProjectDirectory() == hostProjectDir {
		return azdCtx, nil
	}

	// nearest project is not in host project directory, check if it targets the current app host project
	prjConfig, err := project.Load(context.Background(), azdCtx.ProjectPath())
	if err != nil {
		return nil, err
	}

	for _, svc := range prjConfig.Services {
		if svc.Language == project.ServiceLanguageDotNet && svc.Host == project.ContainerAppTarget {
			if svc.Path() != hostProjectPath {
				log.Printf("ignoring %s due to mismatch, using app host directory", azdCtx.ProjectPath())
				return azdcontext.NewAzdContextWithDirectory(hostProjectDir), nil
			}
		}

		// there can only be one app host project
		break
	}

	log.Printf("use nearest directory: %s", azdCtx.ProjectDirectory())
	return azdCtx, nil
}
