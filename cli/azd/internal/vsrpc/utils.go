package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// appHostServiceForProject returns the ServiceConfig of the service for the AppHost project for the given azd project.
func appHostForProject(
	ctx context.Context, pc *project.ProjectConfig, dotnetCli dotnet.DotNetCli,
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

// azdContext resolves the azd context directory to use.
//
//   - If the host project directory contains azure.yaml, the host project directory is used.
//   - If the nearest project directory contains azure.yaml, and the azure.yaml has services matching the given host project,
//     the nearest project directory is used.
//   - Otherwise, the root directory is used by default.
//
// The desired effect is that in Visual Studio, we prefer using the solution directory as the context directory by default.
// When the solution directory has an existing azure.yaml referencing a different app host project, we then
// prefer using the app host project directory. This allows publishing multiple app host projects within a solution without
// additional configuration.
func azdContext(hostProjectPath string, root string) (*azdcontext.AzdContext, error) {
	hostProjectDir := filepath.Dir(hostProjectPath)
	azdCtx, err := azdcontext.NewAzdContextFromWd(hostProjectDir)
	if errors.Is(err, azdcontext.ErrNoProject) {
		// no project exists, use root directory as the default
		azdCtx = azdcontext.NewAzdContextWithDirectory(root)

		if _, err := os.Stat(azdCtx.ProjectPath()); errors.Is(err, os.ErrNotExist) {
			return azdCtx, nil
		} else if err != nil {
			return nil, err
		}
		// If we got here, it means the "solution root" is a sibling rather than the root of the project directory
		// If so, we want to continue validating whether the azure.yaml targets the current app host project
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
				log.Printf("use app host directory: %s", hostProjectDir)
				return azdcontext.NewAzdContextWithDirectory(hostProjectDir), nil
			}
		}

		// there can only be one app host project
		break
	}

	log.Printf("use nearest directory: %s", azdCtx.ProjectDirectory())
	return azdCtx, nil
}
