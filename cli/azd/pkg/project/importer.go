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

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
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

	slices.SortFunc(allServicesSlice, func(x, y *ServiceConfig) int {
		return strings.Compare(x.Name, y.Name)
	})

	return allServicesSlice, nil
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

// defaultOptions for infra settings. These values are applied across provisioning providers.
const (
	DefaultModule = "main"
	DefaultPath   = "infra"
)

var featureCompose = alpha.MustFeatureKey("compose")

// ProjectInfrastructure parses the project configuration and returns the infrastructure configuration.
// The configuration can be explicitly defined on azure.yaml using path and module, or in case these values
// are not explicitly defined, the project importer uses default values to find the infrastructure.
func (im *ImportManager) ProjectInfrastructure(ctx context.Context, projectConfig *ProjectConfig) (*Infra, error) {
	// Use default project values for Infra when not specified in azure.yaml
	if projectConfig.Infra.Module == "" {
		projectConfig.Infra.Module = DefaultModule
	}
	if projectConfig.Infra.Path == "" {
		projectConfig.Infra.Path = DefaultPath
	}

	infraRoot := projectConfig.Infra.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(projectConfig.Path, infraRoot)
	}

	// Allow overriding the infrastructure only when path and module exists.
	if moduleExists, err := pathHasModule(infraRoot, projectConfig.Infra.Module); err == nil && moduleExists {
		log.Printf("using infrastructure from %s directory", infraRoot)
		return &Infra{
			Options: projectConfig.Infra,
		}, nil
	}

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

	composeEnabled := im.dotNetImporter.alphaFeatureManager.IsEnabled(featureCompose)
	if composeEnabled && len(projectConfig.Resources) > 0 {
		return tempInfra(ctx, projectConfig)
	}

	if !composeEnabled && len(projectConfig.Resources) > 0 {
		return nil, fmt.Errorf(
			"compose is currently under alpha support and must be explicitly enabled."+
				" Run `%s` to enable this feature", alpha.GetEnableCommand(featureCompose))
	}

	return &Infra{}, nil
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

// SynthAllInfrastructure returns a file system containing all infrastructure for the project,
// rooted at the project directory.
func (im *ImportManager) SynthAllInfrastructure(ctx context.Context, projectConfig *ProjectConfig) (fs.FS, error) {
	for _, svcConfig := range projectConfig.Services {
		if svcConfig.Language == ServiceLanguageDotNet {
			if len(projectConfig.Services) != 1 {
				return nil, errNoMultipleServicesWithAppHost
			}

			return im.dotNetImporter.SynthAllInfrastructure(ctx, projectConfig, svcConfig)
		}
	}

	composeEnabled := im.dotNetImporter.alphaFeatureManager.IsEnabled(featureCompose)
	if composeEnabled && len(projectConfig.Resources) > 0 {
		return infraFsForProject(ctx, projectConfig)
	}

	if !composeEnabled && len(projectConfig.Resources) > 0 {
		return nil, fmt.Errorf(
			"compose is currently under alpha support and must be explicitly enabled."+
				" Run `%s` to enable this feature", alpha.GetEnableCommand(featureCompose))
	}

	return nil, fmt.Errorf("this project does not contain any infrastructure to synthesize")
}

// Infra represents the (possibly temporarily generated) infrastructure. Call [Cleanup] when done with infrastructure,
// which will cause any temporarily generated files to be removed.
type Infra struct {
	Options    provisioning.Options
	cleanupDir string
}

func (i *Infra) Cleanup() error {
	if i.cleanupDir != "" {
		return os.RemoveAll(i.cleanupDir)
	}

	return nil
}
