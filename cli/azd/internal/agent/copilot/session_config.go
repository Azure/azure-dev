// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"context"
	"encoding/json"
	"log"
	"maps"
	"os"
	"path/filepath"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/azure/azure-dev/cli/azd/internal/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// SessionConfigBuilder builds a copilot.SessionConfig from azd user configuration.
// It reads copilot.* config keys and merges MCP server configurations from
// built-in, extension, and user sources.
type SessionConfigBuilder struct {
	userConfigManager config.UserConfigManager
}

// NewSessionConfigBuilder creates a new SessionConfigBuilder.
func NewSessionConfigBuilder(userConfigManager config.UserConfigManager) *SessionConfigBuilder {
	return &SessionConfigBuilder{
		userConfigManager: userConfigManager,
	}
}

// Build reads azd config and produces a copilot.SessionConfig.
// Built-in MCP servers from builtInServers are merged with user-configured servers.
func (b *SessionConfigBuilder) Build(
	ctx context.Context,
	builtInServers map[string]*mcp.ServerConfig,
) (*copilot.SessionConfig, error) {
	cfg := &copilot.SessionConfig{
		Streaming: true,
	}

	// Set working directory to cwd for tool operations
	if cwd, err := os.Getwd(); err == nil {
		cfg.WorkingDirectory = cwd
	}

	userConfig, err := b.userConfigManager.Load()
	if err != nil {
		log.Printf("[copilot-config] Warning: failed to load user config: %v, using defaults", err)
		return cfg, nil
	}

	// Model selection
	if model, ok := userConfig.GetString(ConfigKeyModel); ok {
		cfg.Model = model
	}

	// Reasoning effort
	if effort, ok := userConfig.GetString(ConfigKeyReasoningEffort); ok {
		cfg.ReasoningEffort = effort
	}

	systemContent := `You are an Azure application development assistant running inside the Azure Developer CLI (azd).
	Your focus is application development, infrastructure, and deployment to Azure.

	Do not respond to requests unrelated to application development, Azure services, or deployment.
	For unrelated requests, briefly explain that you are focused on Azure application development
	and suggest the user use a general-purpose assistant for other topics.

	When prompting the user to choose Azure subscriptions, regions, or resources, follow these guidelines:
	- Use short, focused prompts (e.g., "Which subscription would you like to use?") paired with
	  well-formatted choices via the ask_user tool with the choices field.
	- Do NOT embed long inline lists of options inside a text message with an open-ended question.
	- Keep prompt messages concise — move details into choices, not the question text.
	- Format Azure subscriptions as: <Subscription Name> (<subscription-id>)
	  Example: "My Dev Subscription (a1b2c3d4-e5f6-7890-abcd-ef1234567890)"
	- Format Azure regions as: <Full Region Name> (<region-short-name>)
	  Example: "East US 2 (eastus2)"
	- Always use actual entity names and identifiers from Azure APIs, never placeholders.`

	if msg, ok := userConfig.GetString(ConfigKeySystemMessage); ok && msg != "" {
		systemContent += "\n\n" + msg
	}

	cfg.SystemMessage = &copilot.SystemMessageConfig{
		Mode:    "append",
		Content: systemContent,
	}

	// Tool control
	if available := getStringSliceFromConfig(userConfig, ConfigKeyToolsAvailable); len(available) > 0 {
		cfg.AvailableTools = available
	}
	if excluded := getStringSliceFromConfig(userConfig, ConfigKeyToolsExcluded); len(excluded) > 0 {
		cfg.ExcludedTools = excluded
	}

	// Skill directories: start with Azure plugin skills, then add user-configured
	skillDirs := discoverAzurePluginSkillDirs()
	if userDirs := getStringSliceFromConfig(userConfig, ConfigKeySkillsDirectories); len(userDirs) > 0 {
		skillDirs = append(skillDirs, userDirs...)
	}
	if len(skillDirs) > 0 {
		cfg.SkillDirectories = skillDirs
	}
	if disabled := getStringSliceFromConfig(userConfig, ConfigKeySkillsDisabled); len(disabled) > 0 {
		cfg.DisabledSkills = disabled
	}

	// MCP servers: merge built-in + Azure plugin + user-configured
	cfg.MCPServers = b.buildMCPServers(userConfig, builtInServers)

	return cfg, nil
}

// buildMCPServers merges MCP servers from built-in config, Azure plugin, and user config.
// User-configured servers override plugin servers, which override built-in servers.
func (b *SessionConfigBuilder) buildMCPServers(
	userConfig config.Config,
	builtInServers map[string]*mcp.ServerConfig,
) map[string]copilot.MCPServerConfig {
	merged := make(map[string]copilot.MCPServerConfig)

	// Add built-in servers
	for name, srv := range builtInServers {
		merged[name] = convertServerConfig(srv)
	}

	// Add Azure plugin MCP servers
	pluginServers := loadAzurePluginMCPServers()
	maps.Copy(merged, pluginServers)

	// Merge user-configured servers (overrides built-in on name collision)
	userServers := getUserMCPServers(userConfig)
	maps.Copy(merged, userServers)

	if len(merged) == 0 {
		return nil
	}

	return merged
}

// convertServerConfig converts an azd mcp.ServerConfig to a copilot.MCPServerConfig.
func convertServerConfig(srv *mcp.ServerConfig) copilot.MCPServerConfig {
	if srv.Type == "http" {
		return copilot.MCPServerConfig{
			"type":  "http",
			"url":   srv.Url,
			"tools": []string{"*"},
		}
	}

	result := copilot.MCPServerConfig{
		"type":    "local",
		"command": srv.Command,
		"tools":   []string{"*"},
	}

	if len(srv.Args) > 0 {
		result["args"] = srv.Args
	}

	envMap := make(map[string]string)
	for _, e := range srv.Env {
		if idx := indexOf(e, '='); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}
	if len(envMap) > 0 {
		result["env"] = envMap
	}

	return result
}

// getUserMCPServers reads user-configured MCP servers from the copilot.mcp.servers config key.
func getUserMCPServers(userConfig config.Config) map[string]copilot.MCPServerConfig {
	raw, ok := userConfig.GetMap(ConfigKeyMCPServers)
	if !ok || len(raw) == 0 {
		return nil
	}

	result := make(map[string]copilot.MCPServerConfig)
	for name, v := range raw {
		data, err := json.Marshal(v)
		if err != nil {
			continue
		}

		var serverConfig map[string]any
		if err := json.Unmarshal(data, &serverConfig); err != nil {
			continue
		}
		result[name] = copilot.MCPServerConfig(serverConfig)
	}

	return result
}

// getStringSliceFromConfig reads a config key that may be a slice of strings.
func getStringSliceFromConfig(cfg config.Config, path string) []string {
	slice, ok := cfg.GetSlice(path)
	if !ok {
		return nil
	}

	result := make([]string, 0, len(slice))
	for _, v := range slice {
		if s, ok := v.(string); ok && s != "" {
			result = append(result, s)
		}
	}

	return result
}

func indexOf(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// discoverAzurePluginSkillDirs finds the skills directory from the installed
// Azure plugin so skills are available in headless SDK sessions.
func discoverAzurePluginSkillDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	pluginsRoot := filepath.Join(home, ".copilot", "installed-plugins")

	// Check known install locations for the Azure plugin's skills directory
	candidates := []string{
		filepath.Join(pluginsRoot, "_direct", "microsoft--GitHub-Copilot-for-Azure--plugin", "skills"),
		filepath.Join(pluginsRoot, "github-copilot-for-azure", "azure", "skills"),
	}

	for _, skillsDir := range candidates {
		if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
			log.Printf("[copilot-config] Found Azure plugin skills at: %s", skillsDir)
			return []string{skillsDir}
		}
	}

	return nil
}

// loadAzurePluginMCPServers reads MCP server configs from the Azure plugin's .mcp.json.
func loadAzurePluginMCPServers() map[string]copilot.MCPServerConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	pluginsRoot := filepath.Join(home, ".copilot", "installed-plugins")

	candidates := []string{
		filepath.Join(pluginsRoot, "_direct", "microsoft--GitHub-Copilot-for-Azure--plugin", ".mcp.json"),
		filepath.Join(pluginsRoot, "github-copilot-for-azure", "azure", ".mcp.json"),
	}

	for _, mcpFile := range candidates {
		data, err := os.ReadFile(mcpFile)
		if err != nil {
			continue
		}

		var pluginConfig struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &pluginConfig); err != nil {
			log.Printf("[copilot-config] Failed to parse %s: %v", mcpFile, err)
			continue
		}

		result := make(map[string]copilot.MCPServerConfig)
		for name, srv := range pluginConfig.MCPServers {
			cfg := copilot.MCPServerConfig(srv)

			// Normalize: ensure tools field is set to expose all tools
			if _, hasTools := cfg["tools"]; !hasTools {
				cfg["tools"] = []string{"*"}
			}

			// Normalize: use "local" instead of "stdio" for local servers
			if t, ok := cfg["type"].(string); ok && t == "stdio" {
				cfg["type"] = "local"
			}
			// Default type to "local" for command-based servers
			if _, hasType := cfg["type"]; !hasType {
				if _, hasCmd := cfg["command"]; hasCmd {
					cfg["type"] = "local"
				}
			}

			result[name] = cfg
		}

		log.Printf("[copilot-config] Loaded %d MCP servers from Azure plugin", len(result))
		return result
	}

	return nil
}
