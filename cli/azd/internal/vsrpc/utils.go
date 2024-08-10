package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/azdpath"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// appHostServiceForProject returns the ServiceConfig of the service for the AppHost project for the given azd project.
func appHostForProject(
	ctx context.Context, pc *project.ProjectConfig, dotnetCli *dotnet.Cli,
) (*project.ServiceConfig, error) {
	for _, service := range pc.Services {
		if service.Language == project.ServiceLanguageDotNet {
			isAppHost, err := dotnetCli.GetMsBuildProperty(ctx, service.Path(), "IsAspireHost")
			if err != nil {
				log.Printf("error checking if %s is an app host project: %v", service.Path(), err)
			}
			if strings.TrimSpace(isAppHost) == "true" {
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

// azdRoot resolves the azd root directory to use.
//
//   - If the host project directory contains azure.yaml, the host project directory is used.
//   - If the nearest project directory contains azure.yaml, and the azure.yaml has services matching the given host project,
//     the nearest project directory is used.
//   - Otherwise, the host project directory directory is used by default.
func azdRoot(hostProjectPath string) (*azdpath.Root, error) {
	hostProjectDir := filepath.Dir(hostProjectPath)
	azdRoot, err := azdpath.FindRootFromWd(hostProjectDir)
	if errors.Is(err, azdpath.ErrNoProject) {
		// no project exists, use host project directory as the default
		return azdpath.NewRootFromDirectory(hostProjectDir), nil
	} else if err != nil {
		return nil, err
	}

	// nearest project is in host project directory, use it
	if azdRoot.Directory() == hostProjectDir {
		return azdRoot, nil
	}

	// nearest project is not in host project directory, check if it targets the current app host project
	prjConfig, err := project.Load(context.Background(), azdpath.ProjectPath(azdRoot))
	if err != nil {
		return nil, err
	}

	for _, svc := range prjConfig.Services {
		if svc.Language == project.ServiceLanguageDotNet && svc.Host == project.ContainerAppTarget {
			if svc.Path() != hostProjectPath {
				log.Printf("ignoring %s due to mismatch, using app host directory", azdpath.ProjectPath(azdRoot))
				return azdpath.NewRootFromDirectory(hostProjectDir), nil
			}
		}

		// there can only be one app host project
		break
	}

	log.Printf("use nearest directory: %s", azdRoot.Directory())
	return azdRoot, nil
}
