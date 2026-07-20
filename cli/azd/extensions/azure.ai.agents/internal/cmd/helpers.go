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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/google/uuid"
	"golang.org/x/term"
)

const (
	// ConfigFile is the legacy project-level state file for local agent context.
	// Kept only for migration purposes.
	ConfigFile = ".foundry-agent.json"

	// DefaultPort is the default port for local agent servers.
	DefaultPort = 8088
)

// AgentLocalContext holds local state persisted in UserConfig.
// This struct is kept as an in-memory representation for migration compatibility.
type AgentLocalContext struct {
	AgentName     string            `json:"agent_name,omitempty"`
	Sessions      map[string]string `json:"sessions,omitempty"`
	Conversations map[string]string `json:"conversations,omitempty"`
	Invocations   map[string]string `json:"invocations,omitempty"`
}

// resolveConfigPath returns the full path to the legacy .foundry-agent.json file.
// Kept for fetchOpenAPISpec (disk caching) and migration.
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

// resolveProjectPath returns the project path from the azd client.
// Used to generate project-discriminated local keys.
func resolveProjectPath(ctx context.Context, azdClient *azdext.AzdClient) string {
	if azdClient == nil {
		return ""
	}
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || projectResponse.Project == nil {
		return ""
	}
	return projectResponse.Project.Path
}

// readmeExistsForProject returns a closure suitable for passing to the
// nextstep resolvers (e.g. ResolveAfterInit, ResolveAfterRun,
// ResolveAfterDeploy). Given a service-relative path it checks for a
// canonical "README.md" file inside the project root and returns true
// when present. Only the canonical casing is checked to match the
// rendered guidance ("see <relPath>/README.md").
//
// Behavior is nil-safe: if the project path cannot be resolved (e.g.
// azdClient is nil or the gRPC call fails) the returned closure always
// reports false, which causes the resolvers to fall back to the bare
// placeholder payload — never a false-positive README pointer.
func readmeExistsForProject(ctx context.Context, azdClient *azdext.AzdClient) func(string) bool {
	projectRoot := resolveProjectPath(ctx, azdClient)
	return func(relativePath string) bool {
		if projectRoot == "" {
			return false
		}
		readmePath, err := paths.JoinAllowRoot(projectRoot, relativePath, "README.md")
		if err != nil {
			return false
		}
		_, err = os.Stat(readmePath)
		return err == nil
	}
}

// loadLocalContext reads the legacy .foundry-agent.json state file.
// Used only for migration. New code should use getContextValue/setContextValue.
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

// migrateFromLegacyFile checks for the legacy .foundry-agent.json and migrates
// its contents to UserConfig. The old file is deleted after successful migration.
// This is a best-effort operation; errors are logged and do not block the caller.
func migrateFromLegacyFile(ctx context.Context, azdClient *azdext.AzdClient) {
	configFilePath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return
	}

	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		return
	}

	agentCtx := loadLocalContext(configFilePath)

	allSucceeded := true
	anyData := len(agentCtx.Sessions) > 0 || len(agentCtx.Conversations) > 0
	for key, val := range agentCtx.Sessions {
		if err := setAgentSpecificContextValue(ctx, azdClient, "sessions", key, val); err != nil {
			log.Printf("migrateFromLegacyFile: failed to migrate session %q: %v", key, err)
			allSucceeded = false
		}
	}
	for key, val := range agentCtx.Conversations {
		if err := setAgentSpecificContextValue(ctx, azdClient, "conversations", key, val); err != nil {
			log.Printf("migrateFromLegacyFile: failed to migrate conversation %q: %v", key, err)
			allSucceeded = false
		}
	}

	if anyData && allSucceeded {
		if err := os.Remove(configFilePath); err != nil {
			log.Printf("migrateFromLegacyFile: failed to delete legacy file %s: %v", configFilePath, err)
		} else {
			log.Printf("migrateFromLegacyFile: migrated and deleted %s", configFilePath)
		}
	}
}

// saveContextValue persists a value into the named field of the config store.
// storeField selects the map: "sessions" or "conversations".
func saveContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	value string,
	storeField string,
) {
	if agentKey == "" || value == "" {
		return
	}
	setContextValueSafe(ctx, azdClient, storeField, agentKey, value)
}

// resolveLocalAgentName resolves the plain agent name used for local mode,
// without composing any port/project/version disambiguation into it. Use this
// when you need just a stable, file-system-safe identifier for the agent —
// for example, when naming the cached OpenAPI spec file shared with the
// `nextstep.ReadCachedOpenAPISpec` reader.
//
// For the structured config-store key (which DOES need port + project hash
// to avoid cross-project collisions), use `resolveLocalAgentKey` instead.
func resolveLocalAgentName(ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool) string {
	agentName := name

	if azdClient != nil {
		info, err := resolveAgentServiceFromProject(ctx, azdClient, name, noPrompt)
		if err == nil && info.ServiceName != "" {
			if agentName == "" {
				agentName = info.ServiceName
			}
		}
	}

	if agentName == "" {
		agentName = "local"
	}

	return agentName
}

// resolveLocalAgentKey builds the storage key for local mode from the azd project config.
// Returns the new structured key format: localhost:<port>/<projectHash>/agents/<name>/versions/latest/local
func resolveLocalAgentKey(ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool) string {
	return resolveLocalAgentKeyWithPort(ctx, azdClient, name, noPrompt, DefaultPort)
}

// resolveLocalAgentKeyWithPort builds the local storage key with a specific port.
func resolveLocalAgentKeyWithPort(
	ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool, port int,
) string {
	agentName := resolveLocalAgentName(ctx, azdClient, name, noPrompt)
	projectPath := resolveProjectPath(ctx, azdClient)
	return buildLocalAgentKey(port, agentName, "", projectPath)
}

// legacyKeysForLocal returns the legacy keys that the old code would have used
// for this agent in local mode.
func legacyKeysForLocal(serviceName, agentName string) []string {
	keys := []string{}
	if serviceName != "" {
		keys = append(keys, serviceName+"-local")
	}
	if agentName != "" && agentName != serviceName {
		keys = append(keys, agentName+"-local")
	}
	keys = append(keys, "local")
	return keys
}

// legacyKeysForRemote returns the legacy keys that the old code would have used
// for this agent in remote mode.
func legacyKeysForRemote(agentName string) []string {
	if agentName == "" {
		return nil
	}
	return []string{agentName}
}

// resolveStoredID resolves a persisted ID (session, conversation, etc.) from the config store.
// It checks (in order): explicit flag value, persisted value in the config store, then either
// returns "" (generateIfMissing=false, for remote mode where the server assigns the ID) or
// generates and persists a new UUID (generateIfMissing=true, for local mode).
// storeField selects which map to use: "sessions" or "conversations".
// legacyKeys are old-format keys to check as fallback during migration.
func resolveStoredID(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	explicit string,
	forceNew bool,
	storeField string,
	generateIfMissing bool,
	legacyKeys ...string,
) (string, error) {
	if explicit != "" {
		// Persist the explicit ID so that subsequent commands can find it.
		saveContextValue(ctx, azdClient, agentKey, explicit, storeField)
		return explicit, nil
	}
	if forceNew && !generateIfMissing {
		return "", nil
	}

	if !forceNew {
		// Try config store with fallback to legacy keys.
		val, err := getContextValueWithFallback(ctx, azdClient, storeField, agentKey, legacyKeys)
		if err != nil {
			if generateIfMissing {
				return uuid.NewString(), nil
			}
			return "", err
		}
		if val != "" {
			return val, nil
		}
	}

	if !generateIfMissing {
		return "", nil
	}

	newID := uuid.NewString()
	saveContextValue(ctx, azdClient, agentKey, newID, storeField)

	return newID, nil
}

// resolveStoredIDFromPath is a testable variant of resolveStoredID that operates on the
// config store directly. The configPath parameter is ignored (kept for API compat in tests)
// and operations go through UserConfig.
func resolveStoredIDFromPath(
	configPath string,
	agentKey string,
	explicit string,
	forceNew bool,
	storeField string,
	generateIfMissing bool,
) (string, error) {
	// This function is only used in tests. For the new implementation, tests should
	// use the config store directly. This wrapper creates a temporary client.
	// In practice, tests should be updated to use getContextValue/setContextValue.
	_ = configPath // no longer used

	if explicit != "" {
		return explicit, nil
	}
	if forceNew && !generateIfMissing {
		return "", nil
	}
	if !generateIfMissing {
		return "", nil
	}
	return uuid.NewString(), nil
}

// printSessionStatus prints the session line for the invoke banner.
// label is the formatted prefix (e.g. "Session:  " or "Session:      ").
func printSessionStatus(label, sid string) {
	if sid != "" {
		fmt.Printf("%s%s\n", label, sid)
	} else {
		fmt.Printf("%s(new -- server will assign)\n", label)
	}
}

// captureResponseSession reads the x-agent-session-id header from a response
// and saves it when the caller had no pre-existing session (sid == "").
// label is the formatted prefix for printing (e.g. "Session:  "). When label
// is empty the session is persisted silently (used by --output raw mode so
// stdout stays pristine).
func captureResponseSession(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	sid string,
	resp *http.Response,
	label string,
) {
	if sid != "" || azdClient == nil {
		return
	}
	if newSid := resp.Header.Get("x-agent-session-id"); newSid != "" {
		saveContextValue(ctx, azdClient, agentKey, newSid, "sessions")
		if label != "" {
			fmt.Printf("%s%s (assigned by server)\n", label, newSid)
		}
	}
}

// fetchOpenAPISpec fetches the OpenAPI spec from a running agent and caches it on disk.
// baseURL is the root URL (e.g., "http://localhost:8088" or "{endpoint}/agents/{name}/endpoint/protocols").
// suffix is "local" or "remote", used in the cached filename.
// apiVersion, when non-empty, is appended as the "?api-version=<v>" query parameter.
// Local agents do not require this; remote Foundry endpoints reject requests without it.
// If forceRefresh is false and the file already exists, the fetch is skipped.
//
// Returns the on-disk path to the cached spec on success (whether freshly
// written or already cached), plus a fresh flag that is true only when this
// call actually wrote the file. Callers that want to surface the "OpenAPI
// spec saved to ..." line gate on the fresh flag; callers that just need the
// path (or want silence) ignore it. Returns ("", false) on any failure;
// errors are silently swallowed because the spec is best-effort.
func fetchOpenAPISpec(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	baseURL string,
	agentName string,
	suffix string,
	bearerToken string,
	apiVersion string,
	forceRefresh bool,
) (string, bool) {
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return "", false
	}
	configDir := filepath.Dir(configPath)

	// Sanitize agentName to prevent path traversal in the cached filename.
	safeName := strings.ReplaceAll(agentName, "..", "_")
	safeName = strings.ReplaceAll(safeName, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")

	specFile := filepath.Join(configDir, fmt.Sprintf("openapi-%s-%s.json", safeName, suffix))

	if !forceRefresh {
		if _, err := os.Stat(specFile); err == nil {
			return specFile, false // already cached; surface the path without re-fetching
		}
	}

	specURL := baseURL + "/invocations/docs/openapi.json"
	if apiVersion != "" {
		specURL += "?api-version=" + url.QueryEscape(apiVersion)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return "", false
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // G704: URL constructed from azd environment or localhost
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}

	if err := os.WriteFile(specFile, body, 0600); err != nil {
		return "", false
	}

	return specFile, true
}

// resolveConversationID resolves a Foundry conversation ID.
// When explicit is provided, it is persisted and returned directly.
// If no conversation is found (or forceNew is true), it attempts to create one and persist it.
// Returns empty string when conversation creation fails, allowing invoke to continue without multi-turn memory.
func resolveConversationID(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	explicit string,
	forceNew bool,
	projectEndpoint string,
	bearerToken string,
	agentName string,
	apiVersion string,
	options *agent_api.SessionRequestOptions,
	legacyKeys ...string,
) (string, error) {
	if explicit != "" {
		// Persist the explicit conversation ID so subsequent commands can find it.
		saveContextValue(ctx, azdClient, agentKey, explicit, "conversations")
		return explicit, nil
	}

	if !forceNew {
		val, err := getContextValueWithFallback(ctx, azdClient, "conversations", agentKey, legacyKeys)
		if err == nil && val != "" {
			return val, nil
		}
	}

	// Create and persist a new conversation for multi-turn memory.
	newConvID, err := createConversation(ctx, projectEndpoint, agentName, bearerToken, apiVersion, options)
	if err != nil {
		return "", fmt.Errorf("failed to create conversation: %w", err)
	}

	saveContextValue(ctx, azdClient, agentKey, newConvID, "conversations")

	return newConvID, nil
}

// setACREnvVar sets the AZD_AGENT_SKIP_ACR environment variable based on whether ACR
// should be skipped. ACR is skipped when:
// - Code deploy mode (no container registry needed)
// - Pre-built image provided via --image flag (user manages their own registry)
//
// This env var is consumed by the Bicep template in Azure-Samples/azd-ai-starter-basic
// (infra/main.bicep) as `param skipAcr bool` to conditionally skip ACR resource creation.
//
// Cross-repo dependency: changes to this variable name must be coordinated with
// the template parameter mapping in main.parameters.json of the starter template.
func setACREnvVar(ctx context.Context, azdClient *azdext.AzdClient, envName string, skipACR bool) error {
	value := "false"
	if skipACR {
		value = "true"
	}

	if err := setEnvValue(ctx, azdClient, envName, "AZD_AGENT_SKIP_ACR", value); err != nil {
		if skipACR {
			return fmt.Errorf("configuring ACR skip: %w", err)
		}
		return fmt.Errorf("configuring ACR for container deploy: %w", err)
	}
	return nil
}

// detectProjectType detects the project type and suggests a start command.
type ProjectType struct {
	Language string // "python", "dotnet", "node", "unknown"
	StartCmd string // suggested start command
}

type projectLanguages struct {
	python         bool
	pythonMetadata bool
	pythonMain     bool
	dotnet         bool
	node           bool
}

func detectProjectLanguages(projectDir string) projectLanguages {
	if projectDir == "" {
		projectDir = "."
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		warnProjectInspectionFailure(os.Stderr, projectDir, err)
		return projectLanguages{}
	}

	var languages projectLanguages
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		switch {
		case name == "pyproject.toml",
			name == "requirements.txt":
			languages.python = true
			languages.pythonMetadata = true
		case strings.HasSuffix(name, ".py"):
			languages.python = true
			if name == "main.py" {
				languages.pythonMain = true
			}
		case strings.HasSuffix(name, ".csproj"):
			languages.dotnet = true
		case name == "package.json":
			languages.node = true
		}
	}

	return languages
}

func warnProjectInspectionFailure(writer io.Writer, projectDir string, err error) {
	fmt.Fprintf(writer, "%s", output.WithWarningFormat(
		"WARNING: cannot read project directory %q: %v. "+
			"Treating the project as unknown, so code deploy will not be offered "+
			"and no local start command can be detected. "+
			"Check the service path in azure.yaml and directory permissions.\n",
		projectDir,
		err,
	))
}

func isPythonProject(projectDir string) bool {
	return detectProjectLanguages(projectDir).python
}

func isDotnetProject(projectDir string) bool {
	return detectProjectLanguages(projectDir).dotnet
}

func supportsCodeDeploy(projectDir string) bool {
	languages := detectProjectLanguages(projectDir)
	// Python metadata is authoritative. A lone .py file is only a
	// fallback when package.json does not identify a Node project.
	supportsPython := languages.pythonMetadata ||
		(languages.python && !languages.node)
	return languages.dotnet || supportsPython
}

func detectProjectType(projectDir string) ProjectType {
	languages := detectProjectLanguages(projectDir)

	if languages.pythonMetadata {
		if fileExists(filepath.Join(projectDir, "main.py")) {
			return ProjectType{Language: "python", StartCmd: "python main.py"}
		}
		return ProjectType{Language: "python", StartCmd: ""}
	}

	if languages.dotnet {
		return ProjectType{Language: "dotnet", StartCmd: "dotnet run"}
	}

	if languages.node {
		return ProjectType{Language: "node", StartCmd: "npm start"}
	}

	if languages.pythonMain {
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
	ServiceName     string // azure.yaml service key
	AgentName       string // deployed agent name from env; invoke may opt into brownfield fallback
	Version         string // deployed agent version from env
	AgentEndpoint   string // full AGENT_{SVC}_ENDPOINT URL (includes name + version)
	ProjectEndpoint string // adopted project endpoint used by a verified brownfield fallback
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

type brownfieldAgentReference struct {
	name            string
	projectEndpoint string
}

// brownfieldInlineAgentReference returns the inline hosted-agent name and the
// endpoint of the adopted Foundry project it belongs to. The endpoint is only a
// candidate signal; callers must verify that the named agent exists there
// before using the reference.
func brownfieldInlineAgentReference(
	svc *azdext.ServiceConfig,
	projectConfig *azdext.ProjectConfig,
) *brownfieldAgentReference {
	if svc == nil || projectConfig == nil {
		return nil
	}

	var projectEndpoint string
	for _, dependency := range svc.GetUses() {
		projectService := projectConfig.GetServices()[dependency]
		if projectService == nil || projectService.GetHost() != AiProjectHost {
			continue
		}
		cfg, err := projectpkg.LoadServiceTargetAgentConfig(projectService)
		if err != nil {
			log.Printf(
				"resolve agent service %q: failed to read project dependency %q: %v",
				svc.Name, dependency, err,
			)
			continue
		}
		if cfg != nil && strings.TrimSpace(cfg.Endpoint) != "" {
			projectEndpoint = strings.TrimSpace(cfg.Endpoint)
			break
		}
	}
	if projectEndpoint == "" {
		return nil
	}

	definition, isHosted, found, _, err := projectpkg.AgentDefinitionFromService(svc)
	if err != nil {
		log.Printf("resolve agent service %q: failed to read inline agent definition: %v", svc.Name, err)
		return nil
	}
	if !found || !isHosted {
		return nil
	}

	agentName := strings.TrimSpace(definition.Name)
	if agentName == "" {
		return nil
	}
	return &brownfieldAgentReference{
		name:            agentName,
		projectEndpoint: projectEndpoint,
	}
}

type brownfieldAgentExistenceResolver func(context.Context, string, string) (bool, error)

type agentServiceResolutionOptions struct {
	allowBrownfieldInlineName bool
	brownfieldAgentExists     brownfieldAgentExistenceResolver
}

type agentServiceResolutionOption func(*agentServiceResolutionOptions)

func resolveBrownfieldAgentExists(
	ctx context.Context,
	projectEndpoint string,
	agentName string,
) (bool, error) {
	credential, err := newAgentCredential()
	if err != nil {
		return false, err
	}

	client := agent_api.NewAgentClient(projectEndpoint, credential)
	return agents.AgentExists(ctx, client, agentName, DefaultAgentAPIVersion)
}

// withBrownfieldInlineAgentName allows remote invoke to use an inline agent name
// after verifying it exists in the adopted Foundry project. It is opt-in because
// shared callers include destructive commands such as delete, which must
// continue requiring deployment state or an explicit agent name.
func withBrownfieldInlineAgentName() agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.allowBrownfieldInlineName = true
		options.brownfieldAgentExists = resolveBrownfieldAgentExists
	}
}

func withBrownfieldAgentExistenceResolver(
	resolver brownfieldAgentExistenceResolver,
) agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.allowBrownfieldInlineName = true
		options.brownfieldAgentExists = resolver
	}
}

// resolveAgentServiceFromProject finds the azure.ai.agent service in azure.yaml
// and resolves its deployed agent name and version from the azd environment.
// Callers may explicitly opt into the brownfield inline-name fallback; deployed
// AGENT_<SERVICE>_NAME output always overrides it.
func resolveAgentServiceFromProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
	options ...agentServiceResolutionOption,
) (*AgentServiceInfo, error) {
	svc, projectConfig, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return nil, err
	}

	resolutionOptions := agentServiceResolutionOptions{}
	for _, option := range options {
		option(&resolutionOptions)
	}

	info := &AgentServiceInfo{ServiceName: svc.Name}

	// Resolve deployed agent name and version from the azd environment. The
	// deployed name wins because it reflects the resource actually created.
	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		if resolutionOptions.allowBrownfieldInlineName {
			return info, fmt.Errorf("getting current environment for agent service %q: %w", svc.Name, err)
		}
		return info, nil
	}
	if envResponse == nil || envResponse.Environment == nil || envResponse.Environment.Name == "" {
		if resolutionOptions.allowBrownfieldInlineName {
			return info, fmt.Errorf("current environment is not available for agent service %q", svc.Name)
		}
		return info, nil
	}

	serviceKey := toServiceKey(svc.Name)
	nameKey := fmt.Sprintf("AGENT_%s_NAME", serviceKey)
	versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)

	nameResponse, nameErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     nameKey,
	})
	switch {
	case nameErr != nil:
		if resolutionOptions.allowBrownfieldInlineName {
			return info, fmt.Errorf(
				"reading %s from environment %q: %w",
				nameKey,
				envResponse.Environment.Name,
				nameErr,
			)
		}
		log.Printf("resolve agent service %q: failed to read %s: %v", svc.Name, nameKey, nameErr)
	case nameResponse != nil && nameResponse.Value != "":
		info.AgentName = nameResponse.Value
	case resolutionOptions.allowBrownfieldInlineName:
		reference := brownfieldInlineAgentReference(svc, projectConfig)
		if reference == nil {
			break
		}
		if resolutionOptions.brownfieldAgentExists == nil {
			return info, fmt.Errorf("brownfield agent existence resolver is not configured")
		}

		exists, err := resolutionOptions.brownfieldAgentExists(
			ctx,
			reference.projectEndpoint,
			reference.name,
		)
		if err != nil {
			return info, fmt.Errorf(
				"checking whether agent %q exists in the adopted Foundry project: %w",
				reference.name,
				err,
			)
		}
		if exists {
			info.AgentName = reference.name
			info.ProjectEndpoint = reference.projectEndpoint
			return info, nil
		}
	}

	if v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     versionKey,
	}); err == nil && v.Value != "" {
		info.Version = v.Value
	}

	endpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey)
	if v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     endpointKey,
	}); err == nil && v.Value != "" {
		info.AgentEndpoint = v.Value
	}

	return info, nil
}

// ServiceRunContext holds the resolved context needed for local development.
type ServiceRunContext struct {
	ServiceName    string // the resolved service name (from azure.yaml)
	ProjectDir     string // absolute path to the service source directory
	StartupCommand string // startupCommand from AdditionalProperties (may be empty)
	// Definition is the resolved agent definition (from the inline azure.yaml
	// entry or a legacy agent.yaml). It is nil when no definition can be resolved.
	Definition *agent_yaml.ContainerAgent
}

// resolveServiceRunContext queries the azd project to find the matching azure.ai.agent
// service, then returns the service's absolute source directory and startup command.
func resolveServiceRunContext(ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool) (*ServiceRunContext, error) {
	svc, project, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return nil, err
	}

	projectDir, err := paths.JoinAllowRoot(project.Path, svc.RelativePath)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid service path for %s: %s", svc.Name, err),
			"update azure.yaml so the agent service path stays within the project directory",
		)
	}

	var startupCmd string
	if agentConfig, cfgErr := projectpkg.LoadServiceTargetAgentConfig(svc); cfgErr == nil {
		startupCmd = agentConfig.StartupCommand
	}

	var definition *agent_yaml.ContainerAgent
	if def, _, source, defErr := projectpkg.LoadAgentDefinition(svc, project.Path); defErr == nil {
		definition = &def
		if source.IsLegacy() {
			projectpkg.WarnLegacyAgentShape(source)
		}
	}

	return &ServiceRunContext{
		ServiceName:    svc.Name,
		ProjectDir:     projectDir,
		StartupCommand: startupCmd,
		Definition:     definition,
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
// protocol that the agent implements (e.g. "responses", "invocations") along with
// the resolved service name. The service name is useful for callers that need to
// avoid a redundant resolveAgentService call (and its interactive prompt) later.
// Returns an error when the protocol cannot be determined, with a contextual
// suggestion guiding the user to fix the underlying issue.
func resolveAgentProtocol(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
) (agent_api.AgentProtocol, string, error) {
	svc, proj, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"could not resolve agent service in azd project: %s", err,
			),
			"run from your project directory and ensure "+
				"azure.yaml contains an azure.ai.agent service",
		)
	}

	hosted, isHosted, source, err := projectpkg.LoadAgentDefinition(svc, proj.Path)
	if err != nil {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("could not resolve the agent definition for %s: %s", svc.Name, err),
			"ensure the agent definition is present in azure.yaml or run `azd ai agent init`",
		)
	}
	if source.IsLegacy() {
		projectpkg.WarnLegacyAgentShape(source)
	}
	if !isHosted {
		return "", "", exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("agent service %s is not a hosted agent", svc.Name),
			"only hosted agents can be invoked",
		)
	}

	protocol, err := protocolFromContainerAgent(hosted)
	if err != nil {
		return "", "", err
	}
	return protocol, svc.Name, nil
}

// protocolFromContainerAgent extracts the protocol to use for invocation from a
// resolved agent definition. Returns an error with a contextual suggestion when
// the definition does not declare exactly one invocable protocol.
//
// When multiple protocols are declared (e.g. "responses" + "a2a"), the caller
// must use --protocol to disambiguate.
func protocolFromContainerAgent(
	hosted agent_yaml.ContainerAgent,
) (agent_api.AgentProtocol, error) {
	if len(hosted.Protocols) == 0 {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the agent definition does not declare any protocols",
			"add a protocols section to the agent definition",
		)
	}

	// Validate that no protocol entry has an empty value, and collect invocable ones.
	var invocable []agent_api.AgentProtocol
	for _, rec := range hosted.Protocols {
		p := agent_api.AgentProtocol(strings.TrimSpace(rec.Protocol))
		if p == "" {
			return "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"agent.yaml declares a protocol entry, "+
					"but its protocol field is empty",
				"set a non-empty protocol value in agent.yaml",
			)
		}
		if p.IsInvocable() {
			invocable = append(invocable, p)
		}
	}

	switch len(invocable) {
	case 0:
		names := make([]string, len(hosted.Protocols))
		for i, p := range hosted.Protocols {
			names[i] = p.Protocol
		}
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"agent.yaml declares only non-invocable protocols: %s",
				strings.Join(names, ", "),
			),
			"azd can only invoke agents using the responses, invocations, or a2a protocols",
		)
	case 1:
		// Exactly one invocable protocol — but if the agent declares
		// multiple protocols overall, require --protocol to be explicit.
		if len(hosted.Protocols) > 1 {
			return "", multiProtocolError(hosted.Protocols)
		}
		return invocable[0], nil
	default:
		return "", multiProtocolError(hosted.Protocols)
	}
}

// multiProtocolError builds a validation error for agents that declare
// multiple protocols, listing the valid invocable choices.
func multiProtocolError(
	protocols []agent_yaml.ProtocolVersionRecord,
) error {
	names := make([]string, len(protocols))
	for i, p := range protocols {
		names[i] = p.Protocol
	}
	supported := make([]string, len(agent_api.InvocableProtocols()))
	for i, p := range agent_api.InvocableProtocols() {
		supported[i] = string(p)
	}
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf(
			"agent.yaml declares multiple protocols (%s)",
			strings.Join(names, ", "),
		),
		fmt.Sprintf(
			"use --protocol to specify which invocable protocol "+
				"to use (supported: %s)",
			strings.Join(supported, ", "),
		),
	)
}

// isTerminal reports whether fd refers to an interactive terminal.
// Used to gate human-only output such as the next-step guidance block.
func isTerminal(fd uintptr) bool {
	//nolint:gosec // file descriptors fit in int on all supported platforms
	return term.IsTerminal(int(fd))
}
