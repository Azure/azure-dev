// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
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
	Invocations   map[string]string `json:"invocations,omitempty"`
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

// saveContextValue persists a value into the named field of the local config.
// storeField selects the map: "sessions", "conversations", or "invocations".
func saveContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	value string,
	storeField string,
) {
	if value == "" {
		return
	}
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		log.Printf("saveContextValue: failed to resolve config path: %v", err)
		return
	}
	if err := saveContextValueToPath(configPath, agentKey, value, storeField); err != nil {
		log.Printf("saveContextValue: failed to persist %s for %q: %v", storeField, agentKey, err)
	}
}

// saveContextValueToPath persists a value into the named field of the local config at configPath.
// This is the path-based implementation used by saveContextValue and directly in tests.
func saveContextValueToPath(configPath, agentKey, value, storeField string) error {
	agentCtx := loadLocalContext(configPath)
	store := contextMap(agentCtx, storeField)
	store[agentKey] = value
	return saveLocalContext(agentCtx, configPath)
}

// resolveLocalAgentKey builds the storage key for local mode from the azd project config.
// Returns "{serviceName}-local" when the service can be resolved, or "local" as fallback.
func resolveLocalAgentKey(ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool) string {
	if azdClient == nil {
		return "local"
	}
	info, err := resolveAgentServiceFromProject(ctx, azdClient, name, noPrompt)
	if err != nil || info.ServiceName == "" {
		return "local"
	}
	return info.ServiceName + "-local"
}

// resolveStoredID resolves a persisted ID (session, conversation, etc.) from the local config.
// It checks (in order): explicit flag value, persisted value in the config store, then either
// returns "" (generateIfMissing=false, for remote mode where the server assigns the ID) or
// generates and persists a new UUID (generateIfMissing=true, for local mode).
// storeField selects which map to use: "sessions" or "conversations".
func resolveStoredID(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	explicit string,
	forceNew bool,
	storeField string,
	generateIfMissing bool,
) (string, error) {
	if explicit != "" {
		// Persist the explicit ID so that subsequent commands (e.g. monitor, invoke)
		// can find it without the user passing it again.
		persistExplicitID(ctx, azdClient, agentKey, explicit, storeField)
		return explicit, nil
	}
	if forceNew && !generateIfMissing {
		return "", nil
	}

	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		if generateIfMissing {
			return uuid.NewString(), nil
		}
		return "", err
	}

	agentCtx := loadLocalContext(configPath)

	store := contextMap(agentCtx, storeField)
	if !forceNew {
		if id, ok := store[agentKey]; ok {
			return id, nil
		}
	}

	if !generateIfMissing {
		return "", nil
	}

	newID := uuid.NewString()
	store[agentKey] = newID
	_ = saveLocalContext(agentCtx, configPath)

	return newID, nil
}

// persistExplicitID saves a user-provided explicit ID to the local config.
// Errors are logged for debug visibility but do not block the caller.
func persistExplicitID(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	value string,
	storeField string,
) {
	saveContextValue(ctx, azdClient, agentKey, value, storeField)
}

// resolveStoredIDFromPath is a testable variant of resolveStoredID that operates on a
// config file path directly, removing the need for an azdClient.
func resolveStoredIDFromPath(
	configPath string,
	agentKey string,
	explicit string,
	forceNew bool,
	storeField string,
	generateIfMissing bool,
) (string, error) {
	if explicit != "" {
		if err := saveContextValueToPath(configPath, agentKey, explicit, storeField); err != nil {
			log.Printf("resolveStoredIDFromPath: failed to persist explicit %s for %q: %v", storeField, agentKey, err)
		}
		return explicit, nil
	}
	if forceNew && !generateIfMissing {
		return "", nil
	}

	agentCtx := loadLocalContext(configPath)
	store := contextMap(agentCtx, storeField)
	if !forceNew {
		if id, ok := store[agentKey]; ok {
			return id, nil
		}
	}

	if !generateIfMissing {
		return "", nil
	}

	newID := uuid.NewString()
	store[agentKey] = newID
	_ = saveLocalContext(agentCtx, configPath)

	return newID, nil
}
func contextMap(agentCtx *AgentLocalContext, field string) map[string]string {
	switch field {
	case "sessions":
		if agentCtx.Sessions == nil {
			agentCtx.Sessions = make(map[string]string)
		}
		return agentCtx.Sessions
	case "conversations":
		if agentCtx.Conversations == nil {
			agentCtx.Conversations = make(map[string]string)
		}
		return agentCtx.Conversations
	case "invocations":
		if agentCtx.Invocations == nil {
			agentCtx.Invocations = make(map[string]string)
		}
		return agentCtx.Invocations
	default:
		return make(map[string]string)
	}
}

// printSessionStatus prints the session line for the invoke banner.
// label is the formatted prefix (e.g. "Session:  " or "Session:      ").
func printSessionStatus(label, sid string) {
	if sid != "" {
		fmt.Printf("%s%s\n", label, sid)
	} else {
		fmt.Printf("%s(new — server will assign)\n", label)
	}
}

// captureResponseSession reads the x-agent-session-id header from a response
// and saves it when the caller had no pre-existing session (sid == "").
// label is the formatted prefix for printing (e.g. "Session:  ").
func captureResponseSession(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentName string,
	sid string,
	resp *http.Response,
	label string,
) {
	if sid != "" || azdClient == nil {
		return
	}
	if newSid := resp.Header.Get("x-agent-session-id"); newSid != "" {
		saveContextValue(ctx, azdClient, agentName, newSid, "sessions")
		fmt.Printf("%s%s (assigned by server)\n", label, newSid)
	}
}

// fetchOpenAPISpec fetches the OpenAPI spec from a running agent and caches it on disk.
// baseURL is the root URL (e.g., "http://localhost:8088" or "{endpoint}/agents/{name}/endpoint/protocols").
// suffix is "local" or "remote", used in the cached filename.
// If forceRefresh is false and the file already exists, the fetch is skipped.
// Failures are non-fatal and silently ignored.
func fetchOpenAPISpec(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	baseURL string,
	agentName string,
	suffix string,
	bearerToken string,
	forceRefresh bool,
) {
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return
	}
	configDir := filepath.Dir(configPath)

	// Sanitize agentName to prevent path traversal in the cached filename.
	safeName := strings.ReplaceAll(agentName, "..", "_")
	safeName = strings.ReplaceAll(safeName, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")

	specFile := filepath.Join(configDir, fmt.Sprintf("openapi-%s-%s.json", safeName, suffix))

	if !forceRefresh {
		if _, err := os.Stat(specFile); err == nil {
			return // file exists, skip fetch
		}
	}

	specURL := baseURL + "/invocations/docs/openapi.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // G704: URL constructed from azd environment or localhost
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	if err := os.WriteFile(specFile, body, 0600); err != nil {
		return
	}

	fmt.Printf("OpenAPI spec saved to %s\n", specFile)
}

// resolveConversationID resolves a Foundry conversation ID.
// When explicit is provided, it is persisted and returned directly.
// If no conversation is found (or forceNew is true), it attempts to create one and persist it.
// Returns empty string when conversation creation fails, allowing invoke to continue without multi-turn memory.
func resolveConversationID(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentName string,
	explicit string,
	forceNew bool,
	endpoint string,
	bearerToken string,
) (string, error) {
	if explicit != "" {
		// Persist the explicit conversation ID so subsequent commands can find it.
		saveContextValue(ctx, azdClient, agentName, explicit, "conversations")
		return explicit, nil
	}
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

	// Create and persist a new conversation for multi-turn memory.
	newConvID, err := createConversation(ctx, endpoint, bearerToken)
	if err != nil {
		return "", fmt.Errorf("failed to create conversation: %w", err)
	}

	agentCtx.Conversations[agentName] = newConvID
	if err := saveLocalContext(agentCtx, configPath); err != nil {
		return newConvID, fmt.Errorf("failed to save conversation ID: %w", err)
	}

	return newConvID, nil
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
	_, err := os.Stat(path) //nolint:gosec // path is derived from controlled inputs (agent ID + "src/" prefix)
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
	slices.SortFunc(services, func(a, b *azdext.ServiceConfig) int {
		return cmp.Compare(a.Name, b.Name)
	})

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
	ServiceName    string // the resolved service name (from azure.yaml)
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
		ServiceName:    svc.Name,
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

// resolveAgentProtocol loads the agent.yaml manifest for the service and returns the
// protocol that the agent implements (e.g. "responses", "invocations").
// Defaults to "responses" when the manifest cannot be loaded or has no protocols.
func resolveAgentProtocol(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
) agent_api.AgentProtocol {
	svc, project, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return agent_api.AgentProtocolResponses
	}

	agentYamlPath := filepath.Join(project.Path, svc.RelativePath, "agent.yaml")
	data, err := os.ReadFile(agentYamlPath) //nolint:gosec // G304: path constructed from azd project root
	if err != nil {
		return agent_api.AgentProtocolResponses
	}

	var hosted agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &hosted); err == nil && len(hosted.Protocols) > 0 {
		return agent_api.AgentProtocol(hosted.Protocols[0].Protocol)
	}

	return agent_api.AgentProtocolResponses
}
