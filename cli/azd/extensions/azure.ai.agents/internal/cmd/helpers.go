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

	"azureaiagent/internal/exterrors"
	projectpkg "azureaiagent/internal/project"

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
	AgentName     string            `json:"agent_name,omitempty"`
	Sessions      map[string]string `json:"sessions,omitempty"`
	Conversations map[string]string `json:"conversations,omitempty"`
}

// resolveConfigPath returns the full path to the .foundry-agent.json file
// in the current azd environment directory (<project root>/.azure/<env name>/).
func resolveConfigPath(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", fmt.Errorf("failed to get project config: %w", err)
	}

	if projectResponse.Project == nil {
		return "", fmt.Errorf("failed to get project config (is there an azure.yaml?)")
	}

	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", fmt.Errorf("failed to get current environment: %w", err)
	}
	if envResponse.Environment == nil || envResponse.Environment.Name == "" {
		return "", fmt.Errorf("no current environment set; run 'azd env select' or 'azd init' first")
	}

	return filepath.Join(projectResponse.Project.Path, ".azure", envResponse.Environment.Name, ConfigFile), nil
}

// loadLocalContext reads the .foundry-agent.json state file.
// configPath is the full path to the config file (use resolveConfigPath to obtain it).
func loadLocalContext(configPath string) *AgentLocalContext {
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: configPath is resolved from azd project root, not user input
	if err != nil {
		return &AgentLocalContext{}
	}
	var agentCtx AgentLocalContext
	if err := json.Unmarshal(data, &agentCtx); err != nil {
		return &AgentLocalContext{}
	}
	return &agentCtx
}

// saveLocalContext writes the .foundry-agent.json state file.
// configPath is the full path to the config file (use resolveConfigPath to obtain it).
func saveLocalContext(agentCtx *AgentLocalContext, configPath string) error {
	data, err := json.MarshalIndent(agentCtx, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal local context: %w", err)
	}
	return os.WriteFile(configPath, append(data, '\n'), 0600)
}

// resolveSessionID resolves or generates a session ID for invoke.
// Returns the session ID (existing or newly generated).
func resolveSessionID(ctx context.Context, azdClient *azdext.AzdClient, agentName string, explicit string, forceNew bool) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return "", err
	}
	agentCtx := loadLocalContext(configPath)
	if agentCtx.Sessions == nil {
		agentCtx.Sessions = make(map[string]string)
	}
	if !forceNew {
		if sid, ok := agentCtx.Sessions[agentName]; ok {
			return sid, nil
		}
	}
	sid := generateSessionID()
	agentCtx.Sessions[agentName] = sid
	if err := saveLocalContext(agentCtx, configPath); err != nil {
		return sid, fmt.Errorf("failed to save session ID: %w", err)
	}
	return sid, nil
}

// resolveConversationID resolves or creates a Foundry conversation ID.
// Returns empty string if no existing conversation is found or on error.
func resolveConversationID(ctx context.Context, azdClient *azdext.AzdClient, agentName string, forceNew bool) (string, error) {
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return "", err
	}
	agentCtx := loadLocalContext(configPath)
	if agentCtx.Conversations == nil {
		agentCtx.Conversations = make(map[string]string)
	}
	if !forceNew {
		if convID, ok := agentCtx.Conversations[agentName]; ok {
			return convID, nil
		}
	}
	// Conversation creation requires an API call — handled by the invoke command.
	return "", nil
}

// saveConversationID persists a conversation ID for an agent.
func saveConversationID(ctx context.Context, azdClient *azdext.AzdClient, agentName, convID string) error {
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return err
	}
	agentCtx := loadLocalContext(configPath)
	if agentCtx.Conversations == nil {
		agentCtx.Conversations = make(map[string]string)
	}
	agentCtx.Conversations[agentName] = convID
	if err := saveLocalContext(agentCtx, configPath); err != nil {
		return fmt.Errorf("failed to save conversation ID: %w", err)
	}
	return nil
}

// generateSessionID creates a random 25-character session ID (lowercase + digits).
func generateSessionID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 25)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			// Fail gracefully instead of panicking so the CLI can surface a useful message.
			fmt.Fprintf(os.Stderr, "failed to generate secure session ID: %v\n", err)
			return ""
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
	if fileExists(filepath.Join(projectDir, "pyproject.toml")) ||
		fileExists(filepath.Join(projectDir, "requirements.txt")) {
		if fileExists(filepath.Join(projectDir, "main.py")) {
			return ProjectType{Language: "python", StartCmd: "python main.py"}
		}
		return ProjectType{Language: "python", StartCmd: ""}
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

	// Check for standalone main.py as fallback
	if fileExists(filepath.Join(projectDir, "main.py")) {
		return ProjectType{Language: "python", StartCmd: "python main.py"}
	}

	return ProjectType{Language: "unknown", StartCmd: ""}
}

// detectStartupCommand returns the suggested start command for the project
// in projectDir, or an empty string if the project type is unknown.
func detectStartupCommand(projectDir string) string {
	return detectProjectType(projectDir).StartCmd
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

// promptForAgentService prompts the user to select one of multiple azure.ai.agent services.
// In no-prompt mode it returns an error listing the available services.
func promptForAgentService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	services []*azdext.ServiceConfig,
	noPrompt bool,
) (*azdext.ServiceConfig, error) {
	if noPrompt {
		names := make([]string, len(services))
		for i, s := range services {
			names[i] = s.Name
		}
		return nil, fmt.Errorf(
			"multiple azure.ai.agent services found in azure.yaml: %s\n\n"+
				"Provide the service name as a positional argument to specify which one to use",
			strings.Join(names, ", "),
		)
	}

	choices := make([]*azdext.SelectChoice, len(services))
	for i, s := range services {
		choices[i] = &azdext.SelectChoice{
			Label: s.Name,
			Value: s.Name,
		}
	}

	defaultIndex := int32(0)
	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select an agent service",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, fmt.Errorf("selection cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for service selection: %w", err)
	}

	return services[int(*resp.Value)], nil
}

// resolveAgentService finds an azure.ai.agent service from the project configuration.
// When name is provided, it filters to that specific service.
// When name is empty with a single service, that service is returned automatically.
// When name is empty with multiple services, it prompts the user to select one
// (or returns an error in no-prompt mode).
func resolveAgentService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
) (*azdext.ServiceConfig, *azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get project config (is there an azure.yaml?): %w", err)
	}
	if projectResponse.Project == nil {
		return nil, nil, fmt.Errorf("failed to get project config (is there an azure.yaml?)")
	}

	var svc *azdext.ServiceConfig

	if name != "" {
		for _, s := range projectResponse.Project.Services {
			if s.Host == AiAgentHost && s.Name == name {
				svc = s
				break
			}
		}
		if svc == nil {
			return nil, nil, fmt.Errorf("no azure.ai.agent service named '%s' found in azure.yaml", name)
		}
	} else {
		var agentServices []*azdext.ServiceConfig
		for _, s := range projectResponse.Project.Services {
			if s.Host == AiAgentHost {
				agentServices = append(agentServices, s)
			}
		}

		switch len(agentServices) {
		case 0:
			return nil, nil, fmt.Errorf("no azure.ai.agent service found in azure.yaml")
		case 1:
			svc = agentServices[0]
		default:
			selected, err := promptForAgentService(ctx, azdClient, agentServices, noPrompt)
			if err != nil {
				return nil, nil, err
			}
			svc = selected
		}
	}

	return svc, projectResponse.Project, nil
}

// resolveAgentServiceFromProject finds the azure.ai.agent service in azure.yaml
// and resolves its deployed agent name and version from the azd environment.
func resolveAgentServiceFromProject(ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool) (*AgentServiceInfo, error) {
	svc, _, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return nil, err
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

// ServiceRunContext holds the resolved context needed for local development.
type ServiceRunContext struct {
	ProjectDir     string // absolute path to the service source directory
	StartupCommand string // startupCommand from AdditionalProperties (may be empty)
}

// resolveServiceRunContext queries the azd project to find the matching azure.ai.agent
// service, then returns the service's absolute source directory and startup command.
func resolveServiceRunContext(ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool) (*ServiceRunContext, error) {
	svc, project, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return nil, err
	}

	projectDir := filepath.Join(project.Path, svc.RelativePath)

	var startupCmd string
	if svc.Config != nil {
		var agentConfig projectpkg.ServiceTargetAgentConfig
		if err := projectpkg.UnmarshalStruct(svc.Config, &agentConfig); err == nil {
			startupCmd = agentConfig.StartupCommand
		}
	}

	return &ServiceRunContext{
		ProjectDir:     projectDir,
		StartupCommand: startupCmd,
	}, nil
}

// toServiceKey converts a service name into the env var key format (uppercase, underscores).
func toServiceKey(serviceName string) string {
	key := strings.ReplaceAll(serviceName, " ", "_")
	key = strings.ReplaceAll(key, "-", "_")
	return strings.ToUpper(key)
}

// resolveStartupCommandForInit detects the startup command from the project source directory.
// If detection fails and noPrompt is false, it prompts the user via the azdClient.
// Returns empty string if the user skips the prompt or if running in no-prompt mode.
func resolveStartupCommandForInit(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectPath string,
	targetDir string,
	noPrompt bool,
) (string, error) {
	absDir := targetDir
	if !filepath.IsAbs(targetDir) && projectPath != "" {
		absDir = filepath.Join(projectPath, targetDir)
	}

	if cmd := detectStartupCommand(absDir); cmd != "" {
		return cmd, nil
	}

	if noPrompt {
		return "", nil
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Enter the command to start your agent (e.g., python main.py), or leave blank to skip",
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", nil
		}
		return "", fmt.Errorf("prompting for startup command: %w", err)
	}

	return strings.TrimSpace(resp.Value), nil
}
