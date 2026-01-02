// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

type ImportManager struct {
	dotNetImporter *DotNetImporter
}

func NewImportManager(dotNetImporter *DotNetImporter) *ImportManager {
	return &ImportManager{
		dotNetImporter: dotNetImporter,
	}
}

func (im *ImportManager) HasService(ctx context.Context, projectConfig *ProjectConfig, name string) (bool, error) {
	services, err := im.ServiceStable(ctx, projectConfig)
	if err != nil {
		return false, err
	}

	for _, svc := range services {
		if svc.Name == name {
			return true, nil
		}
	}

	return false, nil
}

var (
	errNoMultipleServicesWithAppHost = fmt.Errorf(
		"a project may only contain a single Aspire service and no other services at this time.")

	errAppHostMustTargetContainerApp = fmt.Errorf(
		"Aspire services must be configured to target the container app host at this time.")
)

// Retrieves the list of services in the project, in a stable ordering that is deterministic.
func (im *ImportManager) ServiceStable(ctx context.Context, projectConfig *ProjectConfig) ([]*ServiceConfig, error) {
	allServices := make(map[string]*ServiceConfig)

	for name, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if canImport, err := im.dotNetImporter.CanImport(ctx, svcConfig.Path()); canImport {
				if len(projectConfig.Services) != 1 {
					return nil, errNoMultipleServicesWithAppHost
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, errAppHostMustTargetContainerApp
				}

				services, err := im.dotNetImporter.Services(ctx, projectConfig, svcConfig)
				if err != nil {
					return nil, fmt.Errorf("importing services: %w", err)
				}

				for name, svcConfig := range services {
					// TODO(ellismg): We should consider if we should prefix these services so the are of the form
					// "app:frontend" instead of just "frontend". Perhaps both as the key here and and as the .Name
					// property on the ServiceConfig.  This does have implications for things like service specific
					// property names that translate to environment variables.
					allServices[name] = svcConfig
				}

				continue
			} else if err != nil {
				log.Printf("error checking if %s is an app host project: %v", svcConfig.Path(), err)
			}
		}

		allServices[name] = svcConfig
	}

	// Collect all the services and then sort the resulting list by name. This provides a stable ordering of services.
	allServicesSlice := make([]*ServiceConfig, 0, len(allServices))
	for _, v := range allServices {
		allServicesSlice = append(allServicesSlice, v)
	}

	// Sort services by dependency graph instead of alphabetical order
	return im.sortServicesByDependencies(allServicesSlice, projectConfig)
}

// sortServicesByDependencies performs a topological sort of services based on their dependencies.
// Returns services in dependency order (dependencies first) with circular reference detection.
// If no dependencies are defined, falls back to alphabetical ordering for backward compatibility.
func (im *ImportManager) sortServicesByDependencies(
	services []*ServiceConfig,
	projectConfig *ProjectConfig,
) ([]*ServiceConfig, error) {
	// Validate dependencies exist
	if err := im.validateServiceDependencies(services, projectConfig); err != nil {
		return nil, err
	}

	// Check if any service has dependencies
	hasDependencies := false
	for _, svc := range services {
		if len(svc.Uses) > 0 {
			hasDependencies = true
			break
		}
	}

	// If no dependencies are defined, maintain the original alphabetical ordering
	if !hasDependencies {
		slices.SortFunc(services, func(x, y *ServiceConfig) int {
			return strings.Compare(x.Name, y.Name)
		})
		return services, nil
	}

	// Create dependency graph
	graph := make(map[string][]string)
	inDegree := make(map[string]int)

	// Initialize graph and in-degree count
	for _, svc := range services {
		graph[svc.Name] = []string{}
		inDegree[svc.Name] = 0
	}

	// Build dependency edges
	for _, svc := range services {
		for _, dependency := range svc.Uses {
			// Only consider service-to-service dependencies for ordering
			// (service-to-resource dependencies are declarative only)
			if _, isService := graph[dependency]; isService {
				graph[dependency] = append(graph[dependency], svc.Name)
				inDegree[svc.Name]++
			}
		}
	}

	// Topological sort using Kahn's algorithm
	var result []*ServiceConfig
	var queue []string

	// Find all services with no dependencies
	for serviceName, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, serviceName)
		}
	}

	// Process services in dependency order
	serviceMap := make(map[string]*ServiceConfig)
	for _, svc := range services {
		serviceMap[svc.Name] = svc
	}

	for len(queue) > 0 {
		// Remove a service with no dependencies
		current := queue[0]
		queue = queue[1:]
		result = append(result, serviceMap[current])

		// Update dependencies of services that depend on current service
		for _, dependent := range graph[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	// Check for circular dependencies
	if len(result) != len(services) {
		return nil, fmt.Errorf("circular dependency detected in services")
	}

	return result, nil
}

// validateServiceDependencies ensures all dependencies referenced in service Uses exist
func (im *ImportManager) validateServiceDependencies(services []*ServiceConfig, projectConfig *ProjectConfig) error {
	serviceNames := make(map[string]bool)
	for _, svc := range services {
		serviceNames[svc.Name] = true
	}

	for _, svc := range services {
		for _, dependency := range svc.Uses {
			// Check if dependency exists as a service or resource
			if !serviceNames[dependency] && (projectConfig.Resources == nil || projectConfig.Resources[dependency] == nil) {
				return fmt.Errorf(
					"service '%s' depends on '%s' which does not exist as a service or resource",
					svc.Name,
					dependency,
				)
			}
		}
	}

	return nil
}

// HasAppHost returns true when there is one AppHost (Aspire) in the project.
func (im *ImportManager) HasAppHost(ctx context.Context, projectConfig *ProjectConfig) bool {
	for _, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if canImport, err := im.dotNetImporter.CanImport(ctx, svcConfig.Path()); canImport {
				return true
			} else if err != nil {
				log.Printf("error checking if %s is an app host project: %v", svcConfig.Path(), err)
			}
		}
	}
	return false
}

var (
	DefaultProvisioningOptions = provisioning.Options{
		Module: "main",
		Path:   "infra",
	}
)

// ProjectInfrastructure parses the project configuration and returns the infrastructure configuration.
//
// The configuration can be explicitly defined on azure.yaml using path and module, or in case these values
// are not explicitly defined, the project importer uses default values to find the infrastructure.
func (im *ImportManager) ProjectInfrastructure(ctx context.Context, projectConfig *ProjectConfig) (*Infra, error) {
	infraOptions, err := projectConfig.Infra.GetWithDefaults()
	if err != nil {
		return nil, err
	}

	infraRoot := infraOptions.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(projectConfig.Path, infraRoot)
	}

	// short-circuit: If layers are defined, we know it's an explicit infrastructure
	if len(infraOptions.Layers) > 0 {
		return &Infra{
			Options: infraOptions,
		}, nil
	}

	// short-circuit: If infra files exist, we know it's an explicit infrastructure
	if moduleExists, err := pathHasModule(infraRoot, infraOptions.Module); err == nil && moduleExists {
		log.Printf("using infrastructure from %s directory", infraRoot)
		return &Infra{
			Options: infraOptions,
		}, nil
	}

	// Temp infra from AppHost
	for _, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if canImport, err := im.dotNetImporter.CanImport(ctx, svcConfig.Path()); canImport {
				if len(projectConfig.Services) != 1 {
					return nil, errNoMultipleServicesWithAppHost
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, errAppHostMustTargetContainerApp
				}

				return im.dotNetImporter.ProjectInfrastructure(ctx, svcConfig)
			} else if err != nil {
				log.Printf("error checking if %s is an app host project: %v", svcConfig.Path(), err)
			}
		}
	}

	// Temp infra from resources
	if len(projectConfig.Resources) > 0 {
		return tempInfra(ctx, projectConfig)
	}

	// Return default project infra
	return &Infra{
		Options: infraOptions,
	}, nil
}

// pathHasModule returns true if there is a file named "<module>" or "<module.bicep>" in path.
func pathHasModule(path, module string) (bool, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("error while iterating directory: %w", err)
	}

	return slices.ContainsFunc(files, func(file fs.DirEntry) bool {
		fileName := file.Name()
		fileNameNoExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		return !file.IsDir() && fileNameNoExt == module
	}), nil

}

// GenerateAllInfrastructure returns a file system containing all infrastructure for the project,
// rooted at the project directory.
func (im *ImportManager) GenerateAllInfrastructure(ctx context.Context, projectConfig *ProjectConfig) (fs.FS, error) {
	for _, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if canImport, err := im.dotNetImporter.CanImport(ctx, svcConfig.Path()); canImport {
				if len(projectConfig.Services) != 1 {
					return nil, errNoMultipleServicesWithAppHost
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, errAppHostMustTargetContainerApp
				}

				return im.dotNetImporter.GenerateAllInfrastructure(ctx, projectConfig, svcConfig)
			} else if err != nil {
				log.Printf("error checking if %s is an app host project: %v", svcConfig.Path(), err)
			}

		}
	}

	if len(projectConfig.Resources) > 0 {
		return infraFsForProject(ctx, projectConfig)
	}

	return nil, fmt.Errorf("this project does not contain any infrastructure to generate")
}

// Infra represents the (possibly temporarily generated) infrastructure. Call [Cleanup] when done with infrastructure,
// which will cause any temporarily generated files to be removed.
type Infra struct {
	Options    provisioning.Options
	cleanupDir string
	IsCompose  bool
}

func (i *Infra) Cleanup() error {
	if i.cleanupDir != "" {
		return os.RemoveAll(i.cleanupDir)
	}

	return nil
}
