// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	// ConfigFile is the project-level state file for local agent context.
	ConfigFile = ".foundry-agent.json"

	// DefaultPort is the default port for local agent servers.
	DefaultPort = 8088
)

// AgentLocalContext holds local state persisted in .foundry-agent.json.
type AgentLocalContext struct {
	AgentName    string                       `json:"agent_name,omitempty"`
	Sessions     map[string]string            `json:"sessions,omitempty"`
	Conversations map[string]string           `json:"conversations,omitempty"`
}

// loadLocalContext reads the .foundry-agent.json state file from the project root.
func loadLocalContext() *AgentLocalContext {
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		return &AgentLocalContext{}
	}
	var ctx AgentLocalContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return &AgentLocalContext{}
	}
	return &ctx
}

// saveLocalContext writes the .foundry-agent.json state file.
func saveLocalContext(ctx *AgentLocalContext) error {
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal local context: %w", err)
	}
	return os.WriteFile(ConfigFile, append(data, '\n'), 0644)
}

// resolveAgentNameLocal resolves the agent name from: explicit flag > .foundry-agent.json > error.
func resolveAgentNameLocal(name string) (string, error) {
	if name != "" {
		return name, nil
	}
	ctx := loadLocalContext()
	if ctx.AgentName != "" {
		return ctx.AgentName, nil
	}
	return "", fmt.Errorf("no agent name specified; use --name or run 'azd ai agent init' first")
}

// resolveSessionID resolves or generates a session ID for invoke.
// Returns the session ID (existing or newly generated).
func resolveSessionID(agentName string, explicit string, forceNew bool) string {
	if explicit != "" {
		return explicit
	}
	ctx := loadLocalContext()
	if ctx.Sessions == nil {
		ctx.Sessions = make(map[string]string)
	}
	if !forceNew {
		if sid, ok := ctx.Sessions[agentName]; ok {
			return sid
		}
	}
	sid := generateSessionID()
	ctx.Sessions[agentName] = sid
	_ = saveLocalContext(ctx)
	return sid
}

// resolveConversationID resolves or creates a Foundry conversation ID.
// Returns empty string if creation fails (multi-turn memory disabled).
func resolveConversationID(agentName string, forceNew bool) string {
	ctx := loadLocalContext()
	if ctx.Conversations == nil {
		ctx.Conversations = make(map[string]string)
	}
	if !forceNew {
		if convID, ok := ctx.Conversations[agentName]; ok {
			return convID
		}
	}
	// Conversation creation requires an API call — handled by the invoke command.
	return ""
}

// saveConversationID persists a conversation ID for an agent.
func saveConversationID(agentName, convID string) {
	ctx := loadLocalContext()
	if ctx.Conversations == nil {
		ctx.Conversations = make(map[string]string)
	}
	ctx.Conversations[agentName] = convID
	_ = saveLocalContext(ctx)
}

// generateSessionID creates a random 25-character session ID (lowercase + digits).
func generateSessionID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 25)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			panic(fmt.Sprintf("crypto/rand failed: %v", err))
		}
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

// detectProjectType detects the project type and suggests a start command.
type ProjectType struct {
	Language string // "python", "dotnet", "node", "unknown"
	StartCmd string // suggested start command
}

func detectProjectType(projectDir string) ProjectType {
	// Python: pyproject.toml or requirements.txt
	if fileExists(filepath.Join(projectDir, "pyproject.toml")) {
		return ProjectType{Language: "python", StartCmd: "python main.py"}
	}
	if fileExists(filepath.Join(projectDir, "requirements.txt")) {
		return ProjectType{Language: "python", StartCmd: "python main.py"}
	}

	// .NET: any .csproj file
	matches, _ := filepath.Glob(filepath.Join(projectDir, "*.csproj"))
	if len(matches) > 0 {
		return ProjectType{Language: "dotnet", StartCmd: "dotnet run"}
	}

	// Node.js: package.json
	if fileExists(filepath.Join(projectDir, "package.json")) {
		return ProjectType{Language: "node", StartCmd: "npm start"}
	}

	// Check for main.py as fallback
	if fileExists(filepath.Join(projectDir, "main.py")) {
		return ProjectType{Language: "python", StartCmd: "python main.py"}
	}

	return ProjectType{Language: "unknown", StartCmd: ""}
}

// parseEndpoint extracts account and project names from a Foundry project endpoint URL.
// e.g., "https://myaccount.services.ai.azure.com/api/projects/myproject" → ("myaccount", "myproject")
func parseEndpoint(endpoint string) (account, project string, err error) {
	endpoint = strings.TrimRight(endpoint, "/")
	// Extract account from hostname
	if !strings.Contains(endpoint, "://") {
		return "", "", fmt.Errorf("invalid endpoint URL: %s", endpoint)
	}
	hostPath := strings.SplitN(endpoint, "://", 2)[1]
	hostParts := strings.SplitN(hostPath, "/", 2)
	hostname := hostParts[0]
	account = strings.SplitN(hostname, ".", 2)[0]

	// Extract project from path
	pathParts := strings.Split(endpoint, "/")
	if len(pathParts) > 0 {
		project = pathParts[len(pathParts)-1]
	}
	if account == "" || project == "" {
		return "", "", fmt.Errorf("could not parse account/project from endpoint: %s", endpoint)
	}
	return account, project, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// AgentServiceInfo holds the resolved name and version for an agent service.
type AgentServiceInfo struct {
	ServiceName string // azure.yaml service key
	AgentName   string // deployed agent name from env
	Version     string // deployed agent version from env
}

// resolveAgentServiceFromProject finds the first azure.ai.agent service in azure.yaml
// and resolves its deployed agent name and version from the azd environment.
// The name parameter filters to a specific service; empty means use the first one found.
func resolveAgentServiceFromProject(ctx context.Context, name string) (*AgentServiceInfo, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || projectResponse.Project == nil {
		return nil, fmt.Errorf("failed to get project config: %w", err)
	}

	// Find the matching azure.ai.agent service
	var svc *azdext.ServiceConfig
	for _, s := range projectResponse.Project.Services {
		if s.Host != AiAgentHost {
			continue
		}
		if name != "" && s.Name != name {
			continue
		}
		svc = s
		break
	}

	if svc == nil {
		if name != "" {
			return nil, fmt.Errorf("no azure.ai.agent service named '%s' found in azure.yaml", name)
		}
		return nil, fmt.Errorf("no azure.ai.agent service found in azure.yaml")
	}

	info := &AgentServiceInfo{ServiceName: svc.Name}

	// Resolve agent name and version from azd environment
	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return info, nil
	}

	serviceKey := toServiceKey(svc.Name)
	nameKey := fmt.Sprintf("AGENT_%s_NAME", serviceKey)
	versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)

	if v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     nameKey,
	}); err == nil && v.Value != "" {
		info.AgentName = v.Value
	}

	if v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     versionKey,
	}); err == nil && v.Value != "" {
		info.Version = v.Value
	}

	return info, nil
}

// resolveStartupCommandFromService reads startupCommand from an azure.ai.agent
// service's AdditionalProperties. Returns empty string if unavailable.
func resolveStartupCommandFromService(ctx context.Context, name string) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || projectResponse.Project == nil {
		return ""
	}

	for _, s := range projectResponse.Project.Services {
		if s.Host != AiAgentHost {
			continue
		}
		if name != "" && s.Name != name {
			continue
		}
		if s.AdditionalProperties == nil {
			return ""
		}
		fields := s.AdditionalProperties.GetFields()
		if fields == nil {
			return ""
		}
		v, ok := fields["startupCommand"]
		if !ok || v == nil {
			return ""
		}
		return v.GetStringValue()
	}

	return ""
}

// toServiceKey converts a service name into the env var key format (uppercase, underscores).
func toServiceKey(serviceName string) string {
	key := strings.ReplaceAll(serviceName, " ", "_")
	key = strings.ReplaceAll(key, "-", "_")
	return strings.ToUpper(key)
}
