// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/azure/azure-dev/cli/azd/internal/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// SessionConfigBuilder builds a copilot.SessionConfig from azd user configuration.
// It reads ai.agent.* config keys and merges MCP server configurations from
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
		// Use defaults if config can't be loaded
		return cfg, nil
	}

	// Model selection
	if model, ok := userConfig.GetString("ai.agent.model"); ok {
		cfg.Model = model
	}

	// Reasoning effort
	if effort, ok := userConfig.GetString("ai.agent.reasoningEffort"); ok {
		cfg.ReasoningEffort = effort
	}

	// System message — temporarily disabled for debugging
	// TODO: Re-enable after MCP integration is verified
	// systemContent := `You are an Azure application development assistant running inside the Azure Developer CLI (azd).
	// Your focus is application development, infrastructure, and deployment to Azure.
	//
	// Do not respond to requests unrelated to application development, Azure services, or deployment.
	// For unrelated requests, briefly explain that you are focused on Azure application development
	// and suggest the user use a general-purpose assistant for other topics.`
	//
	// if msg, ok := userConfig.GetString("ai.agent.systemMessage"); ok && msg != "" {
	// 	systemContent += "\n\n" + msg
	// }
	//
	// cfg.SystemMessage = &copilot.SystemMessageConfig{
	// 	Mode:    "append",
	// 	Content: systemContent,
	// }

	// Tool control
	if available := getStringSliceFromConfig(userConfig, "ai.agent.tools.available"); len(available) > 0 {
		cfg.AvailableTools = available
	}
	if excluded := getStringSliceFromConfig(userConfig, "ai.agent.tools.excluded"); len(excluded) > 0 {
		cfg.ExcludedTools = excluded
	}

	// Skill directories: start with Azure plugin skills, then add user-configured
	skillDirs := discoverAzurePluginSkillDirs()
	if userDirs := getStringSliceFromConfig(userConfig, "ai.agent.skills.directories"); len(userDirs) > 0 {
		skillDirs = append(skillDirs, userDirs...)
	}
	if len(skillDirs) > 0 {
		cfg.SkillDirectories = skillDirs
	}
	if disabled := getStringSliceFromConfig(userConfig, "ai.agent.skills.disabled"); len(disabled) > 0 {
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
	for name, srv := range pluginServers {
		merged[name] = srv
	}

	// Merge user-configured servers (overrides built-in on name collision)
	userServers := getUserMCPServers(userConfig)
	for name, srv := range userServers {
		merged[name] = srv
	}

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

// getUserMCPServers reads user-configured MCP servers from the ai.agent.mcp.servers config key.
func getUserMCPServers(userConfig config.Config) map[string]copilot.MCPServerConfig {
	raw, ok := userConfig.GetMap("ai.agent.mcp.servers")
	if !ok || len(raw) == 0 {
		return nil
	}

	result := make(map[string]copilot.MCPServerConfig)
	for name, v := range raw {
		// Marshal/unmarshal each server entry to get typed config
		data, err := json.Marshal(v)
		if err != nil {
			continue
		}

		// Try to detect type field first
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			continue
		}

		if probe.Type == "http" {
			var remote map[string]any
			if err := json.Unmarshal(data, &remote); err != nil {
				continue
			}
			result[name] = copilot.MCPServerConfig(remote)
		} else {
			var local map[string]any
			if err := json.Unmarshal(data, &local); err != nil {
				continue
			}
			result[name] = copilot.MCPServerConfig(local)
		}
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
