// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templateversion"
)

// NewTemplateVersionMiddleware creates middleware that ensures the template version file exists
// and updates azure.yaml with the version information
func NewTemplateVersionMiddleware(container *ioc.NestedContainer) middleware.Middleware {
	return &templateVersionMiddleware{
		container: container,
	}
}

type templateVersionMiddleware struct {
	container *ioc.NestedContainer
}

// Run implements middleware.Middleware
func (m *templateVersionMiddleware) Run(ctx context.Context, next middleware.NextFn) (*actions.ActionResult, error) {
	ctx, span := tracing.Start(ctx, "template-version-middleware")
	defer span.End()

	// Get a console for debug messages
	var console input.Console
	err := m.container.Resolve(&console)
	if err != nil {
		// No console available, just continue with the command
		span.SetStatus(1, "Failed to get console for debug messages")
	}

	// For now, we'll enable template versioning for all commands
	// since we don't have a way to determine the specific command being run
	if console != nil {
		console.Message(ctx, "DEBUG: Checking template version")
	}

	// Get the project path from the context
	projectPath := ""

	// Try to create a new AzdContext to get the project path
	azdContext, err := azdcontext.NewAzdContext()
	if err != nil {
		// No AZD context available, continue with the command
		if console != nil {
			console.Message(ctx, fmt.Sprintf("DEBUG: Failed to create AZD context: %v", err))
		}
		span.SetStatus(1, "Failed to create AZD context")
		return next(ctx)
	}

	projectPath = azdContext.ProjectDirectory()
	if console != nil {
		console.Message(ctx, fmt.Sprintf("DEBUG: Got project directory: %s", projectPath))
	}

	if projectPath == "" {
		// No project path available, continue with the command
		if console != nil {
			console.Message(ctx, "DEBUG: Project path is empty, skipping template version check")
		}
		span.SetStatus(1, "No project path found")
		return next(ctx)
	}

	// Get template version manager
	var runner exec.CommandRunner
	if err = m.container.Resolve(&runner); err != nil {
		// Runner not available, continue with the command
		if console != nil {
			console.Message(ctx, fmt.Sprintf("DEBUG: Failed to resolve command runner: %v", err))
		}
		span.SetStatus(1, "Command runner not available")
		return next(ctx)
	}
	
	// Create version manager directly instead of using IoC container
	versionManager := templateversion.NewManager(console, runner)
	
	if console != nil {
		console.Message(ctx, "DEBUG: Successfully created version manager directly")
	}

	// Ensure template version file exists - errors are handled within the EnsureTemplateVersion method
	if console != nil {
		console.Message(ctx, fmt.Sprintf("DEBUG: Calling EnsureTemplateVersion with projectPath: %s", projectPath))
	}
	
	version, err := versionManager.EnsureTemplateVersion(ctx, projectPath)
	
	if console != nil {
		console.Message(ctx, fmt.Sprintf("DEBUG: EnsureTemplateVersion returned version: %s, err: %v", version, err))
	}
	
	if err != nil {
		// Log error but continue with the command
		span.SetStatus(1, "Failed to ensure template version")
		if console != nil {
			console.Message(ctx, fmt.Sprintf("DEBUG: Failed to ensure template version: %v", err))
		}
	} else if version != "" {
		// Only update azure.yaml if we successfully got a version
		if console != nil {
			console.Message(ctx, fmt.Sprintf("DEBUG: Updating azure.yaml with version: %s", version))
		}
		
		err = updateAzureYamlVersion(ctx, projectPath, version)
		if err != nil {
			// Log error but continue with the command
			span.SetStatus(1, "Failed to update azure.yaml with template version")
			if console != nil {
				console.Message(ctx, fmt.Sprintf("DEBUG: Failed to update azure.yaml with template version: %v", err))
			}
		} else if console != nil {
			console.Message(ctx, "DEBUG: Successfully updated azure.yaml with version")
		}
	}

	// Always continue with the next middleware/command
	return next(ctx)
}

// isTemplateCommand returns true if the command requires a template
func isTemplateCommand(commandName string) bool {
	templateCommands := []string{
		"init",
		"up",
		"deploy",
		"provision",
		"env", // Some env commands may need the template
		"pipeline",
		"monitor",
	}

	for _, cmd := range templateCommands {
		if cmd == commandName {
			return true
		}
	}

	return false
}

// updateAzureYamlVersion updates the azure.yaml file with the template version
func updateAzureYamlVersion(ctx context.Context, projectPath string, version string) error {
	ctx, span := tracing.Start(ctx, "update-azure-yaml-version")
	defer span.End()

	if projectPath == "" {
		return nil
	}

	// Get the azure.yaml path
	azureYamlPath := filepath.Join(projectPath, "azure.yaml")

	// Load the project config
	projectConfig, err := project.Load(ctx, azureYamlPath)
	if err != nil {
		return err
	}

	if projectConfig == nil {
		return nil
	}

	// Check if version already matches
	if projectConfig.TrackingId == version {
		return nil
	}

	// Update the tracking ID
	projectConfig.TrackingId = version

	// Save the updated config
	return project.Save(ctx, projectConfig, azureYamlPath)
}
