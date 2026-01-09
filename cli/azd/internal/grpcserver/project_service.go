// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type projectService struct {
	azdext.UnimplementedProjectServiceServer

	lazyAzdContext    *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	importManager     *project.ImportManager
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]
	ghCli             *github.Cli
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
		importManager:     importManager,
		ghCli:             ghCli,
	}
}

// reloadAndCacheProjectConfig reloads the project configuration from disk and updates the lazy cache.
// It preserves the EventDispatcher from the previous instance to maintain event handler continuity
// for both the project and all services.
//
// Event dispatchers must be preserved because they contain event handlers that were registered by:
//   - azure.yaml hooks (prepackage, postdeploy, etc.)
//   - azd extensions that registered custom event handlers
//
// Without preserving these dispatchers, any event handlers registered before the reload would be lost,
// causing hooks and extension-registered handlers to stop working after configuration updates.
func (s *projectService) reloadAndCacheProjectConfig(ctx context.Context, projectPath string) error {
	// Get the current config to preserve the EventDispatchers
	oldConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		// If we can't get old config, just reload without preserving dispatchers
		reloadedConfig, err := project.Load(ctx, projectPath)
		if err != nil {
			return err
		}
		s.lazyProjectConfig.SetValue(reloadedConfig)
		return nil
	}

	// Reload the config from disk
	reloadedConfig, err := project.Load(ctx, projectPath)
	if err != nil {
		return err
	}

	// Preserve the EventDispatcher from the old project config
	if oldConfig.EventDispatcher != nil {
		reloadedConfig.EventDispatcher = oldConfig.EventDispatcher
	}

	// Preserve the EventDispatcher for each service
	if oldConfig.Services != nil && reloadedConfig.Services != nil {
		for serviceName, oldService := range oldConfig.Services {
			if reloadedService, exists := reloadedConfig.Services[serviceName]; exists && oldService.EventDispatcher != nil {
				reloadedService.EventDispatcher = oldService.EventDispatcher
			}
		}
	}

	// Update the lazy cache
	s.lazyProjectConfig.SetValue(reloadedConfig)
	return nil
}

// validateServiceExists checks if a service exists in the project configuration.
// Returns an error if the service doesn't exist.
func (s *projectService) validateServiceExists(ctx context.Context, serviceName string) error {
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return err
	}

	if projectConfig.Services == nil || projectConfig.Services[serviceName] == nil {
		return fmt.Errorf("service '%s' not found", serviceName)
	}

	return nil
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

	var project *azdext.ProjectConfig
	if err := mapper.WithResolver(envKeyMapper).Convert(projectConfig, &project); err != nil {
		return nil, fmt.Errorf("converting project config to proto: %w", err)
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
	if req.Service == nil || req.Service.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}

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

	// Check if service already exists to preserve EventDispatcher
	if existingService, exists := projectConfig.Services[req.Service.Name]; exists &&
		existingService.EventDispatcher != nil {
		serviceConfig.EventDispatcher = existingService.EventDispatcher
	} else {
		// Initialize EventDispatcher for new service
		serviceConfig.EventDispatcher = ext.NewEventDispatcher[project.ServiceLifecycleEventArgs]()
	}

	// Set the Project reference and Name (required fields not set by mapper)
	serviceConfig.Project = projectConfig
	serviceConfig.Name = req.Service.Name

	projectConfig.Services[req.Service.Name] = serviceConfig
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// GetConfigSection retrieves a configuration section from the project configuration.
// This method provides access to both core struct fields (e.g., "infra", "services")
// and extension-specific configuration data stored in AdditionalProperties using
// dot-notation paths (e.g., "extension.database.connection", "infra").
//
// Parameters:
//   - req.Path: Dot-notation path to the configuration section (e.g., "custom.settings", "infra")
//
// Returns:
//   - Section: The configuration section as a protobuf Struct if found
//   - Found: Boolean indicating whether the section exists
//
// Examples:
//   - "infra" - retrieves the infrastructure configuration section
//   - "custom.database" - retrieves custom extension configuration
func (s *projectService) GetConfigSection(
	ctx context.Context, req *azdext.GetProjectConfigSectionRequest,
) (*azdext.GetProjectConfigSectionResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Load entire project config as a map (includes core fields and AdditionalProperties)
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

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

// GetConfigValue retrieves a specific configuration value from the project configuration.
// This method provides access to both core struct fields (e.g., "name", "infra.provider")
// and extension-specific configuration values stored in AdditionalProperties using
// dot-notation paths (e.g., "extension.database.port", "infra.path").
//
// Parameters:
//   - req.Path: Dot-notation path to the configuration value (e.g., "custom.settings.timeout", "infra.provider")
//
// Returns:
//   - Value: The configuration value as a protobuf Value if found
//   - Found: Boolean indicating whether the value exists
//
// Supports all JSON types: strings, numbers, booleans, objects, and arrays.
// Examples:
//   - "name" - retrieves the project name
//   - "infra.provider" - retrieves the infrastructure provider
//   - "custom.timeout" - retrieves custom extension configuration
func (s *projectService) GetConfigValue(
	ctx context.Context,
	req *azdext.GetProjectConfigValueRequest,
) (*azdext.GetProjectConfigValueResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Load entire project config as a map (includes core fields and AdditionalProperties)
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

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

// SetConfigSection sets or updates a configuration section in the project configuration.
// This method allows extensions to store complex configuration data as nested objects
// (both core fields and AdditionalProperties) using dot-notation paths.
// The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.Path: Dot-notation path where to store the section (e.g., "custom.database", "infra.module")
//   - req.Section: The configuration section as a protobuf Struct containing the data
//
// If the path doesn't exist, it will be created. Existing data at the path will be replaced.
func (s *projectService) SetConfigSection(
	ctx context.Context,
	req *azdext.SetProjectConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Load entire project config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Convert protobuf Struct to map
	sectionMap := req.Section.AsMap()
	if err := cfg.Set(req.Path, sectionMap); err != nil {
		return nil, fmt.Errorf("failed to set config section: %w", err)
	}

	// Save the updated configuration (validates structure)
	if err := project.SaveConfig(ctx, cfg, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	// Reload and update the lazy cache, preserving the EventDispatcher
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// SetConfigValue sets or updates a specific configuration value in the project configuration.
// This method allows extensions to store individual configuration values (both core fields and
// AdditionalProperties) using dot-notation paths. The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.Path: Dot-notation path where to store the value (e.g., "custom.settings.timeout", "name", "infra.provider")
//   - req.Value: The configuration value as a protobuf Value (string, number, boolean, etc.)
//
// If the path doesn't exist, intermediate objects will be created automatically.
// Existing data at the path will be replaced.
func (s *projectService) SetConfigValue(
	ctx context.Context,
	req *azdext.SetProjectConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Load entire project config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Convert protobuf Value to interface{}
	value := req.Value.AsInterface()
	if err := cfg.Set(req.Path, value); err != nil {
		return nil, fmt.Errorf("failed to set config value: %w", err)
	}

	// Save the updated configuration (validates structure)
	if err := project.SaveConfig(ctx, cfg, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	// Reload and update the lazy cache, preserving the EventDispatcher
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// UnsetConfig removes a configuration value or section from the project configuration.
// This method allows extensions to clean up configuration data (both core fields and AdditionalProperties)
// using dot-notation paths. The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.Path: Dot-notation path to the configuration to remove (e.g., "custom.settings.timeout", "infra.module")
//
// If the path points to a value, only that value is removed.
// If the path points to a section, the entire section and all its contents are removed.
// If the path doesn't exist, the operation succeeds without error.
func (s *projectService) UnsetConfig(
	ctx context.Context,
	req *azdext.UnsetProjectConfigRequest,
) (*azdext.EmptyResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Load entire project config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	if err := cfg.Unset(req.Path); err != nil {
		return nil, fmt.Errorf("failed to unset config: %w", err)
	}

	// Save the updated configuration (validates structure)
	if err := project.SaveConfig(ctx, cfg, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	// Reload and update the lazy cache, preserving the EventDispatcher
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// GetServiceConfigSection retrieves a configuration section from a specific service's configuration.
// This method provides access to service-specific configuration data (both core fields like "host", "project"
// and AdditionalProperties) using dot-notation paths.
//
// Parameters:
//   - req.ServiceName: Name of the service to retrieve configuration from
//   - req.Path: Dot-notation path to the configuration section (e.g., "custom.database", or empty for entire service config)
//
// Returns:
//   - Section: The configuration section as a protobuf Struct if found
//   - Found: Boolean indicating whether the section exists
//
// Returns an error if the specified service doesn't exist in the project.
func (s *projectService) GetServiceConfigSection(
	ctx context.Context,
	req *azdext.GetServiceConfigSectionRequest,
) (*azdext.GetServiceConfigSectionResponse, error) {
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Validate service exists
	if err := s.validateServiceExists(ctx, req.ServiceName); err != nil {
		return nil, err
	}

	// Load entire project config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Construct path to service config section: "services.<serviceName>.<path>"
	servicePath := fmt.Sprintf("services.%s", req.ServiceName)
	if req.Path != "" {
		servicePath = fmt.Sprintf("%s.%s", servicePath, req.Path)
	}

	section, found := cfg.GetMap(servicePath)

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

// GetServiceConfigValue retrieves a specific configuration value from a service's configuration.
// This method provides access to individual service-specific configuration values (both core fields
// like "host", "project" and AdditionalProperties) using dot-notation paths.
//
// Parameters:
//   - req.ServiceName: Name of the service to retrieve configuration from
//   - req.Path: Dot-notation path to the configuration value (e.g., "custom.database.port", "host", "project")
//
// Returns:
//   - Value: The configuration value as a protobuf Value if found
//   - Found: Boolean indicating whether the value exists
//
// Supports all JSON types: strings, numbers, booleans, objects, and arrays.
// Returns an error if the specified service doesn't exist in the project.
func (s *projectService) GetServiceConfigValue(
	ctx context.Context,
	req *azdext.GetServiceConfigValueRequest,
) (*azdext.GetServiceConfigValueResponse, error) {
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Validate service exists
	if err := s.validateServiceExists(ctx, req.ServiceName); err != nil {
		return nil, err
	}

	// Load entire project config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Construct path to service config value: "services.<serviceName>.<path>"
	servicePath := fmt.Sprintf("services.%s.%s", req.ServiceName, req.Path)

	value, ok := cfg.Get(servicePath)

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

// SetServiceConfigSection sets or updates a configuration section in a service's configuration.
// This method allows extensions to store complex service-specific configuration data (both core fields
// and AdditionalProperties) as nested objects. The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.ServiceName: Name of the service to update configuration for
//   - req.Path: Dot-notation path where to store the section
//   - req.Section: The configuration section as a protobuf Struct containing the data
//
// Returns an error if the specified service doesn't exist in the project.
// If the path doesn't exist, it will be created. Existing data at the path will be replaced.
func (s *projectService) SetServiceConfigSection(
	ctx context.Context,
	req *azdext.SetServiceConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Validate service exists
	if err := s.validateServiceExists(ctx, req.ServiceName); err != nil {
		return nil, err
	}

	// Load the full config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Construct path to service config section: "services.<serviceName>.<path>"
	servicePath := fmt.Sprintf("services.%s", req.ServiceName)
	if req.Path != "" {
		servicePath = fmt.Sprintf("%s.%s", servicePath, req.Path)
	}

	// Convert protobuf Struct to map
	sectionMap := req.Section.AsMap()
	if err := cfg.Set(servicePath, sectionMap); err != nil {
		return nil, fmt.Errorf("failed to set service config section: %w", err)
	}

	// Save the updated configuration (validates structure)
	if err := project.SaveConfig(ctx, cfg, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	// Reload and update the lazy cache, preserving the EventDispatcher
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// SetServiceConfigValue sets or updates a specific configuration value in a service's configuration.
// This method allows extensions to store individual service-specific configuration values (both core
// fields like "host", "project" and AdditionalProperties). The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.ServiceName: Name of the service to update configuration for
//   - req.Path: Dot-notation path where to store the value (e.g., "custom.database.port", "host", "project")
//   - req.Value: The configuration value as a protobuf Value (string, number, boolean, etc.)
//
// Returns an error if the specified service doesn't exist in the project.
// If the path doesn't exist, intermediate objects will be created automatically.
// Existing data at the path will be replaced.
func (s *projectService) SetServiceConfigValue(
	ctx context.Context,
	req *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Validate service exists
	if err := s.validateServiceExists(ctx, req.ServiceName); err != nil {
		return nil, err
	}

	// Load the full config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Construct path to service config value: "services.<serviceName>.<path>"
	servicePath := fmt.Sprintf("services.%s.%s", req.ServiceName, req.Path)

	// Convert protobuf Value to interface{}
	value := req.Value.AsInterface()
	if err := cfg.Set(servicePath, value); err != nil {
		return nil, fmt.Errorf("failed to set service config value: %w", err)
	}

	// Save the updated configuration (validates structure)
	if err := project.SaveConfig(ctx, cfg, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	// Reload and update the lazy cache, preserving the EventDispatcher
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// UnsetServiceConfig removes a configuration value or section from a service's configuration.
// This method allows extensions to clean up service-specific configuration data (both core fields
// and AdditionalProperties). The changes are immediately persisted to the project file.
//
// Parameters:
//   - req.ServiceName: Name of the service to remove configuration from
//   - req.Path: Dot-notation path to the configuration to remove (e.g., "custom.database.port", "module")
//
// Returns an error if the specified service doesn't exist in the project.
// If the path points to a value, only that value is removed.
// If the path points to a section, the entire section and all its contents are removed.
// If the path doesn't exist, the operation succeeds without error.
func (s *projectService) UnsetServiceConfig(
	ctx context.Context,
	req *azdext.UnsetServiceConfigRequest,
) (*azdext.EmptyResponse, error) {
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	// Validate service exists
	if err := s.validateServiceExists(ctx, req.ServiceName); err != nil {
		return nil, err
	}

	// Load the full config as a map
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	// Construct path to service config: "services.<serviceName>.<path>"
	servicePath := fmt.Sprintf("services.%s.%s", req.ServiceName, req.Path)

	if err := cfg.Unset(servicePath); err != nil {
		return nil, fmt.Errorf("failed to unset service config: %w", err)
	}

	// Save the updated configuration (validates structure)
	if err := project.SaveConfig(ctx, cfg, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	// Reload and update the lazy cache, preserving the EventDispatcher
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
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
