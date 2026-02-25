// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"encoding/json"

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

	userConfig, err := b.userConfigManager.Load()
	if err != nil {
		// Use defaults if config can't be loaded
		return cfg, nil
	}

	// Model selection
	if model, ok := userConfig.GetString("ai.agent.model"); ok {
		cfg.Model = model
	}

	// System message — use "append" mode to add to default prompt
	if msg, ok := userConfig.GetString("ai.agent.systemMessage"); ok && msg != "" {
		cfg.SystemMessage = &copilot.SystemMessageConfig{
			Mode:    "append",
			Content: msg,
		}
	}

	// Tool control
	if available := getStringSliceFromConfig(userConfig, "ai.agent.tools.available"); len(available) > 0 {
		cfg.AvailableTools = available
	}
	if excluded := getStringSliceFromConfig(userConfig, "ai.agent.tools.excluded"); len(excluded) > 0 {
		cfg.ExcludedTools = excluded
	}

	// Skill control
	if dirs := getStringSliceFromConfig(userConfig, "ai.agent.skills.directories"); len(dirs) > 0 {
		cfg.SkillDirectories = dirs
	}
	if disabled := getStringSliceFromConfig(userConfig, "ai.agent.skills.disabled"); len(disabled) > 0 {
		cfg.DisabledSkills = disabled
	}

	// MCP servers: merge built-in + user-configured
	cfg.MCPServers = b.buildMCPServers(userConfig, builtInServers)

	return cfg, nil
}

// buildMCPServers merges built-in MCP servers with user-configured ones.
// User-configured servers with matching names override built-in servers.
func (b *SessionConfigBuilder) buildMCPServers(
	userConfig config.Config,
	builtInServers map[string]*mcp.ServerConfig,
) map[string]copilot.MCPServerConfig {
	merged := make(map[string]copilot.MCPServerConfig)

	// Add built-in servers
	for name, srv := range builtInServers {
		merged[name] = convertServerConfig(srv)
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
			"type": "http",
			"url":  srv.Url,
		}
	}

	result := copilot.MCPServerConfig{
		"type":    "stdio",
		"command": srv.Command,
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
