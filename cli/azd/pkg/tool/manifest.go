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
	// ToolCategoryVSCodeExtension is a VS Code extension.
	ToolCategoryVSCodeExtension ToolCategory = "vscode-extension"
	// ToolCategoryServer is a long-running background process or server (e.g. MCP server).
	ToolCategoryServer ToolCategory = "server"
	// ToolCategoryAzdExtension is an azd extension installed via `azd extension install`.
	ToolCategoryAzdExtension ToolCategory = "azd-extension"
	// ToolCategorySkill is a skill hosted by an agent CLI that azd installs through the
	// host's native plugin commands.
	ToolCategorySkill ToolCategory = "skill"
)

// ToolPriority indicates how strongly a tool is recommended.
type ToolPriority string

const (
	// ToolPriorityRecommended marks a tool that most azd users should install.
	ToolPriorityRecommended ToolPriority = "recommended"
	// ToolPriorityOptional marks a tool that is useful but not essential.
	ToolPriorityOptional ToolPriority = "optional"
)

// Checksum describes the expected hash of a downloaded artifact.
type Checksum struct {
	// Algorithm is the hash algorithm (e.g. "sha256", "sha512").
	Algorithm string
	// Value is the hex-encoded checksum to compare against.
	Value string
}

// SkillHost describes how a single agent CLI host (e.g. GitHub Copilot CLI,
// Claude Code) installs, updates, and uninstalls a skill. Skill
// tools carry one or more SkillHost entries; the installer picks the first
// host whose binary is on PATH.
type SkillHost struct {
	// Host is the binary name of the agent CLI (e.g. "copilot", "claude").
	Host string
	// MarketplaceAddCommand is the optional one-time command that registers
	// the plugin marketplace with the host (e.g. ["plugin", "marketplace",
	// "add", "microsoft/azure-skills"]). Empty when not required.
	MarketplaceAddCommand []string
	// PluginInstallCommand installs the plugin via the host
	// (e.g. ["plugin", "install", "azure@azure-skills"]).
	PluginInstallCommand []string
	// PluginUpdateCommand updates the plugin to its latest version.
	PluginUpdateCommand []string
	// PluginListCommand lists installed plugins on the host
	// (e.g. ["plugin", "list"]). The detector runs this command and
	// searches the output for PluginName to decide whether the skill
	// is installed.
	PluginListCommand []string
	// PluginName is the plugin's short name as reported by the host's
	// plugin listing (e.g. "azure"). Used by the detector.
	PluginName string
	// VersionRegex is a Go regular expression with a capture group for
	// the semver portion of the version output of PluginListCommand.
	// Required: the detector treats a VersionRegex match as the
	// authoritative signal that the skill is installed (and uses the
	// captured group as InstalledVersion). A host with an empty
	// VersionRegex is never reported as installed.
	VersionRegex string
}

// InstallStrategy describes how to install a tool on a specific platform.
type InstallStrategy struct {
	// PackageManager is the package manager name (e.g. "winget", "brew", "apt", "npm", "code").
	PackageManager string
	// PackageId is the identifier within the package manager (e.g. "Microsoft.AzureCLI").
	PackageId string
	// InstallCommand is the full shell command when a simple package-manager install
	// does not apply (e.g. "curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash").
	InstallCommand string
	// DirectDownloadUrl is a URL to a binary or archive that azd downloads
	// directly. When set, azd downloads the artifact, verifies its checksum
	// (if provided), and makes it available locally. This path is used
	// instead of PackageManager or InstallCommand.
	DirectDownloadUrl string
	// Checksum is the expected hash of the artifact referenced by
	// DirectDownloadUrl. When empty, checksum verification is skipped.
	Checksum Checksum
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
	// Category classifies the tool (CLI, VS Code extension, server, or azd extension).
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
	// SkillHosts describes the agent CLI hosts that can install this tool when
	// Category == ToolCategorySkill. Hosts are evaluated in order; the first
	// one whose binary is on PATH is used. Platform-agnostic because the host
	// CLI's plugin command syntax does not vary between operating systems.
	// Ignored for other categories.
	SkillHosts []SkillHost
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
	azureSkills(),
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
				PackageManager: "apt",
				PackageId:      "azure-cli",
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
		Website:       "https://docs.github.com/copilot/how-tos/set-up/install-copilot-cli",
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
		Category:      ToolCategoryVSCodeExtension,
		Priority:      ToolPriorityRecommended,
		Website:       "https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-azureresourcegroups",
		DetectCommand: "code",
		VersionArgs:   []string{"--list-extensions", "--show-versions"},
		VersionRegex:  `ms-azuretools\.vscode-azureresourcegroups@(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "code",
			PackageId:      "ms-azuretools.vscode-azureresourcegroups",
		}),
	}
}

func vscodeBicep() *ToolDefinition {
	return &ToolDefinition{
		Id:            "vscode-bicep",
		Name:          "Bicep VS Code Extension",
		Description:   "VS Code extension providing language support for Azure Bicep.",
		Category:      ToolCategoryVSCodeExtension,
		Priority:      ToolPriorityRecommended,
		Website:       "https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-bicep",
		DetectCommand: "code",
		VersionArgs:   []string{"--list-extensions", "--show-versions"},
		VersionRegex:  `ms-azuretools\.vscode-bicep@(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "code",
			PackageId:      "ms-azuretools.vscode-bicep",
		}),
	}
}

func vscodeGitHubCopilot() *ToolDefinition {
	return &ToolDefinition{
		Id:            "GitHub.copilot-chat",
		Name:          "GitHub Copilot Chat VS Code Extension",
		Description:   "VS Code extension for AI-powered code completions, chat, and agent mode.",
		Category:      ToolCategoryVSCodeExtension,
		Priority:      ToolPriorityOptional,
		Website:       "https://marketplace.visualstudio.com/items?itemName=GitHub.copilot-chat",
		DetectCommand: "code",
		VersionArgs:   []string{"--list-extensions", "--show-versions"},
		VersionRegex:  `(?i)github\.copilot-chat@(\d+\.\d+\.\d+)`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "code",
			PackageId:      "GitHub.copilot-chat",
			FallbackUrl:    "https://marketplace.visualstudio.com/items?itemName=GitHub.copilot-chat",
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
		Website:       "https://github.com/microsoft/mcp",
		DetectCommand: "npm",
		VersionArgs:   []string{"list", "-g", "@azure/mcp", "--json"},
		VersionRegex:  `"@azure/mcp":\s*\{\s*"version":\s*"(\d+\.\d+\.\d+(?:-[^"]*)?)"`,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@azure/mcp",
			InstallCommand: "npm install -g @azure/mcp",
		}),
	}
}

func azdAIExtensions() *ToolDefinition {
	return &ToolDefinition{
		Id:            "azure.ai.agents",
		Name:          "azd AI Agent Extensions",
		Description:   "Azure Developer CLI extensions for AI agent workflows.",
		Category:      ToolCategoryAzdExtension,
		Priority:      ToolPriorityOptional,
		Website:       "https://learn.microsoft.com/azure/developer/azure-developer-cli/",
		DetectCommand: "azd",
		VersionArgs:   []string{"extension", "list", "--installed", "--output", "json"},
		InstallStrategies: allPlatforms(InstallStrategy{
			InstallCommand: "azd extension install azure.ai.agents --source azd",
		}),
	}
}

func azureSkills() *ToolDefinition {
	return &ToolDefinition{
		Id:   "azure-skills",
		Name: "Azure Skills",
		Description: "Azure skills for AI coding assistants. " +
			"Provides skills and MCP server configurations for Azure scenarios.",
		Category: ToolCategorySkill,
		Priority: ToolPriorityRecommended,
		Website:  "https://github.com/microsoft/azure-skills",
		SkillHosts: []SkillHost{
			{
				Host:                  "copilot",
				MarketplaceAddCommand: []string{"plugin", "marketplace", "add", "microsoft/azure-skills"},
				PluginInstallCommand:  []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:   []string{"plugin", "update", "azure@azure-skills"},
				PluginListCommand:     []string{"plugin", "list"},
				PluginName:            "azure@azure-skills",
				// Sample: "  • azure@azure-skills (v1.1.70)"
				VersionRegex: `azure@azure-skills[^\n]*?(\d+\.\d+\.\d+)`,
			},
			{
				Host:                  "claude",
				MarketplaceAddCommand: []string{"plugin", "marketplace", "add", "https://github.com/microsoft/azure-skills"},
				PluginInstallCommand:  []string{"plugin", "install", "azure"},
				PluginUpdateCommand:   []string{"plugin", "update", "azure@azure-skills"},
				PluginListCommand:     []string{"plugin", "list", "azure@azure-skills"},
				PluginName:            "azure@azure-skills",
				// Sample (target-filtered output):
				//   ❯ azure@azure-skills
				//     Version: 1.1.70
				//     Scope: user
				// Claude only returns the queried plugin, so a single
				// "Version: x.y.z" line is unambiguous.
				VersionRegex: `Version:\s*v?(\d+\.\d+\.\d+)`,
			},
		},
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
