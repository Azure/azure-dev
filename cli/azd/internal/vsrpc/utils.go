package vsrpc

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
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
