// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltInTools(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsExpectedCount", func(t *testing.T) {
		t.Parallel()

		tools := BuiltInTools()
		require.Len(t, tools, 8, "expected 8 built-in tools")
	})

	t.Run("ContainsAllExpectedToolIDs", func(t *testing.T) {
		t.Parallel()

		expectedIDs := []string{
			"az-cli",
			"github-copilot-cli",
			"vscode-azure-tools",
			"vscode-bicep",
			"GitHub.copilot-chat",
			"azure-mcp-server",
			"azure.ai.agents",
			"azure-skills",
		}

		tools := BuiltInTools()
		actualIDs := make([]string, len(tools))
		for i, tool := range tools {
			actualIDs[i] = tool.Id
		}

		for _, id := range expectedIDs {
			assert.Contains(t, actualIDs, id,
				"missing expected tool %q", id)
		}
	})

	t.Run("NoDuplicateIDs", func(t *testing.T) {
		t.Parallel()

		tools := BuiltInTools()
		seen := make(map[string]bool, len(tools))
		for _, tool := range tools {
			require.False(t, seen[tool.Id],
				"duplicate tool ID %q", tool.Id)
			seen[tool.Id] = true
		}
	})

	t.Run("AllToolsHaveRequiredFields", func(t *testing.T) {
		t.Parallel()

		tools := BuiltInTools()
		for _, tool := range tools {
			assert.NotEmpty(t, tool.Id,
				"tool must have an Id")
			assert.NotEmpty(t, tool.Name,
				"tool %q must have a Name", tool.Id)
			assert.NotEmpty(t, tool.Description,
				"tool %q must have a Description", tool.Id)
			assert.NotEmpty(t, tool.Category,
				"tool %q must have a Category", tool.Id)
			if tool.Category != ToolCategorySkill {
				assert.NotEmpty(t, tool.DetectCommand,
					"tool %q must have a DetectCommand", tool.Id)
			}
		}
	})

	t.Run("AllToolsHaveValidCategory", func(t *testing.T) {
		t.Parallel()

		validCategories := map[ToolCategory]bool{
			ToolCategoryCLI:             true,
			ToolCategoryVSCodeExtension: true,
			ToolCategoryServer:          true,
			ToolCategoryAzdExtension:    true,
			ToolCategorySkill:           true,
		}

		tools := BuiltInTools()
		for _, tool := range tools {
			assert.True(t, validCategories[tool.Category],
				"tool %q has invalid category %q",
				tool.Id, tool.Category)
		}
	})

	t.Run("AllToolsHaveValidPriority", func(t *testing.T) {
		t.Parallel()

		validPriorities := map[ToolPriority]bool{
			ToolPriorityRecommended: true,
			ToolPriorityOptional:    true,
		}

		tools := BuiltInTools()
		for _, tool := range tools {
			assert.True(t, validPriorities[tool.Priority],
				"tool %q has invalid priority %q",
				tool.Id, tool.Priority)
		}
	})

	t.Run("AllToolsHaveInstallStrategies", func(t *testing.T) {
		t.Parallel()

		tools := BuiltInTools()
		for _, tool := range tools {
			if tool.Category == ToolCategorySkill {
				// Skill tools install via SkillHosts, not InstallStrategies.
				assert.NotEmpty(t, tool.SkillHosts,
					"skill tool %q must have SkillHosts",
					tool.Id)
				continue
			}
			assert.NotEmpty(t, tool.InstallStrategies,
				"tool %q must have InstallStrategies",
				tool.Id)
		}
	})

	t.Run("ReturnsFreshCopy", func(t *testing.T) {
		t.Parallel()

		first := BuiltInTools()
		second := BuiltInTools()

		require.Equal(t, len(first), len(second))

		// Mutating the first slice should not affect the second.
		first[0] = &ToolDefinition{Id: "mutated-tool"}
		assert.NotEqual(t, first[0].Id, second[0].Id,
			"BuiltInTools should return independent copies")
	})
}

func TestFindTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		expectNil bool
		expectId  string
	}{
		{
			name:     "FindsAzCLI",
			id:       "az-cli",
			expectId: "az-cli",
		},
		{
			name:     "FindsGitHubCopilotCLI",
			id:       "github-copilot-cli",
			expectId: "github-copilot-cli",
		},
		{
			name:     "FindsVSCodeExtension",
			id:       "vscode-azure-tools",
			expectId: "vscode-azure-tools",
		},
		{
			name:     "FindsMCPServer",
			id:       "azure-mcp-server",
			expectId: "azure-mcp-server",
		},
		{
			name:     "FindsAzdAIExtensions",
			id:       "azure.ai.agents",
			expectId: "azure.ai.agents",
		},
		{
			name:      "ReturnsNilForUnknownID",
			id:        "nonexistent-tool",
			expectNil: true,
		},
		{
			name:      "ReturnsNilForEmptyID",
			id:        "",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := FindTool(tt.id)
			if tt.expectNil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expectId, result.Id)
			}
		})
	}
}

func TestFindToolsByCategory(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsCLITools", func(t *testing.T) {
		t.Parallel()

		tools := FindToolsByCategory(ToolCategoryCLI)
		require.NotEmpty(t, tools)

		for _, tool := range tools {
			assert.Equal(t, ToolCategoryCLI, tool.Category)
		}

		// Known CLI tools: az-cli, github-copilot-cli
		ids := make([]string, len(tools))
		for i, tool := range tools {
			ids[i] = tool.Id
		}
		assert.Contains(t, ids, "az-cli")
		assert.Contains(t, ids, "github-copilot-cli")
	})

	t.Run("ReturnsExtensionTools", func(t *testing.T) {
		t.Parallel()

		tools := FindToolsByCategory(ToolCategoryVSCodeExtension)
		require.NotEmpty(t, tools)

		for _, tool := range tools {
			assert.Equal(t, ToolCategoryVSCodeExtension, tool.Category)
		}
	})

	t.Run("ReturnsServerTools", func(t *testing.T) {
		t.Parallel()

		tools := FindToolsByCategory(ToolCategoryServer)
		require.NotEmpty(t, tools)

		for _, tool := range tools {
			assert.Equal(t, ToolCategoryServer, tool.Category)
		}
	})

	t.Run("ReturnsLibraryTools", func(t *testing.T) {
		t.Parallel()

		tools := FindToolsByCategory(ToolCategoryAzdExtension)
		require.NotEmpty(t, tools)

		for _, tool := range tools {
			assert.Equal(t, ToolCategoryAzdExtension, tool.Category)
		}
	})

	t.Run("ReturnsEmptyForUnknownCategory", func(t *testing.T) {
		t.Parallel()

		tools := FindToolsByCategory(ToolCategory("bogus"))
		require.Empty(t, tools)
	})

	t.Run("CategoriesSumToTotal", func(t *testing.T) {
		t.Parallel()

		allTools := BuiltInTools()
		cli := FindToolsByCategory(ToolCategoryCLI)
		ext := FindToolsByCategory(ToolCategoryVSCodeExtension)
		srv := FindToolsByCategory(ToolCategoryServer)
		lib := FindToolsByCategory(ToolCategoryAzdExtension)
		skills := FindToolsByCategory(ToolCategorySkill)

		total := len(cli) + len(ext) + len(srv) + len(lib) + len(skills)
		assert.Equal(t, len(allTools), total,
			"sum of categorised tools must equal total")
	})
}

func TestSpecificToolDefinitions(t *testing.T) {
	t.Parallel()

	t.Run("AzCLIHasCorrectFields", func(t *testing.T) {
		t.Parallel()

		tool := FindTool("az-cli")
		require.NotNil(t, tool)

		assert.Equal(t, "Azure CLI", tool.Name)
		assert.Equal(t, ToolCategoryCLI, tool.Category)
		assert.Equal(t, ToolPriorityRecommended, tool.Priority)
		assert.Equal(t, "az", tool.DetectCommand)
		assert.Equal(t, []string{"--version"}, tool.VersionArgs)
		assert.NotEmpty(t, tool.VersionRegex)
		assert.NotEmpty(t, tool.Website)

		_, hasWindows := tool.InstallStrategies["windows"]
		_, hasDarwin := tool.InstallStrategies["darwin"]
		_, hasLinux := tool.InstallStrategies["linux"]
		assert.True(t, hasWindows, "should have windows strategy")
		assert.True(t, hasDarwin, "should have darwin strategy")
		assert.True(t, hasLinux, "should have linux strategy")
	})

	t.Run("AzdAIExtensionsContract", func(t *testing.T) {
		t.Parallel()

		tool := FindTool("azure.ai.agents")
		require.NotNil(t, tool, "azure.ai.agents must be registered")

		assert.Equal(t, "azure.ai.agents", tool.Id,
			"Id must match the JSON id emitted by `azd extension list`")
		assert.Equal(t, ToolCategoryAzdExtension, tool.Category,
			"Category must be AzdExtension so DetectTool routes to detectAzdExtension")
		assert.Equal(t, "azd-extension", string(tool.Category),
			"wire format must remain stable for `azd tool list --output json` consumers")
		assert.Equal(t, "azd", tool.DetectCommand,
			"DetectCommand must be 'azd' for the extension-list probe")
		assert.Equal(t,
			[]string{"extension", "list", "--installed", "--output", "json"},
			tool.VersionArgs,
			"VersionArgs must match the JSON command parsed by detectAzdExtension")
		assert.Empty(t, tool.Dependencies,
			"azd extensions are self-contained; must not depend on az-cli")

		for _, platform := range []string{"windows", "darwin", "linux"} {
			strategies, ok := tool.InstallStrategies[platform]
			require.True(t, ok, "missing install strategy for %s", platform)
			require.Len(t, strategies, 1)
			assert.Contains(t, strategies[0].InstallCommand, "azure.ai.agents",
				"%s install command must target azure.ai.agents", platform)
			assert.Contains(t, strategies[0].InstallCommand, "--source azd",
				"%s install command must pin the azd source", platform)
		}
	})

	t.Run("VSCodeExtensionsUseCodeDetectCommand", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "vscode-extension", string(ToolCategoryVSCodeExtension),
			"wire format must remain stable for `azd tool list --output json` consumers")

		extensions := FindToolsByCategory(ToolCategoryVSCodeExtension)
		for _, ext := range extensions {
			assert.Equal(t, "code", ext.DetectCommand,
				"extension %q should detect via 'code'", ext.Id)
		}
	})
}

func TestAllPlatforms(t *testing.T) {
	t.Parallel()

	strategy := InstallStrategy{
		PackageManager: "npm",
		PackageId:      "@test/pkg",
		InstallCommand: "npm install -g @test/pkg",
	}

	result := allPlatforms(strategy)

	require.Len(t, result, 3)
	for _, os := range []string{"windows", "darwin", "linux"} {
		got, exists := result[os]
		require.True(t, exists, "missing %s", os)
		assert.Equal(t, []InstallStrategy{strategy}, got)
	}

	// Variadic form preserves order across all platforms.
	a := InstallStrategy{PackageManager: "winget", PackageId: "A"}
	b := InstallStrategy{PackageManager: "npm", PackageId: "B"}
	multi := allPlatforms(a, b)
	for _, os := range []string{"windows", "darwin", "linux"} {
		assert.Equal(t, []InstallStrategy{a, b}, multi[os])
	}
}

// TestGithubCopilotCLI_InstallStrategies guards that the per-platform install
// strategies and the derived uninstall commands for the GitHub Copilot CLI
// match the official docs (issue #8831): npm on all platforms, winget on
// Windows, a Homebrew cask on macOS+Linux, and the install script (binary
// removal on uninstall) on macOS+Linux.
func TestGithubCopilotCLI_InstallStrategies(t *testing.T) {
	t.Parallel()

	tool := FindTool("github-copilot-cli")
	require.NotNil(t, tool)

	// helper: does the platform list contain a strategy matching pred?
	has := func(platform string, pred func(InstallStrategy) bool) bool {
		for _, s := range tool.InstallStrategies[platform] {
			if pred(s) {
				return true
			}
		}
		return false
	}
	isWinget := func(s InstallStrategy) bool {
		return s.PackageManager == "winget" && s.PackageId == "GitHub.Copilot"
	}
	isNpm := func(s InstallStrategy) bool {
		return s.PackageManager == "npm" && s.PackageId == "@github/copilot"
	}
	isBrewCask := func(s InstallStrategy) bool {
		return s.PackageManager == "brew" && s.PackageId == "copilot-cli" && s.Cask
	}
	isScript := func(s InstallStrategy) bool {
		return s.PackageManager == "" &&
			strings.Contains(s.InstallCommand, "copilot-install")
	}

	// Windows: winget (preferred) + npm.
	assert.True(t, has("windows", isWinget), "windows winget")
	assert.True(t, has("windows", isNpm), "windows npm")

	// macOS: brew cask + npm + install script.
	assert.True(t, has("darwin", isBrewCask), "darwin brew cask")
	assert.True(t, has("darwin", isNpm), "darwin npm")
	assert.True(t, has("darwin", isScript), "darwin install script")

	// Linux: npm (preferred) + brew cask + install script.
	assert.True(t, has("linux", isNpm), "linux npm")
	assert.True(t, has("linux", isBrewCask), "linux brew cask")
	assert.True(t, has("linux", isScript), "linux install script")

	// The derived uninstall commands match the official documentation.
	_, args := buildUninstallCommand("brew", "copilot-cli", true)
	assert.Equal(t, []string{"uninstall", "--cask", "copilot-cli"}, args)
	_, args = buildUninstallCommand("npm", "@github/copilot", false)
	assert.Equal(t, []string{"uninstall", "-g", "@github/copilot"}, args)
}
