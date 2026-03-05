// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	// DefaultPort is the default port for local agent servers.
	DefaultPort = 8088

	// Environment variable keys for invoke state
	EnvKeyAgentInvokeName           = "AZD_AI_AGENT_INVOKE_NAME"
	EnvKeyAgentInvokeSessionID      = "AZD_AI_AGENT_INVOKE_SESSIONID"
	EnvKeyAgentInvokeConversationID = "AZD_AI_AGENT_INVOKE_CONVERSATIONID"
)

// getInvokeEnvValue reads an invoke state env var from the current azd environment.
func getInvokeEnvValue(ctx context.Context, key string) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return ""
	}

	val, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     key,
	})
	if err != nil || val.Value == "" {
		return ""
	}
	return val.Value
}

// setInvokeEnvValue writes an invoke state env var to the current azd environment.
func setInvokeEnvValue(ctx context.Context, key, value string) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer azdClient.Close()

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return err
	}

	_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     key,
		Value:   value,
	})
	return err
}

// resolveSessionID resolves or generates a session ID for invoke.
// State is stored in the azd environment via AZD_AI_AGENT_INVOKE_SESSIONID.
func resolveSessionID(ctx context.Context, agentName string, explicit string, forceNew bool) string {
	if explicit != "" {
		return explicit
	}

	currentName := getInvokeEnvValue(ctx, EnvKeyAgentInvokeName)
	if !forceNew && currentName == agentName {
		if sid := getInvokeEnvValue(ctx, EnvKeyAgentInvokeSessionID); sid != "" {
			return sid
		}
	}

	sid := generateSessionID()
	_ = setInvokeEnvValue(ctx, EnvKeyAgentInvokeName, agentName)
	_ = setInvokeEnvValue(ctx, EnvKeyAgentInvokeSessionID, sid)
	return sid
}

// resolveConversationID resolves the conversation ID from the azd environment.
// Returns empty string if no conversation exists or the agent changed.
func resolveConversationID(ctx context.Context, agentName string, forceNew bool) string {
	if forceNew {
		return ""
	}

	currentName := getInvokeEnvValue(ctx, EnvKeyAgentInvokeName)
	if currentName != agentName {
		return ""
	}

	return getInvokeEnvValue(ctx, EnvKeyAgentInvokeConversationID)
}

// saveConversationID persists a conversation ID in the azd environment.
func saveConversationID(ctx context.Context, agentName, convID string) {
	_ = setInvokeEnvValue(ctx, EnvKeyAgentInvokeName, agentName)
	_ = setInvokeEnvValue(ctx, EnvKeyAgentInvokeConversationID, convID)
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
	// Detect Python entrypoint
	pyEntry := detectPythonEntrypoint(projectDir)

	// Python with pyproject.toml → uv-managed project
	if fileExists(filepath.Join(projectDir, "pyproject.toml")) {
		cmd := "uv run python main.py"
		if pyEntry != "" {
			cmd = "uv run python " + pyEntry
		}
		return ProjectType{Language: "python", StartCmd: cmd}
	}

	// Python with requirements.txt
	if fileExists(filepath.Join(projectDir, "requirements.txt")) {
		cmd := "python main.py"
		if pyEntry != "" {
			cmd = "python " + pyEntry
		}
		return ProjectType{Language: "python", StartCmd: cmd}
	}

	// .NET: find .csproj file and use its name
	matches, _ := filepath.Glob(filepath.Join(projectDir, "*.csproj"))
	if len(matches) > 0 {
		csprojName := filepath.Base(matches[0])
		return ProjectType{Language: "dotnet", StartCmd: "dotnet " + csprojName}
	}

	// Node.js: package.json
	if fileExists(filepath.Join(projectDir, "package.json")) {
		return ProjectType{Language: "node", StartCmd: "npm start"}
	}

	// Bare Python entrypoint as fallback
	if pyEntry != "" {
		return ProjectType{Language: "python", StartCmd: "python " + pyEntry}
	}

	return ProjectType{Language: "unknown", StartCmd: ""}
}

// detectPythonEntrypoint returns the name of the Python entrypoint file found in projectDir.
func detectPythonEntrypoint(projectDir string) string {
	for _, name := range []string{"main.py", "app.py"} {
		if fileExists(filepath.Join(projectDir, name)) {
			return name
		}
	}
	return ""
}

// promptStartupCommand detects the project startup command and prompts the user to confirm or override.
// If flagValue is set (from --startup-command), it is returned directly.
// If noPrompt is true, the auto-detected value is returned without prompting.
func promptStartupCommand(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectDir string,
	flagValue string,
	noPrompt bool,
) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}

	detected := detectProjectType(projectDir)
	defaultCmd := detected.StartCmd

	if noPrompt {
		return defaultCmd, nil
	}

	message := "Enter startup command for the agent"
	if detected.Language != "unknown" && detected.Language != "" {
		message = fmt.Sprintf("Enter startup command for the agent (detected %s project)", detected.Language)
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        message,
			DefaultValue:   defaultCmd,
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		return "", fmt.Errorf("prompting for startup command: %w", err)
	}

	return resp.Value, nil
}

// resolveProjectDir resolves a relative targetDir to an absolute path using the azd project root.
func resolveProjectDir(ctx context.Context, azdClient *azdext.AzdClient, targetDir string) (string, error) {
	if filepath.IsAbs(targetDir) {
		return targetDir, nil
	}

	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", fmt.Errorf("getting project path: %w", err)
	}

	return filepath.Join(projectResponse.Project.Path, targetDir), nil
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

// AgentServiceInfo holds the resolved name for an agent service.
type AgentServiceInfo struct {
	ServiceName string // azure.yaml service key
	AgentName   string // agent name (same as service name)
}

// resolveAgentServiceFromProject finds the first azure.ai.agent service in azure.yaml.
// The name parameter filters to a specific service; empty means use the first one found.
// The service name from azure.yaml is used directly as the agent name.
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

	return &AgentServiceInfo{
		ServiceName: svc.Name,
		AgentName:   svc.Name,
	}, nil
}

// resolveLatestVersion fetches the latest deployed version of an agent via the API.
func resolveLatestVersion(ctx context.Context, accountName, projectName, agentName string) (string, error) {
	endpoint, err := resolveAgentEndpoint(ctx, accountName, projectName)
	if err != nil {
		return "", err
	}

	credential, err := newAgentCredential()
	if err != nil {
		return "", err
	}

	client := agent_api.NewAgentClient(endpoint, credential)
	agent, err := client.GetAgent(ctx, agentName, DefaultAgentAPIVersion)
	if err != nil {
		return "", fmt.Errorf("failed to get agent '%s': %w", agentName, err)
	}

	version := agent.Versions.Latest.Version
	if version == "" {
		return "", fmt.Errorf("agent '%s' has no deployed versions", agentName)
	}

	return version, nil
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
