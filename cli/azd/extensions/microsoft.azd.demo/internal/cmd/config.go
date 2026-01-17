// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

// MonitoringConfig represents the project-level monitoring configuration
type MonitoringConfig struct {
	Enabled       bool   `json:"enabled"`
	Environment   string `json:"environment"`
	RetentionDays int    `json:"retentionDays"`
	AlertEmail    string `json:"alertEmail"`
}

// ServiceMonitoringConfig represents service-level monitoring configuration
type ServiceMonitoringConfig struct {
	Enabled         bool   `json:"enabled"`
	HealthCheckPath string `json:"healthCheckPath"`
	MetricsPort     int    `json:"metricsPort"`
	LogLevel        string `json:"logLevel"`
	AlertThresholds struct {
		ErrorRate      float64 `json:"errorRate"`
		ResponseTimeMs int     `json:"responseTimeMs"`
		CPUPercent     int     `json:"cpuPercent"`
	} `json:"alertThresholds"`
	Tags []string `json:"tags"`
}

func newConfigCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Setup monitoring configuration for the project and services",
		Long: `This command demonstrates the new configuration management capabilities by setting up
a realistic monitoring configuration scenario. It will:

1. Check if project-level monitoring config exists, create it if missing
2. Find the first service in the project  
3. Check if service-level monitoring config exists, create it if missing
4. Display the final configuration state

This showcases how extensions can manage both project and service-level configuration
using the new AdditionalProperties gRPC API with strongly-typed Go structs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Wait for debugger if AZD_EXT_DEBUG is set
			if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
					return nil
				}
				return fmt.Errorf("failed waiting for debugger: %w", err)
			}

			return setupMonitoringConfig(ctx, azdClient)
		},
	}
}

func setupMonitoringConfig(ctx context.Context, azdClient *azdext.AzdClient) error {
	color.HiCyan("🔧 Setting up monitoring configuration...")
	fmt.Println()

	// Step 1: Check and setup project-level monitoring config
	projectConfigCreated, err := setupProjectMonitoringConfig(ctx, azdClient)
	if err != nil {
		return err
	}

	// Step 2: Get project to find services
	projectResp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}

	if len(projectResp.Project.Services) == 0 {
		color.Yellow("⚠️  No services found in project - skipping service configuration")
		return displayConfigurationSummary(ctx, azdClient, "", projectConfigCreated, false)
	}

	// Step 3: Setup monitoring for the first service
	var firstServiceName string
	for serviceName := range projectResp.Project.Services {
		firstServiceName = serviceName
		break
	}

	color.HiWhite("📦 Found service: %s", firstServiceName)
	serviceConfigCreated, err := setupServiceMonitoringConfig(ctx, azdClient, firstServiceName)
	if err != nil {
		return err
	}

	// Step 4: Display final configuration state
	return displayConfigurationSummary(ctx, azdClient, firstServiceName, projectConfigCreated, serviceConfigCreated)
}

// Helper functions to convert between type-safe structs and protobuf structs
func structToProtobuf(v interface{}) (*structpb.Struct, error) {
	// Convert struct to JSON bytes
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal struct to JSON: %w", err)
	}

	// Convert JSON bytes to map
	var m map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to map: %w", err)
	}

	// Convert map to protobuf struct
	return structpb.NewStruct(m)
}

func protobufToStruct(pbStruct *structpb.Struct, target interface{}) error {
	// Convert protobuf struct to JSON bytes
	jsonBytes, err := json.Marshal(pbStruct.AsMap())
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf struct to JSON: %w", err)
	}

	// Unmarshal JSON into target struct
	if err := json.Unmarshal(jsonBytes, target); err != nil {
		return fmt.Errorf("failed to unmarshal JSON to target struct: %w", err)
	}

	return nil
}

func setupProjectMonitoringConfig(ctx context.Context, azdClient *azdext.AzdClient) (bool, error) {
	color.HiWhite("🏢 Checking project-level monitoring configuration...")

	// Check if monitoring config already exists
	configResp, err := azdClient.Project().GetConfigSection(ctx, &azdext.GetProjectConfigSectionRequest{
		Path: "monitoring",
	})
	if err != nil {
		return false, fmt.Errorf("failed to check project monitoring config: %w", err)
	}

	if configResp.Found {
		color.Green("  ✓ Project monitoring configuration already exists")

		// Demonstrate reading back the configuration into our type-safe struct
		var existingConfig MonitoringConfig
		if err := protobufToStruct(configResp.Section, &existingConfig); err != nil {
			return false, fmt.Errorf("failed to convert existing config: %w", err)
		}
		color.Cyan("    Current config: Environment=%s, Retention=%d days",
			existingConfig.Environment, existingConfig.RetentionDays)
		return false, nil // false means it already existed (not created)
	}

	// Create default monitoring configuration using type-safe struct
	color.Yellow("  ⚙️  Creating project monitoring configuration...")

	monitoringConfig := MonitoringConfig{
		Enabled:       true,
		Environment:   "development",
		RetentionDays: 30,
		AlertEmail:    "admin@company.com",
	}

	// Convert type-safe struct to protobuf struct
	configStruct, err := structToProtobuf(monitoringConfig)
	if err != nil {
		return false, fmt.Errorf("failed to convert config struct: %w", err)
	}

	_, err = azdClient.Project().SetConfigSection(ctx, &azdext.SetProjectConfigSectionRequest{
		Path:    "monitoring",
		Section: configStruct,
	})
	if err != nil {
		return false, fmt.Errorf("failed to set project monitoring config: %w", err)
	}

	color.Green("  ✓ Project monitoring configuration created successfully")
	return true, nil // true means it was created
}

func setupServiceMonitoringConfig(ctx context.Context, azdClient *azdext.AzdClient, serviceName string) (bool, error) {
	color.HiWhite("🔍 Checking service-level monitoring configuration for '%s'...", serviceName)

	// Check if service monitoring config already exists
	configResp, err := azdClient.Project().GetServiceConfigSection(ctx, &azdext.GetServiceConfigSectionRequest{
		ServiceName: serviceName,
		Path:        "monitoring",
	})
	if err != nil {
		return false, fmt.Errorf("failed to check service monitoring config: %w", err)
	}

	if configResp.Found {
		color.Green("  ✓ Service monitoring configuration already exists")

		// Demonstrate reading back the configuration into our type-safe struct
		var existingConfig ServiceMonitoringConfig
		if err := protobufToStruct(configResp.Section, &existingConfig); err != nil {
			return false, fmt.Errorf("failed to convert existing service config: %w", err)
		}
		color.Cyan("    Current config: Port=%d, LogLevel=%s, Tags=%v",
			existingConfig.MetricsPort, existingConfig.LogLevel, existingConfig.Tags)
		return false, nil // false means it already existed (not created)
	}

	// Create default service monitoring configuration using type-safe struct
	color.Yellow("  ⚙️  Creating service monitoring configuration...")

	serviceConfig := ServiceMonitoringConfig{
		Enabled:         true,
		HealthCheckPath: "/health",
		MetricsPort:     9090,
		LogLevel:        "info",
		Tags:            []string{"web", "api", "production"},
	}

	// Set alert thresholds
	serviceConfig.AlertThresholds.ErrorRate = 5.0
	serviceConfig.AlertThresholds.ResponseTimeMs = 2000
	serviceConfig.AlertThresholds.CPUPercent = 80

	// Convert type-safe struct to protobuf struct
	configStruct, err := structToProtobuf(serviceConfig)
	if err != nil {
		return false, fmt.Errorf("failed to create service config struct: %w", err)
	}

	_, err = azdClient.Project().SetServiceConfigSection(ctx, &azdext.SetServiceConfigSectionRequest{
		ServiceName: serviceName,
		Path:        "monitoring",
		Section:     configStruct,
	})
	if err != nil {
		return false, fmt.Errorf("failed to set service monitoring config: %w", err)
	}

	color.Green("  ✓ Service monitoring configuration created successfully")
	return true, nil // true means it was created
}

func displayConfigurationSummary(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	projectConfigCreated, serviceConfigCreated bool,
) error {
	fmt.Println()
	color.HiCyan("📊 Configuration Summary")
	color.HiCyan("========================")
	fmt.Println()

	// Display project monitoring config with status
	projectStatus := "📋 Already existed"
	if projectConfigCreated {
		projectStatus = "✨ Newly created"
	}
	color.HiWhite("🏢 Project Monitoring Configuration (%s):", projectStatus)
	projectConfigResp, err := azdClient.Project().GetConfigSection(ctx, &azdext.GetProjectConfigSectionRequest{
		Path: "monitoring",
	})
	if err != nil {
		return fmt.Errorf("failed to get project monitoring config: %w", err)
	}

	if projectConfigResp.Found {
		if err := printConfigSection(projectConfigResp.Section.AsMap()); err != nil {
			return err
		}
	}

	fmt.Println()

	// Display service monitoring config with status (only if we have a service)
	if serviceName != "" {
		serviceStatus := "📋 Already existed"
		if serviceConfigCreated {
			serviceStatus = "✨ Newly created"
		}
		color.HiWhite("📦 Service '%s' Monitoring Configuration (%s):", serviceName, serviceStatus)
		serviceConfigResp, err := azdClient.Project().GetServiceConfigSection(ctx, &azdext.GetServiceConfigSectionRequest{
			ServiceName: serviceName,
			Path:        "monitoring",
		})
		if err != nil {
			return fmt.Errorf("failed to get service monitoring config: %w", err)
		}

		if serviceConfigResp.Found {
			if err := printConfigSection(serviceConfigResp.Section.AsMap()); err != nil {
				return err
			}
		}
		fmt.Println()
	}

	color.HiGreen("✅ Monitoring configuration setup complete!")
	fmt.Println()
	color.HiBlue("💡 This demonstrates how extensions can manage both project and service-level")
	color.HiBlue("   configuration using the new AdditionalProperties gRPC API with type-safe")
	color.HiBlue("   Go structs for complex configuration scenarios.")

	return nil
}

func printConfigSection(section map[string]interface{}) error {
	jsonBytes, err := json.MarshalIndent(section, "   ", "  ")
	if err != nil {
		return fmt.Errorf("failed to format section: %w", err)
	}
	fmt.Printf("   %s\n", string(jsonBytes))
	return nil
}
