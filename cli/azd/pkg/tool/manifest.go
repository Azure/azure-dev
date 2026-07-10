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
// Claude Code) installs and updates a skill. Skill tools carry one or more
// SkillHost entries; by default the installer targets the preferred host
// (the first on PATH), but install/upgrade can target specific or all
// detected hosts when the caller selects them (e.g. via `--host`).
type SkillHost struct {
	// Host is the agent's display name, shown to the user and matched
	// (case-insensitively) by --agent (e.g. "Copilot", "Claude").
	Host string
	// Command is the agent CLI's executable name, used to run plugin
	// commands and version probes (e.g. "copilot", "claude"). Required and
	// must be non-empty: it is the real, case-correct binary name run
	// directly by the installer and detector paths, so exec works on
	// case-sensitive filesystems (Linux). TestBuiltInTools_SkillHostsHaveCommand
	// enforces that every configured host sets it.
	Command string
	// MarketplaceAddCommand is the optional one-time command that registers
	// the plugin marketplace with the host (e.g. ["plugin", "marketplace",
	// "add", "microsoft/azure-skills"]). Empty when not required.
	MarketplaceAddCommand []string
	// PluginInstallCommand installs the plugin via the host
	// (e.g. ["plugin", "install", "azure@azure-skills"]).
	PluginInstallCommand []string
	// PluginUpdateCommand updates the plugin to its latest version.
	PluginUpdateCommand []string
	// PluginUninstallCommand removes the plugin from the host
	// (e.g. ["plugin", "uninstall", "azure@azure-skills"]).
	PluginUninstallCommand []string
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
	// BinaryVersionArgs are the CLI arguments that make the host binary
	// print its own version (e.g. ["--version"]). Together with
	// BinaryVersionRegex these let the installer confirm the host is a
	// genuine, functional CLI before installing through it — not merely a
	// file of the same name on PATH. Some environments place a launcher
	// stub on PATH (e.g. the VS Code GitHub Copilot Chat extension drops a
	// small `copilot` stub into its globalStorage and adds that folder to
	// the integrated terminal's PATH) that exits 0 but only prompts to
	// install the real CLI; such a stub passes a bare PATH existence check
	// yet cannot install the skill. When empty, the installer falls back to
	// an existence-only check.
	BinaryVersionArgs []string
	// BinaryVersionRegex is a Go regular expression whose first capture
	// group matches the host binary's own version. To avoid mistaking a
	// launcher stub for a real CLI, anchor it to the host's `--version`
	// banner with `(?m)^` (e.g. `(?m)^GitHub Copilot CLI\s+v?(\d+\.\d+\.\d+)`)
	// rather than matching a bare semver: a stub's output may contain an
	// incidental version-shaped token (a bundled runtime version, a path
	// build number, a URL) that must not count. The installer treats a match
	// against the probe output as proof the host CLI is genuinely installed.
	// When empty, the functional probe is skipped.
	BinaryVersionRegex string
}

// InstallStrategy describes how to install a tool on a specific platform.
type InstallStrategy struct {
	// PackageManager is the package manager name (e.g. "winget", "brew", "apt", "npm", "code").
	PackageManager string
	// PackageId is the identifier within the package manager (e.g. "Microsoft.AzureCLI").
	PackageId string
	// Cask indicates that PackageId is a Homebrew cask rather than a formula.
	// When true, azd adds the `--cask` flag to brew install/upgrade/uninstall
	// and reads the cask version from `brew info` output. Ignored for managers
	// other than "brew".
	Cask bool
	// InstallCommand is the full shell command when a simple package-manager install
	// does not apply (e.g. "curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash").
	InstallCommand string
	// UninstallCommand is the full command that reverses InstallCommand
	// when no package-manager uninstall applies (e.g.
	// "azd extension uninstall azure.ai.agents"). When empty and no
	// package manager is configured, azd reports that it cannot uninstall
	// the tool automatically.
	UninstallCommand string
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
	// InstallStrategies maps a GOOS value ("windows", "darwin", "linux") to an
	// ordered list of platform-specific installation strategies. The list is in
	// preference order: install/upgrade use the first strategy whose package
	// manager is available (or that runs a self-contained command); uninstall
	// detects which strategy actually installed the tool and removes it via
	// that one. Most tools have a single strategy per platform; tools with
	// several official install methods (e.g. the GitHub Copilot CLI) list them
	// all.
	InstallStrategies map[string][]InstallStrategy
	// SkillHosts describes the agent CLI hosts that can install this tool when
	// Category == ToolCategorySkill. Hosts are listed in preference order: by
	// default the first host on PATH is used, but install/upgrade can target
	// specific or all detected hosts (e.g. `--host all`). Platform-agnostic
	// because the host CLI's plugin command syntax does not vary between
	// operating systems. Ignored for other categories.
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
		InstallStrategies: map[string][]InstallStrategy{
			"windows": {{
				PackageManager: "winget",
				PackageId:      "Microsoft.AzureCLI",
			}},
			"darwin": {{
				PackageManager: "brew",
				PackageId:      "azure-cli",
			}},
			"linux": {{
				PackageManager: "apt",
				PackageId:      "azure-cli",
				InstallCommand: "curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash",
				FallbackUrl:    "https://learn.microsoft.com/cli/azure/install-azure-cli",
			}},
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
		// The Copilot CLI can be installed several ways per platform (see the
		// official docs). They are listed in preference order; install picks
		// the first available, and uninstall detects which one was used.
		InstallStrategies: map[string][]InstallStrategy{
			"windows": {
				{PackageManager: "winget", PackageId: "GitHub.Copilot"},
				{PackageManager: "npm", PackageId: "@github/copilot"},
			},
			"darwin": {
				{PackageManager: "brew", PackageId: "copilot-cli", Cask: true},
				{PackageManager: "npm", PackageId: "@github/copilot"},
				{InstallCommand: "curl -fsSL https://gh.io/copilot-install | bash"},
			},
			"linux": {
				{PackageManager: "brew", PackageId: "copilot-cli", Cask: true},
				{PackageManager: "npm", PackageId: "@github/copilot"},
				{InstallCommand: "curl -fsSL https://gh.io/copilot-install | bash"},
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
			InstallCommand:   "azd extension install azure.ai.agents --source azd",
			UninstallCommand: "azd extension uninstall azure.ai.agents",
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
				Host:                   "GitHub Copilot CLI",
				Command:                "copilot",
				MarketplaceAddCommand:  []string{"plugin", "marketplace", "add", "microsoft/azure-skills"},
				PluginInstallCommand:   []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:    []string{"plugin", "update", "azure@azure-skills"},
				PluginUninstallCommand: []string{"plugin", "uninstall", "azure@azure-skills"},
				PluginListCommand:      []string{"plugin", "list"},
				PluginName:             "azure@azure-skills",
				// Sample: "  • azure@azure-skills (v1.1.70)"
				VersionRegex: `azure@azure-skills[^\n]*?(\d+\.\d+\.\d+)`,
				// Probe the host binary itself so a launcher stub that only
				// prompts to install the real CLI is not mistaken for a host.
				// Anchored to GitHub Copilot CLI's `--version` banner ("GitHub Copilot
				// CLI 1.0.64-3") so an incidental semver cannot pass.
				BinaryVersionArgs:  []string{"--version"},
				BinaryVersionRegex: `(?m)^GitHub Copilot CLI\s+v?(\d+\.\d+\.\d+)`,
			},
			{
				Host:    "Claude Code CLI",
				Command: "claude",
				MarketplaceAddCommand: []string{
					"plugin", "marketplace", "add", "https://github.com/microsoft/azure-skills",
				},
				PluginInstallCommand:   []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:    []string{"plugin", "update", "azure@azure-skills"},
				PluginUninstallCommand: []string{"plugin", "uninstall", "azure@azure-skills"},
				PluginListCommand:      []string{"plugin", "list", "--json"},
				PluginName:             "azure@azure-skills",
				// `claude plugin list` ignores a plugin-name argument, so
				// list every plugin as JSON and anchor on the
				// azure@azure-skills entry. Sample (--json):
				//   [
				//     {
				//       "id": "azure@azure-skills",
				//       "version": "1.1.73",
				//       ...
				//     }
				//   ]
				// "version" follows "id" within the same object, so [^}]
				// keeps the capture scoped to the azure@azure-skills entry.
				VersionRegex: `"id":\s*"azure@azure-skills"[^}]*?"version":\s*"v?(\d+\.\d+\.\d+)"`,
				// Probe the host binary itself so a launcher stub that only
				// prompts to install the real CLI is not mistaken for a host.
				// Anchored to claude's `--version` banner ("2.1.178 (Claude
				// Code)") so an incidental semver cannot pass.
				BinaryVersionArgs:  []string{"--version"},
				BinaryVersionRegex: `(?m)^v?(\d+\.\d+\.\d+)\s+\(Claude Code\)`,
			},
		},
	}
}

// allPlatforms returns an [InstallStrategies] map that uses the same ordered
// strategy list for Windows, macOS and Linux. A single strategy is the common
// case; pass several for tools installable multiple ways on every platform.
func allPlatforms(strategies ...InstallStrategy) map[string][]InstallStrategy {
	return map[string][]InstallStrategy{
		"windows": strategies,
		"darwin":  strategies,
		"linux":   strategies,
	}
}
