// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"google.golang.org/protobuf/types/known/structpb"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

type projectService struct {
	azdext.UnimplementedProjectServiceServer

	lazyAzdContext    *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	importManager  *project.ImportManager
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]
	ghCli          *github.Cli
}

// NewProjectService creates a new project service instance with lazy-loaded dependencies.
// The service provides gRPC methods for managing Azure Developer CLI projects, including
// project configuration, service management, and extension configuration through AdditionalProperties.
//
// Parameters:
//   - lazyAzdContext: Lazy-loaded Azure Developer CLI context for project directory operations
//   - lazyEnvManager: Lazy-loaded environment manager for handling Azure environments
//   - lazyProjectConfig: Lazy-loaded project configuration for accessing project settings
//
// Returns an implementation of azdext.ProjectServiceServer.
func NewProjectService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
	importManager *project.ImportManager,
	ghCli *github.Cli,
) azdext.ProjectServiceServer {
	return &projectService{
		lazyAzdContext:    lazyAzdContext,
		lazyEnvManager:    lazyEnvManager,
		lazyProjectConfig: lazyProjectConfig,
		importManager:  importManager,
		ghCli:          ghCli,
	}
}

// Get retrieves the complete project configuration including all services and metadata.
// This method resolves environment variables in configuration values using the default environment
// and converts the internal project configuration to the protobuf format for gRPC communication.
//
// The returned project includes:
//   - Basic project metadata (name, resource group, path)
//   - Infrastructure configuration (provider, path, module)
//   - All configured services with their settings
//   - Template metadata if available
//
// Environment variable substitution is performed using the default environment's variables.
func (s *projectService) Get(ctx context.Context, req *azdext.EmptyRequest) (*azdext.GetProjectResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	envKeyMapper := func(env string) string {
		return ""
	}

	defaultEnvironment, err := azdContext.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	if defaultEnvironment != "" {
		env, err := envManager.Get(ctx, defaultEnvironment)
		if err == nil && env != nil {
			envKeyMapper = env.Getenv
		}
	}

	project := &azdext.ProjectConfig{
		Name:              projectConfig.Name,
		ResourceGroupName: projectConfig.ResourceGroupName.MustEnvsubst(envKeyMapper),
		Path:              projectConfig.Path,
		Infra: &azdext.InfraOptions{
			Provider: string(projectConfig.Infra.Provider),
			Path:     projectConfig.Infra.Path,
			Module:   projectConfig.Infra.Module,
		},
		Services: map[string]*azdext.ServiceConfig{},
	}

	if projectConfig.Metadata != nil {
		project.Metadata = &azdext.ProjectMetadata{
			Template: projectConfig.Metadata.Template,
		}
	}

	for name, service := range projectConfig.Services {
		var protoService *azdext.ServiceConfig

		// Use mapper with environment variable resolver
		if err := mapper.WithResolver(envKeyMapper).Convert(service, &protoService); err != nil {
			return nil, fmt.Errorf("converting service config to proto: %w", err)
		}

		project.Services[name] = protoService
	}

	return &azdext.GetProjectResponse{
		Project: project,
	}, nil
}

// AddService adds a new service to the project configuration and persists the changes.
// The service configuration is converted from the protobuf format to the internal representation
// and added to the project's services map. The updated project configuration is then saved to disk.
//
// Parameters:
//   - req.Service: The service configuration to add, including name, host, language, and other settings
//
// The service name from req.Service.Name is used as the key in the services map.
// If the services map doesn't exist, it will be initialized.
func (s *projectService) AddService(ctx context.Context, req *azdext.AddServiceRequest) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	serviceConfig := &project.ServiceConfig{}
	if err := mapper.Convert(req.Service, &serviceConfig); err != nil {
		return nil, fmt.Errorf("failed converting service configuration, %w", err)
	}

	if projectConfig.Services == nil {
		projectConfig.Services = map[string]*project.ServiceConfig{}
	}

	projectConfig.Services[req.Service.Name] = serviceConfig
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// GetConfigSection retrieves a configuration section from the project's AdditionalProperties.
// This method provides access to extension-specific configuration data stored in the project
// configuration using dot-notation paths (e.g., "extension.database.connection").
//
// Parameters:
//   - req.Path: Dot-notation path to the configuration section (e.g., "custom.settings")
//
// Returns:
//   - Section: The configuration section as a protobuf Struct if found
//   - Found: Boolean indicating whether the section exists
//
// If AdditionalProperties is nil, it's treated as an empty configuration.
func (s *projectService) GetConfigSection(
	ctx context.Context, req *azdext.GetProjectConfigSectionRequest,
) (*azdext.GetProjectConfigSectionResponse, error) {
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := projectConfig.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)
	section, found := cfg.GetMap(req.Path)

	if !found {
		return &azdext.GetProjectConfigSectionResponse{
			Found: false,
		}, nil
	}

	// Convert section to protobuf Struct
	protoStruct, err := structpb.NewStruct(section)
	if err != nil {
		return nil, fmt.Errorf("failed to convert section to protobuf struct: %w", err)
	}

	return &azdext.GetProjectConfigSectionResponse{
		Section: protoStruct,
		Found:   true,
	}, nil
}

// GetConfigValue retrieves a specific configuration value from the project's AdditionalProperties.
// This method provides access to individual configuration values stored in the project
// configuration using dot-notation paths (e.g., "extension.database.port").
//
// Parameters:
//   - req.Path: Dot-notation path to the configuration value (e.g., "custom.settings.timeout")
//
// Returns:
//   - Value: The configuration value as a protobuf Value if found
//   - Found: Boolean indicating whether the value exists
//
// Supports all JSON types: strings, numbers, booleans, objects, and arrays.
// If AdditionalProperties is nil, it's treated as an empty configuration.
func (s *projectService) GetConfigValue(
	ctx context.Context,
	req *azdext.GetProjectConfigValueRequest,
) (*azdext.GetProjectConfigValueResponse, error) {
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := projectConfig.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)
	value, ok := cfg.Get(req.Path)

	if !ok {
		return &azdext.GetProjectConfigValueResponse{
			Found: false,
		}, nil
	}

	// Convert value to protobuf Value
	protoValue, err := structpb.NewValue(value)
	if err != nil {
		return nil, fmt.Errorf("failed to convert value to protobuf value: %w", err)
	}

	return &azdext.GetProjectConfigValueResponse{
		Value: protoValue,
		Found: true,
	}, nil
}

// SetConfigSection sets or updates a configuration section in the project's AdditionalProperties.
// This method allows extensions to store complex configuration data as nested objects
// using dot-notation paths. The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.Path: Dot-notation path where to store the section (e.g., "custom.database")
//   - req.Section: The configuration section as a protobuf Struct containing the data
//
// If the path doesn't exist, it will be created. Existing data at the path will be replaced.
// If AdditionalProperties is nil, it will be initialized as an empty map.
func (s *projectService) SetConfigSection(
	ctx context.Context,
	req *azdext.SetProjectConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := projectConfig.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)

	// Convert protobuf Struct to map
	sectionMap := req.Section.AsMap()
	if err := cfg.Set(req.Path, sectionMap); err != nil {
		return nil, fmt.Errorf("failed to set config section: %w", err)
	}

	// Update project AdditionalProperties and save
	projectConfig.AdditionalProperties = cfg.Raw()
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// SetConfigValue sets or updates a specific configuration value in the project's AdditionalProperties.
// This method allows extensions to store individual configuration values using dot-notation paths.
// The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.Path: Dot-notation path where to store the value (e.g., "custom.settings.timeout")
//   - req.Value: The configuration value as a protobuf Value (string, number, boolean, etc.)
//
// If the path doesn't exist, intermediate objects will be created automatically.
// Existing data at the path will be replaced.
// If AdditionalProperties is nil, it will be initialized as an empty map.
func (s *projectService) SetConfigValue(
	ctx context.Context,
	req *azdext.SetProjectConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := projectConfig.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)

	// Convert protobuf Value to interface{}
	value := req.Value.AsInterface()
	if err := cfg.Set(req.Path, value); err != nil {
		return nil, fmt.Errorf("failed to set config value: %w", err)
	}

	// Update project AdditionalProperties and save
	projectConfig.AdditionalProperties = cfg.Raw()
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// UnsetConfig removes a configuration value or section from the project's AdditionalProperties.
// This method allows extensions to clean up configuration data using dot-notation paths.
// The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.Path: Dot-notation path to the configuration to remove (e.g., "custom.settings.timeout")
//
// If the path points to a value, only that value is removed.
// If the path points to a section, the entire section and all its contents are removed.
// If the path doesn't exist, the operation succeeds without error.
func (s *projectService) UnsetConfig(
	ctx context.Context,
	req *azdext.UnsetProjectConfigRequest,
) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := projectConfig.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)
	if err := cfg.Unset(req.Path); err != nil {
		return nil, fmt.Errorf("failed to unset config: %w", err)
	}

	// Update project AdditionalProperties and save
	projectConfig.AdditionalProperties = cfg.Raw()
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// GetServiceConfigSection retrieves a configuration section from a specific service's AdditionalProperties.
// This method provides access to service-specific extension configuration data using dot-notation paths.
//
// Parameters:
//   - req.ServiceName: Name of the service to retrieve configuration from
//   - req.Path: Dot-notation path to the configuration section (e.g., "custom.database")
//
// Returns:
//   - Section: The configuration section as a protobuf Struct if found
//   - Found: Boolean indicating whether the section exists
//
// Returns an error if the specified service doesn't exist in the project.
// If the service's AdditionalProperties is nil, it's treated as an empty configuration.
func (s *projectService) GetServiceConfigSection(
	ctx context.Context,
	req *azdext.GetServiceConfigSectionRequest,
) (*azdext.GetServiceConfigSectionResponse, error) {
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Check if service exists
	service, exists := projectConfig.Services[req.ServiceName]
	if !exists {
		return nil, fmt.Errorf("service '%s' not found", req.ServiceName)
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := service.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)
	section, found := cfg.GetMap(req.Path)

	if !found {
		return &azdext.GetServiceConfigSectionResponse{
			Found: false,
		}, nil
	}

	// Convert section to protobuf Struct
	protoStruct, err := structpb.NewStruct(section)
	if err != nil {
		return nil, fmt.Errorf("failed to convert section to protobuf struct: %w", err)
	}

	return &azdext.GetServiceConfigSectionResponse{
		Section: protoStruct,
		Found:   true,
	}, nil
}

// GetServiceConfigValue retrieves a specific configuration value from a service's AdditionalProperties.
// This method provides access to individual service-specific configuration values using dot-notation paths.
//
// Parameters:
//   - req.ServiceName: Name of the service to retrieve configuration from
//   - req.Path: Dot-notation path to the configuration value (e.g., "custom.database.port")
//
// Returns:
//   - Value: The configuration value as a protobuf Value if found
//   - Found: Boolean indicating whether the value exists
//
// Supports all JSON types: strings, numbers, booleans, objects, and arrays.
// Returns an error if the specified service doesn't exist in the project.
// If the service's AdditionalProperties is nil, it's treated as an empty configuration.
func (s *projectService) GetServiceConfigValue(
	ctx context.Context,
	req *azdext.GetServiceConfigValueRequest,
) (*azdext.GetServiceConfigValueResponse, error) {
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Check if service exists
	service, exists := projectConfig.Services[req.ServiceName]
	if !exists {
		return nil, fmt.Errorf("service '%s' not found", req.ServiceName)
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := service.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)
	value, ok := cfg.Get(req.Path)

	if !ok {
		return &azdext.GetServiceConfigValueResponse{
			Found: false,
		}, nil
	}

	// Convert value to protobuf Value
	protoValue, err := structpb.NewValue(value)
	if err != nil {
		return nil, fmt.Errorf("failed to convert value to protobuf value: %w", err)
	}

	return &azdext.GetServiceConfigValueResponse{
		Value: protoValue,
		Found: true,
	}, nil
}

// SetServiceConfigSection sets or updates a configuration section in a service's AdditionalProperties.
// This method allows extensions to store complex service-specific configuration data as nested objects.
// The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.ServiceName: Name of the service to update configuration for
//   - req.Path: Dot-notation path where to store the section (e.g., "custom.database")
//   - req.Section: The configuration section as a protobuf Struct containing the data
//
// Returns an error if the specified service doesn't exist in the project.
// If the path doesn't exist, it will be created. Existing data at the path will be replaced.
// If the service's AdditionalProperties is nil, it will be initialized as an empty map.
func (s *projectService) SetServiceConfigSection(
	ctx context.Context,
	req *azdext.SetServiceConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Check if service exists
	service, exists := projectConfig.Services[req.ServiceName]
	if !exists {
		return nil, fmt.Errorf("service '%s' not found", req.ServiceName)
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := service.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)

	// Convert protobuf Struct to map
	sectionMap := req.Section.AsMap()
	if err := cfg.Set(req.Path, sectionMap); err != nil {
		return nil, fmt.Errorf("failed to set service config section: %w", err)
	}

	// Update service AdditionalProperties and save
	service.AdditionalProperties = cfg.Raw()
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// SetServiceConfigValue sets or updates a specific configuration value in a service's AdditionalProperties.
// This method allows extensions to store individual service-specific configuration values.
// The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.ServiceName: Name of the service to update configuration for
//   - req.Path: Dot-notation path where to store the value (e.g., "custom.database.port")
//   - req.Value: The configuration value as a protobuf Value (string, number, boolean, etc.)
//
// Returns an error if the specified service doesn't exist in the project.
// If the path doesn't exist, intermediate objects will be created automatically.
// Existing data at the path will be replaced.
// If the service's AdditionalProperties is nil, it will be initialized as an empty map.
func (s *projectService) SetServiceConfigValue(
	ctx context.Context,
	req *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Check if service exists
	service, exists := projectConfig.Services[req.ServiceName]
	if !exists {
		return nil, fmt.Errorf("service '%s' not found", req.ServiceName)
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := service.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)

	// Convert protobuf Value to interface{}
	value := req.Value.AsInterface()
	if err := cfg.Set(req.Path, value); err != nil {
		return nil, fmt.Errorf("failed to set service config value: %w", err)
	}

	// Update service AdditionalProperties and save
	service.AdditionalProperties = cfg.Raw()
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// UnsetServiceConfig removes a configuration value or section from a service's AdditionalProperties.
// This method allows extensions to clean up service-specific configuration data.
// The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.ServiceName: Name of the service to remove configuration from
//   - req.Path: Dot-notation path to the configuration to remove (e.g., "custom.database.port")
//
// Returns an error if the specified service doesn't exist in the project.
// If the path points to a value, only that value is removed.
// If the path points to a section, the entire section and all its contents are removed.
// If the path doesn't exist, the operation succeeds without error.
func (s *projectService) UnsetServiceConfig(
	ctx context.Context,
	req *azdext.UnsetServiceConfigRequest,
) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	// Check if service exists
	service, exists := projectConfig.Services[req.ServiceName]
	if !exists {
		return nil, fmt.Errorf("service '%s' not found", req.ServiceName)
	}

	// Initialize empty map if AdditionalProperties is nil
	additionalProps := service.AdditionalProperties
	if additionalProps == nil {
		additionalProps = make(map[string]any)
	}

	cfg := config.NewConfig(additionalProps)
	if err := cfg.Unset(req.Path); err != nil {
		return nil, fmt.Errorf("failed to unset service config: %w", err)
	}

	// Update service AdditionalProperties and save
	service.AdditionalProperties = cfg.Raw()
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// GetResolvedServices returns the resolved list of services after processing any importers (e.g., Aspire projects).
// This includes services generated by importers like Aspire AppHost projects.
func (s *projectService) GetResolvedServices(
	ctx context.Context,
	req *azdext.EmptyRequest,
) (*azdext.GetResolvedServicesResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	envKeyMapper := func(env string) string {
		return ""
	}

	defaultEnvironment, err := azdContext.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	if defaultEnvironment != "" {
		env, err := envManager.Get(ctx, defaultEnvironment)
		if err == nil && env != nil {
			envKeyMapper = env.Getenv
		}
	}

	// Get resolved services using ImportManager
	servicesStable, err := s.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return nil, fmt.Errorf("resolving services: %w", err)
	}

	// Convert to proto format
	protoServices := make(map[string]*azdext.ServiceConfig)
	for _, service := range servicesStable {
		var protoService *azdext.ServiceConfig

		// Use mapper with environment variable resolver
		if err := mapper.WithResolver(envKeyMapper).Convert(service, &protoService); err != nil {
			return nil, fmt.Errorf("converting service config to proto: %w", err)
		}

		protoServices[service.Name] = protoService
	}

	return &azdext.GetResolvedServicesResponse{
		Services: protoServices,
	}, nil
}

func (
	s *projectService,
) ParseGitHubUrl(
	ctx context.Context,
	req *azdext.ParseGitHubUrlRequest,
) (*azdext.ParseGitHubUrlResponse, error) {
	urlInfo, err := templates.ParseGitHubUrl(ctx, req.Url, s.ghCli)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub URL: %w", err)
	}

	return &azdext.ParseGitHubUrlResponse{
		Hostname: urlInfo.Hostname,
		RepoSlug: urlInfo.RepoSlug,
		Branch:   urlInfo.Branch,
		FilePath: urlInfo.FilePath,
	}, nil
}
