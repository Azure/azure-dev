// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//go:embed resources/app-spec-template.md
var appSpecTemplateContent string

// NewGenerateProjectSpecTemplateTool creates a new MCP tool for generating project specification templates
func NewGenerateProjectSpecTemplateTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_generate_project_spec_template",
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Generate a project specification template file (app-spec.md) in the current workspace.
This tool creates the initial template file that other tools will populate with actual 
project information during the planning and implementation process.

The template will be created at 'app-spec.md' in the current directory if it doesn't 
already exist. No parameters required.`,
			),
		),
		Handler: handleGenerateProjectSpecTemplate,
	}
}

func handleGenerateProjectSpecTemplate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	const outputPath = "app-spec.md"

	// Check if file already exists
	if _, err := os.Stat(outputPath); err == nil {
		return mcp.NewToolResultText(fmt.Sprintf("Template file '%s' already exists in the workspace. No action taken.", outputPath)), nil
	}

	// Generate current timestamp
	currentTime := time.Now()
	generatedDate := currentTime.Format("2006-01-02 15:04:05 MST")

	// Create the template with placeholder project name and current timestamp
	templateContent := strings.ReplaceAll(appSpecTemplateContent, "{{.ProjectName}}", "[PROJECT_NAME]")
	templateContent = strings.ReplaceAll(templateContent, "{{.GeneratedDate}}", generatedDate)
	templateContent = strings.ReplaceAll(templateContent, "{{.LastUpdated}}", generatedDate)

	// Write the template file to the workspace
	if err := os.WriteFile(outputPath, []byte(templateContent), 0644); err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("Error writing template file: %v", err)), nil
	}

	// Create success response
	response := fmt.Sprintf(`# Project Specification Template Created

✅ **Template File Successfully Created**

**File Path**: %s  
**Generated**: %s

## Template Overview

A comprehensive project specification template has been created in your workspace with the following structure:

- **Project Overview**: Basic project information and classification
- **Project Requirements**: Sections for requirements discovery findings  
- **Technology Stack Selection**: Documentation of stack decisions and rationale
- **Application Architecture**: Component definitions and data architecture
- **Azure Service Mapping**: Infrastructure and service configurations
- **Implementation Plan**: Development approach and deployment strategy
- **Security and Compliance**: Security architecture and requirements
- **Monitoring and Operations**: Operational procedures and monitoring
- **Project Status and Next Steps**: Implementation roadmap and success criteria

## Next Steps

The template is ready! Other tools will now populate the template sections as they discover and plan project details:

1. User intent discovery will fill the "Project Requirements" section
2. Stack selection will complete the "Technology Stack Selection" section  
3. Architecture planning will populate the "Application Architecture" and "Azure Service Mapping" sections
4. Implementation planning will complete the remaining sections

The template serves as the living documentation for your AZD project throughout the planning and implementation process.`, outputPath, generatedDate)

	return mcp.NewToolResultText(response), nil
}
