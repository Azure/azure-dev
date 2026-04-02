// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package tool provides the type system and built-in registry for azd tool definitions.
//
// Tools are external programs, VS Code extensions, servers, or libraries that
// complement the Azure Developer CLI. Each tool carries detection, versioning,
// and per-platform installation metadata so that azd can check, install, and
// upgrade the developer toolchain automatically.
package tool

import (
	"slices"
)

// ToolCategory classifies a tool by its runtime shape.
type ToolCategory string

const (
	// ToolCategoryCLI is a standalone command-line binary (e.g. az, copilot).
	ToolCategoryCLI ToolCategory = "cli"
	// ToolCategoryExtension is an IDE extension (e.g. VS Code extensions).
	ToolCategoryExtension ToolCategory = "extension"
	// ToolCategoryServer is a long-running background process or server (e.g. MCP server).
	ToolCategoryServer ToolCategory = "server"
	// ToolCategoryLibrary is an azd extension or plugin library.
	ToolCategoryLibrary ToolCategory = "library"
)

// ToolPriority indicates how strongly a tool is recommended.
type ToolPriority string

const (
	// ToolPriorityRecommended marks a tool that most azd users should install.
	ToolPriorityRecommended ToolPriority = "recommended"
	// ToolPriorityOptional marks a tool that is useful but not essential.
	ToolPriorityOptional ToolPriority = "optional"
)

// InstallStrategy describes how to install a tool on a specific platform.
type InstallStrategy struct {
	// PackageManager is the package manager name (e.g. "winget", "brew", "apt", "npm", "code").
	PackageManager string
	// PackageId is the identifier within the package manager (e.g. "Microsoft.AzureCLI").
	PackageId string
	// InstallCommand is the full shell command when a simple package-manager install
	// does not apply (e.g. "curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash").
	InstallCommand string
	// FallbackUrl points to manual installation instructions.
	FallbackUrl string
}

// ToolDefinition is the complete metadata for a single tool in the registry.
type ToolDefinition struct {
	// Id is the unique, kebab-case identifier for the tool (e.g. "az-cli").
	Id string
	// Name is the human-readable display name.
	Name string
	// Description summarizes what the tool does in one sentence.
	Description string
	// Category classifies the tool (CLI, extension, server, or library).
	Category ToolCategory
	// Priority indicates whether the tool is recommended or optional.
	Priority ToolPriority
	// Website is the canonical documentation URL.
	Website string
	// DetectCommand is the binary name used to verify the tool is installed (e.g. "az").
	DetectCommand string
	// VersionArgs are the CLI arguments that print a version string (e.g. ["--version"]).
	VersionArgs []string
	// VersionRegex is a Go regular expression with a capture group for the semver portion
	// of the version output (e.g. `azure-cli\s+(\d+\.\d+\.\d+)`).
	VersionRegex string
	// InstallStrategies maps a GOOS value ("windows", "darwin", "linux") to the
	// platform-specific installation strategy.
	InstallStrategies map[string]InstallStrategy
	// Dependencies lists the IDs of tools that must be installed before this one.
	Dependencies []string
}

// BuiltInTools returns the full set of tools that ship with the azd tool registry.
// The returned slice is a fresh copy; callers may safely append or modify it.
func BuiltInTools() []*ToolDefinition {
	return slices.Clone(builtInTools)
}

// FindTool returns the built-in tool with the given id, or nil if none matches.
func FindTool(id string) *ToolDefinition {
	for _, t := range builtInTools {
		if t.Id == id {
			return t
		}
	}
	return nil
}

// FindToolsByCategory returns every built-in tool whose category matches.
// The returned slice is a fresh copy.
func FindToolsByCategory(category ToolCategory) []*ToolDefinition {
	var result []*ToolDefinition
	for _, t := range builtInTools {
		if t.Category == category {
			result = append(result, t)
		}
	}
	return result
}

// builtInTools is the canonical, read-only manifest of tools known to azd.
// Use [BuiltInTools] to obtain a safe copy.
var builtInTools = []*ToolDefinition{
	azCLI(),
	githubCopilotCLI(),
	vscodeAzureTools(),
	vscodeBicep(),
	vscodeGitHubCopilot(),
	azureMCPServer(),
	azdAIExtensions(),
}

// ---------------------------------------------------------------------------
// Individual tool constructors – one function per tool keeps the manifest
// readable without one huge composite literal.
// ---------------------------------------------------------------------------

func azCLI() *ToolDefinition {
	return &ToolDefinition{
		Id:            "az-cli",
		Name:          "Azure CLI",
		Description:   "The Azure command-line interface for managing Azure resources.",
		Category:      ToolCategoryCLI,
		Priority:      ToolPriorityRecommended,
		Website:       "https://learn.microsoft.com/cli/azure/",
		DetectCommand: "az",
		VersionArgs:   []string{"--version"},
		VersionRegex:  `azure-cli\s+(\d+\.\d+\.\d+)`,
		InstallStrategies: map[string]InstallStrategy{
			"windows": {
				PackageManager: "winget",
				PackageId:      "Microsoft.AzureCLI",
			},
			"darwin": {
				PackageManager: "brew",
				PackageId:      "azure-cli",
			},
			"linux": {
				InstallCommand: "curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash",
				FallbackUrl:    "https://learn.microsoft.com/cli/azure/install-azure-cli",
			},
		},
	}
}

func githubCopilotCLI() *ToolDefinition {
	return &ToolDefinition{
		Id:            "github-copilot-cli",
		Name:          "GitHub Copilot CLI",
		Description:   "AI-powered CLI assistant from GitHub Copilot.",
		Category:      ToolCategoryCLI,
		Priority:      ToolPriorityRecommended,
		Website:       "https://docs.github.com/copilot/github-copilot-in-the-cli",
		DetectCommand: "copilot",
		VersionArgs:   []string{"--version"},
		VersionRegex:  `(\d+\.\d+\.\d+)`,
		InstallStrategies: map[string]InstallStrategy{
			"windows": {
				PackageManager: "winget",
				PackageId:      "GitHub.Copilot",
			},
			"darwin": {
				PackageManager: "brew",
				PackageId:      "copilot-cli",
			},
			"linux": {
				PackageManager: "npm",
				PackageId:      "@github/copilot",
				InstallCommand: "npm install -g @github/copilot",
			},
		},
	}
}

func vscodeAzureTools() *ToolDefinition {
	return &ToolDefinition{
		Id:   "vscode-azure-tools",
		Name: "Azure Tools VS Code Extension",
		Description: "VS Code extension for browsing and managing " +
			"Azure resources.",
		Category:      ToolCategoryExtension,
		Priority:      ToolPriorityRecommended,
		Website:       "https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-azureresourcegroups",
		DetectCommand: "code",
		VersionArgs:   []string{"--list-extensions", "--show-versions"},
		VersionRegex:  `ms-azuretools\.vscode-azureresourcegroups@(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "code",
			InstallCommand: "code --install-extension ms-azuretools.vscode-azureresourcegroups",
		}),
	}
}

func vscodeBicep() *ToolDefinition {
	return &ToolDefinition{
		Id:            "vscode-bicep",
		Name:          "Bicep VS Code Extension",
		Description:   "VS Code extension providing language support for Azure Bicep.",
		Category:      ToolCategoryExtension,
		Priority:      ToolPriorityRecommended,
		Website:       "https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-bicep",
		DetectCommand: "code",
		VersionArgs:   []string{"--list-extensions", "--show-versions"},
		VersionRegex:  `ms-azuretools\.vscode-bicep@(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "code",
			InstallCommand: "code --install-extension ms-azuretools.vscode-bicep",
		}),
	}
}

func vscodeGitHubCopilot() *ToolDefinition {
	return &ToolDefinition{
		Id:            "vscode-github-copilot",
		Name:          "GitHub Copilot VS Code Extension",
		Description:   "VS Code extension for AI-powered code completions.",
		Category:      ToolCategoryExtension,
		Priority:      ToolPriorityOptional,
		Website:       "https://marketplace.visualstudio.com/items?itemName=GitHub.copilot",
		DetectCommand: "code",
		VersionArgs:   []string{"--list-extensions", "--show-versions"},
		VersionRegex:  `GitHub\.copilot@(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "code",
			InstallCommand: "code --install-extension GitHub.copilot",
		}),
	}
}

func azureMCPServer() *ToolDefinition {
	return &ToolDefinition{
		Id:            "azure-mcp-server",
		Name:          "Azure MCP Server",
		Description:   "Model Context Protocol server for Azure resource interaction.",
		Category:      ToolCategoryServer,
		Priority:      ToolPriorityOptional,
		Website:       "https://github.com/Azure/azure-mcp",
		DetectCommand: "npx",
		VersionArgs:   []string{"@azure/mcp@latest", "--version"},
		VersionRegex:  `(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@azure/mcp",
			InstallCommand: "npx @azure/mcp@latest",
		}),
	}
}

func azdAIExtensions() *ToolDefinition {
	return &ToolDefinition{
		Id:            "azd-ai-extensions",
		Name:          "azd AI Extensions",
		Description:   "Azure Developer CLI extensions for AI agent workflows.",
		Category:      ToolCategoryLibrary,
		Priority:      ToolPriorityOptional,
		Website:       "https://learn.microsoft.com/azure/developer/azure-developer-cli/",
		DetectCommand: "azd",
		VersionArgs:   []string{"extension", "list"},
		VersionRegex:  `azure\.ai\.agents\s+(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			InstallCommand: "azd extension install azure.ai.agents",
		}),
		Dependencies: []string{"az-cli"},
	}
}

// allPlatforms returns an [InstallStrategies] map that uses the same strategy
// for Windows, macOS and Linux.
func allPlatforms(s InstallStrategy) map[string]InstallStrategy {
	return map[string]InstallStrategy{
		"windows": s,
		"darwin":  s,
		"linux":   s,
	}
}
