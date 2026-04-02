// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

// Importer defines the contract for project importers that can detect projects, extract services,
// and generate infrastructure from project configurations.
//
// Importers can be:
//   - Built-in (e.g., DotNetImporter for Aspire) — auto-detect projects via CanImport/Services
//   - Extension-provided — explicitly configured via infra.importer in azure.yaml
//
// When configured explicitly via azure.yaml, the infra.importer field specifies the importer name
// and path. The ImportManager looks up the importer by name and calls ProjectInfrastructure or
// GenerateAllInfrastructure directly with the configured path.
type Importer interface {
	// Name returns the display name of this importer (e.g., "Aspire", "demo-importer").
	Name() string

	// CanImport returns true if the importer can handle the given service.
	// Used for auto-detection of importable projects (e.g., Aspire AppHost detection).
	// Importers should check service properties (e.g., language) before performing
	// expensive detection operations.
	CanImport(ctx context.Context, svcConfig *ServiceConfig) (bool, error)

	// Services extracts individual service configurations from the project.
	// Used for auto-detection mode where the importer expands a single service into multiple.
	// The returned map is keyed by service name.
	Services(
		ctx context.Context,
		projectConfig *ProjectConfig,
		svcConfig *ServiceConfig,
	) (map[string]*ServiceConfig, error)

	// ProjectInfrastructure generates temporary infrastructure for provisioning.
	// Returns an Infra pointing to a temp directory with generated IaC files.
	// The importerPath is the resolved path to the importer's project files.
	ProjectInfrastructure(ctx context.Context, importerPath string) (*Infra, error)

	// GenerateAllInfrastructure generates the complete infrastructure filesystem for `azd infra gen`.
	// Returns an in-memory FS rooted at the project directory with all generated files.
	// The importerPath is the resolved path to the importer's project files.
	GenerateAllInfrastructure(ctx context.Context, importerPath string) (fs.FS, error)
}

// ImporterRegistry holds external importers registered by extensions at runtime.
// It is a singleton shared between the gRPC server (which adds importers) and
// ImportManager instances (which query them).
type ImporterRegistry struct {
	importers []Importer
	mu        sync.RWMutex
}

// NewImporterRegistry creates a new empty registry.
func NewImporterRegistry() *ImporterRegistry {
	return &ImporterRegistry{}
}

// Add registers an external importer.
func (r *ImporterRegistry) Add(importer Importer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.importers = append(r.importers, importer)
}

// All returns a snapshot of all registered external importers.
func (r *ImporterRegistry) All() []Importer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.importers)
}

// ImportManager manages the orchestration of project importers that detect services and generate infrastructure.
type ImportManager struct {
	importers        []Importer
	importerRegistry *ImporterRegistry
}

// NewImportManager creates a new ImportManager with the given built-in importers.
// The importerRegistry provides access to extension-registered importers added at runtime.
func NewImportManager(importers []Importer, importerRegistry *ImporterRegistry) *ImportManager {
	return &ImportManager{
		importers:        importers,
		importerRegistry: importerRegistry,
	}
}

// allImporters returns the combined list of built-in and extension-registered importers.
// Built-in importers come first to maintain backward compatibility.
func (im *ImportManager) allImporters() []Importer {
	if im.importerRegistry == nil {
		return im.importers
	}
	external := im.importerRegistry.All()
	if len(external) == 0 {
		return im.importers
	}
	return append(slices.Clone(im.importers), external...)
}

// findImporter looks up an importer by name from all available importers.
func (im *ImportManager) findImporter(name string) (Importer, error) {
	for _, importer := range im.allImporters() {
		if importer.Name() == name {
			return importer, nil
		}
	}
	return nil, fmt.Errorf(
		"importer '%s' is not available. Make sure the extension providing it is installed", name)
}

// servicePath returns the resolved path for a service config, handling nil Project gracefully.
func servicePath(svcConfig *ServiceConfig) string {
	if svcConfig.Project != nil {
		return svcConfig.Path()
	}
	return svcConfig.RelativePath
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

// Retrieves the list of services in the project, in a stable ordering that is deterministic.
func (im *ImportManager) ServiceStable(ctx context.Context, projectConfig *ProjectConfig) ([]*ServiceConfig, error) {
	allServices := make(map[string]*ServiceConfig)

	for name, svcConfig := range projectConfig.Services {
		imported := false

		// Only attempt import if the service config has a valid path

		for _, importer := range im.allImporters() {
			canImport, err := importer.CanImport(ctx, svcConfig)
			if err != nil {
				log.Printf(
					"error checking if %s can be imported by %s: %v",
					servicePath(svcConfig), importer.Name(), err,
				)
				continue
			}

			if canImport {
				if len(projectConfig.Services) != 1 {
					return nil, fmt.Errorf(
						"a project may only contain a single %s service and no other services at this time",
						importer.Name(),
					)
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, fmt.Errorf(
						"%s services must be configured to target the container app host at this time",
						importer.Name(),
					)
				}

				services, err := importer.Services(ctx, projectConfig, svcConfig)
				if err != nil {
					return nil, fmt.Errorf("importing services: %w", err)
				}

				maps.Copy(allServices, services)
				imported = true
				break
			}
		}

		if !imported {
			allServices[name] = svcConfig
		}
	}

	// Collect all the services and then sort the resulting list by name. This provides a stable ordering of services.
	allServicesSlice := make([]*ServiceConfig, 0, len(allServices))
	for _, v := range allServices {
		allServicesSlice = append(allServicesSlice, v)
	}

	// Sort services by dependency graph instead of alphabetical order
	return im.sortServicesByDependencies(allServicesSlice, projectConfig)
}

// ServiceStableFiltered retrieves the list of services filtered by their condition status.
// It returns:
// - all enabled services when targetServiceName is empty
// - only the targeted service if enabled, or an error if disabled
// - error if the service condition template is malformed
func (im *ImportManager) ServiceStableFiltered(
	ctx context.Context,
	projectConfig *ProjectConfig,
	targetServiceName string,
	getenv func(string) string,
) ([]*ServiceConfig, error) {
	allServices, err := im.ServiceStable(ctx, projectConfig)
	if err != nil {
		return nil, err
	}

	// If targeting a specific service, check if it exists and is enabled
	if targetServiceName != "" {
		for _, svc := range allServices {
			if svc.Name == targetServiceName {
				enabled, err := svc.IsEnabled(getenv)
				if err != nil {
					return nil, fmt.Errorf("service '%s': %w", svc.Name, err)
				}
				if !enabled {
					conditionValue, _ := svc.Condition.Envsubst(getenv)
					return nil, fmt.Errorf(
						"service '%s' has a deployment condition that evaluated to '%s'. "+
							"The service requires a truthy value (1, true, TRUE, True, yes, YES, Yes) to be enabled",
						svc.Name,
						conditionValue,
					)
				}
				return []*ServiceConfig{svc}, nil
			}
		}
		// This shouldn't happen as getTargetServiceName already validates existence
		return nil, fmt.Errorf("service '%s' not found", targetServiceName)
	}

	// Filter services by condition
	enabledServices := make([]*ServiceConfig, 0, len(allServices))
	for _, svc := range allServices {
		enabled, err := svc.IsEnabled(getenv)
		if err != nil {
			return nil, fmt.Errorf("service '%s': %w", svc.Name, err)
		}
		if enabled {
			enabledServices = append(enabledServices, svc)
		}
	}

	return enabledServices, nil
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

// HasImporter returns true when there is a service in the project that can be handled by an importer.
func (im *ImportManager) HasImporter(ctx context.Context, projectConfig *ProjectConfig) bool {
	for _, svcConfig := range projectConfig.Services {
		for _, importer := range im.allImporters() {
			canImport, err := importer.CanImport(ctx, svcConfig)
			if err != nil {
				log.Printf(
					"error checking if %s can be imported by %s: %v",
					servicePath(svcConfig), importer.Name(), err,
				)
				continue
			}
			if canImport {
				return true
			}
		}
	}
	return false
}

// HasAppHost returns true when there is one AppHost (Aspire) in the project.
// Deprecated: Use HasImporter instead.
func (im *ImportManager) HasAppHost(ctx context.Context, projectConfig *ProjectConfig) bool {
	return im.HasImporter(ctx, projectConfig)
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

	// Auto-detect provider if not explicitly set
	if infraOptions.Provider == provisioning.NotSpecified {
		detectedProvider, err := detectProviderFromFiles(infraRoot)
		if err != nil {
			return nil, err
		}
		if detectedProvider != provisioning.NotSpecified {
			log.Printf("auto-detected infrastructure provider: %s", detectedProvider)
			infraOptions.Provider = detectedProvider
		}
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

	// Infra from explicitly configured importer (infra.importer in azure.yaml)
	if !infraOptions.Importer.Empty() {
		importer, err := im.findImporter(infraOptions.Importer.Name)
		if err != nil {
			return nil, err
		}

		importerPath := infraOptions.Importer.Path
		if importerPath == "" {
			importerPath = projectConfig.Path
		} else if !filepath.IsAbs(importerPath) {
			importerPath = filepath.Join(projectConfig.Path, importerPath)
		}

		log.Printf("using importer '%s' from path '%s'", importer.Name(), importerPath)
		return importer.ProjectInfrastructure(ctx, importerPath)
	}

	// Temp infra from auto-detected importer (backward compatibility with Aspire)
	for _, svcConfig := range projectConfig.Services {
		for _, importer := range im.allImporters() {
			canImport, err := importer.CanImport(ctx, svcConfig)
			if err != nil {
				log.Printf(
					"error checking if %s can be imported by %s: %v",
					servicePath(svcConfig), importer.Name(), err,
				)
				continue
			}

			if canImport {
				if len(projectConfig.Services) != 1 {
					return nil, fmt.Errorf(
						"a project may only contain a single %s service and no other services at this time",
						importer.Name(),
					)
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, fmt.Errorf(
						"%s services must be configured to target the container app host at this time",
						importer.Name(),
					)
				}

				return importer.ProjectInfrastructure(ctx, svcConfig.Path())
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

// detectProviderFromFiles scans the infra directory and detects the IaC provider
// based on file extensions present. Returns an error if both bicep and terraform files exist.
func detectProviderFromFiles(infraPath string) (provisioning.ProviderKind, error) {
	files, err := os.ReadDir(infraPath)
	if err != nil {
		if os.IsNotExist(err) {
			return provisioning.NotSpecified, nil
		}
		return provisioning.NotSpecified, fmt.Errorf("reading infra directory: %w", err)
	}

	hasBicep := false
	hasTerraform := false

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		ext := filepath.Ext(file.Name())
		switch ext {
		case ".bicep", ".bicepparam":
			hasBicep = true
		case ".tf", ".tfvars":
			hasTerraform = true
		}

		// Early exit if both found
		if hasBicep && hasTerraform {
			break
		}
	}

	// Decision logic
	switch {
	case hasBicep && hasTerraform:
		return provisioning.NotSpecified, fmt.Errorf(
			"both Bicep and Terraform files detected in %s. "+
				"Please specify 'infra.provider' in azure.yaml as either 'bicep' or 'terraform'",
			infraPath)
	case hasBicep:
		return provisioning.Bicep, nil
	case hasTerraform:
		return provisioning.Terraform, nil
	default:
		return provisioning.NotSpecified, nil
	}
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
	// Check for explicitly configured importer (infra.importer in azure.yaml)
	if !projectConfig.Infra.Importer.Empty() {
		importerCfg := projectConfig.Infra.Importer
		importer, err := im.findImporter(importerCfg.Name)
		if err != nil {
			return nil, err
		}

		importerPath := importerCfg.Path
		if importerPath == "" {
			importerPath = projectConfig.Path
		} else if !filepath.IsAbs(importerPath) {
			importerPath = filepath.Join(projectConfig.Path, importerPath)
		}

		log.Printf("GenerateAllInfrastructure: using configured importer '%s' from path '%s'",
			importer.Name(), importerPath)
		return importer.GenerateAllInfrastructure(ctx, importerPath)
	}

	// Auto-detect importer from services (backward compatibility with Aspire)
	allImporters := im.allImporters()
	for _, svcConfig := range projectConfig.Services {
		for _, importer := range allImporters {
			canImport, err := importer.CanImport(ctx, svcConfig)
			if err != nil {
				log.Printf(
					"error checking if %s can be imported by %s: %v",
					servicePath(svcConfig), importer.Name(), err,
				)
				continue
			}

			if canImport {
				if len(projectConfig.Services) != 1 {
					return nil, fmt.Errorf(
						"a project may only contain a single %s service and no other services at this time",
						importer.Name(),
					)
				}

				if svcConfig.Host != ContainerAppTarget {
					return nil, fmt.Errorf(
						"%s services must be configured to target the container app host at this time",
						importer.Name(),
					)
				}

				return importer.GenerateAllInfrastructure(ctx, svcConfig.Path())
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
