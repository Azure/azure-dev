// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"errors"
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
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/paths"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/google/uuid"
	"golang.org/x/term"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// DefaultPort is the default port for local agent servers.
	DefaultPort = 8088
)

// resolveProjectCacheDir returns the current environment's project-local cache directory.
func resolveProjectCacheDir(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
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

	return filepath.Join(projectResponse.Project.Path, ".azure", envResponse.Environment.Name), nil
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

// saveContextValue persists a value into the named field of the extension state store.
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

// resolveStoredID resolves a persisted ID (session, conversation, etc.) from extension state.
// It checks (in order): explicit flag value, persisted value in extension state, then either
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
		// Persist the explicit ID so that subsequent commands can find it.
		saveContextValue(ctx, azdClient, agentKey, explicit, storeField)
		return explicit, nil
	}
	if forceNew && !generateIfMissing {
		return "", nil
	}

	if !forceNew {
		val, err := getAgentSpecificContextValue(ctx, azdClient, storeField, agentKey)
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

// printSessionStatus prints the session line for the invoke banner.
// label is the formatted prefix (e.g. "Session:  " or "Session:      ").
func printSessionStatus(label, sid string) {
	if sid != "" {
		fmt.Printf("%s%s\n", label, sid)
	} else {
		fmt.Printf("%s(new -- server will assign)\n", label)
	}
}

// captureResponseSession persists a server-assigned session and returns the
// effective session ID. An empty label suppresses session output.
func captureResponseSession(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
	sid string,
	resp *http.Response,
	label string,
) string {
	if sid != "" {
		return sid
	}
	if newSid := resp.Header.Get("x-agent-session-id"); newSid != "" {
		if azdClient != nil {
			saveContextValue(ctx, azdClient, agentKey, newSid, "sessions")
		}
		if label != "" && azdClient != nil {
			fmt.Printf("%s%s (assigned by server)\n", label, newSid)
		}
		return newSid
	}
	return ""
}

const openAPIProbeTimeout = 10 * time.Second

// openAPIProbeError omits URLs and bodies from diagnostics.
type openAPIProbeError struct {
	stage string
	cause error
}

func (e *openAPIProbeError) Error() string {
	return "OpenAPI metadata discovery: " + e.stage
}

func (e *openAPIProbeError) Unwrap() error {
	return e.cause
}

// fetchOpenAPISpec caches metadata using the existing remote session.
// Remote discovery requires sessionID and bounds configuration RPCs
// separately from HTTP. Local discovery does not use a session ID.
// The fresh result indicates that this call wrote the cache.
func fetchOpenAPISpec(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	transport http.RoundTripper,
	baseURL string,
	agentName string,
	suffix string,
	bearerToken string,
	apiVersion string,
	sessionID string,
	requestHeaders http.Header,
	forceRefresh bool,
) (string, bool, error) {
	remote := suffix == "remote"
	if remote && sessionID == "" {
		return "", false, nil
	}
	if err := ctx.Err(); err != nil {
		return "", false, &openAPIProbeError{stage: "canceled", cause: err}
	}

	configCtx := ctx
	cancel := func() {}
	if remote {
		configCtx, cancel = context.WithTimeout(ctx, openAPIProbeTimeout)
		defer cancel()
	}
	configDir, err := resolveProjectCacheDir(configCtx, azdClient)
	cancel()
	if err != nil {
		return "", false, &openAPIProbeError{stage: "configuration lookup failed", cause: err}
	}
	if err := ctx.Err(); err != nil {
		return "", false, &openAPIProbeError{stage: "canceled", cause: err}
	}
	// Sanitize agentName to prevent path traversal in the cached filename.
	safeName := strings.ReplaceAll(agentName, "..", "_")
	safeName = strings.ReplaceAll(safeName, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")

	specFile := filepath.Join(configDir, fmt.Sprintf("openapi-%s-%s.json", safeName, suffix))

	if !forceRefresh {
		if _, err := os.Stat(specFile); err == nil {
			return specFile, false, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, &openAPIProbeError{stage: "cache lookup failed", cause: err}
		}
	}

	specURL := baseURL + "/invocations/docs/openapi.json"
	query := url.Values{}
	if apiVersion != "" {
		query.Set("api-version", apiVersion)
	}
	if sessionID != "" {
		query.Set("agent_session_id", sessionID)
	}
	if len(query) > 0 {
		specURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return "", false, &openAPIProbeError{stage: "request construction failed", cause: err}
	}
	if requestHeaders != nil {
		req.Header = requestHeaders.Clone()
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	client := &http.Client{Transport: transport, Timeout: openAPIProbeTimeout}
	if remote {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	resp, err := client.Do(req) //nolint:gosec // G704: URL constructed from azd environment or localhost
	if err != nil {
		return "", false, &openAPIProbeError{stage: "HTTP request failed", cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, &openAPIProbeError{stage: fmt.Sprintf("HTTP status %d", resp.StatusCode)}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, &openAPIProbeError{stage: "response read failed", cause: err}
	}

	if err := os.WriteFile(specFile, body, 0600); err != nil {
		return "", false, &openAPIProbeError{stage: "cache write failed", cause: err}
	}

	return specFile, true, nil
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
) (string, error) {
	if explicit != "" {
		// Persist the explicit conversation ID so subsequent commands can find it.
		saveContextValue(ctx, azdClient, agentKey, explicit, "conversations")
		return explicit, nil
	}

	if !forceNew {
		val, err := getAgentSpecificContextValue(ctx, azdClient, "conversations", agentKey)
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
// - Pre-built image provided by a flag, manifest, or detected source definition
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

// AgentServiceInfo holds the resolved deployment information for an agent service.
type AgentServiceInfo struct {
	IsHosted                    bool                               // populated only when hosted classification is requested
	ServiceName                 string                             // azure.yaml service key
	EnvironmentName             string                             // environment used for deployed metadata, when available
	AgentName                   string                             // deployed name; may use brownfield fallback
	Version                     string                             // deployed agent version from env
	AgentEndpoint               string                             // full AGENT_{SVC}_ENDPOINT URL (includes name + version)
	ProtocolEndpoints           map[agent_api.AgentProtocol]string // per-protocol deployment endpoint URLs
	ProtocolEndpointsPresent    bool                               // deployment exposed protocol endpoint metadata
	ProtocolEndpointsIncomplete bool                               // marker exists but is incomplete
	ProtocolEndpointsStale      bool
	ProjectEndpoint             string // adopted project endpoint for brownfield fallback
	// ServiceDir is the absolute path to the service's source directory
	// (project.Path joined with svc.RelativePath). May be empty if the resolver
	// could not compute it.
	ServiceDir string
}

func withDeployedProtocolEndpoints() agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.includeProtocolEndpoints = true
	}
}

func withDeployedAgentNameLookup() agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.matchDeployedAgentName = true
	}
}

// Explicit protocols can invoke a direct name without local deployment state.
// Keep the lookup when state exists, but do not require a default environment.
func withOptionalDeployedAgentNameLookup() agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.matchDeployedAgentName = true
		options.allowMissingDefaultEnvironment = true
	}
}

type agentProtocolEndpointsError struct {
	err error
}

func (e *agentProtocolEndpointsError) Error() string {
	return e.err.Error()
}

func (e *agentProtocolEndpointsError) Unwrap() error {
	return e.err
}

func isAgentProtocolEndpointsError(err error) bool {
	_, ok := errors.AsType[*agentProtocolEndpointsError](err)
	return ok
}

func resolveAgentProtocolEndpoints(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	serviceName string,
) (map[agent_api.AgentProtocol]string, bool, bool, bool, error) {
	if azdClient == nil || envName == "" || serviceName == "" {
		return nil, false, false, false, nil
	}

	envValues, err := getAgentEnvironmentValues(ctx, azdClient, envName)
	if err != nil {
		return nil, false, false, false, &agentProtocolEndpointsError{
			err: fmt.Errorf("failed to read environment values: %w", err),
		}
	}

	endpoints, present, stale, incomplete := resolveAgentProtocolEndpointsFromValues(
		envValues,
		serviceName,
	)
	return endpoints, present, stale, incomplete, nil
}

func getAgentEnvironmentValues(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
) (map[string]string, error) {
	resp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envName,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("environment values response is empty")
	}

	envValues := make(map[string]string, len(resp.KeyValues))
	for _, kv := range resp.KeyValues {
		if kv != nil {
			envValues[kv.Key] = kv.Value
		}
	}
	return envValues, nil
}

func resolveAgentProtocolEndpointsFromValues(
	envValues map[string]string,
	serviceName string,
) (map[agent_api.AgentProtocol]string, bool, bool, bool) {
	serviceKey := toServiceKey(serviceName)
	versionKey := envkey.AgentProtocolEndpointsVersion(serviceName)
	marker, markerPresent := envValues[versionKey]

	endpoints := make(map[agent_api.AgentProtocol]string)
	for _, protocol := range projectpkg.DisplayableProtocolEnvSuffixes() {
		key := fmt.Sprintf("AGENT_%s_%s_ENDPOINT", serviceKey, protocol.Suffix)
		if endpoint := strings.TrimSpace(envValues[key]); endpoint != "" {
			endpoints[agent_api.AgentProtocol(protocol.Label)] = endpoint
		}
	}

	if !markerPresent {
		// Legacy deployments have no completeness marker, so their endpoint
		// values may include stale protocols from an earlier deployment.
		return nil, len(endpoints) > 0, len(endpoints) > 0, false
	}
	if strings.TrimSpace(marker) != "1" {
		return nil, true, false, true
	}
	return endpoints, true, false, false
}

// ambiguousAgentServicesError lets callers provide command-specific selection guidance.
type ambiguousAgentServicesError struct {
	names []string
}

func (e *ambiguousAgentServicesError) Error() string {
	return fmt.Sprintf("multiple azure.ai.agent services found in azure.yaml: %s\n\n"+
		"Provide the service name as a positional argument to specify which one to use", strings.Join(e.names, ", "))
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
		return nil, &ambiguousAgentServicesError{names: names}
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
			return nil, nil, &projectAgentServiceNotFoundError{
				serviceName: name,
			}
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

	// Commands annotated with gateAgentServices classify every selection here.
	if err := requireSelectedServiceFeatures(ctx, svc, projectResponse.Project.GetPath()); err != nil {
		return nil, nil, err
	}
	return svc, projectResponse.Project, nil
}

type agentServiceLookupNotFoundError interface {
	error
	agentServiceLookupNotFound()
}

type projectAgentServiceNotFoundError struct {
	serviceName string
}

func (e *projectAgentServiceNotFoundError) Error() string {
	return fmt.Sprintf(
		"no azure.ai.agent service named '%s' found in azure.yaml",
		e.serviceName,
	)
}

func (e *projectAgentServiceNotFoundError) agentServiceLookupNotFound() {}

type deployedAgentServiceNotFoundError struct {
	deployedName string
}

func (e *deployedAgentServiceNotFoundError) Error() string {
	return fmt.Sprintf(
		"no azure.ai.agent service resolves to deployed agent %q",
		e.deployedName,
	)
}

func (e *deployedAgentServiceNotFoundError) agentServiceLookupNotFound() {}

func resolveAgentServiceByDeployedName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	deployedName string,
	allowMissingDefaultEnvironment bool,
) (*azdext.ServiceConfig, *azdext.ProjectConfig, map[string]string, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get project config: %w", err)
	}
	if projectResponse.Project == nil {
		return nil, nil, nil, fmt.Errorf("failed to get project config")
	}

	envResponse, err := azdClient.Environment().GetCurrent(
		ctx, &azdext.EmptyRequest{},
	)
	if err != nil {
		if allowMissingDefaultEnvironment && isDefaultEnvironmentMissing(err) {
			return nil, nil, nil, &deployedAgentServiceNotFoundError{deployedName: deployedName}
		}
		return nil, nil, nil, fmt.Errorf("failed to get current environment: %w", err)
	}
	if envResponse == nil || envResponse.Environment == nil ||
		envResponse.Environment.Name == "" {
		return nil, nil, nil, fmt.Errorf("current environment is not available")
	}

	envValues, err := getAgentEnvironmentValues(
		ctx,
		azdClient,
		envResponse.Environment.Name,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading environment values: %w", err)
	}

	var matched *azdext.ServiceConfig
	for _, service := range projectResponse.Project.Services {
		if service.Host != AiAgentHost {
			continue
		}

		key := fmt.Sprintf("AGENT_%s_NAME", toServiceKey(service.Name))
		if strings.TrimSpace(envValues[key]) == deployedName {
			if matched != nil {
				return nil, nil, nil, fmt.Errorf(
					"multiple azure.ai.agent services resolve to deployed agent %q",
					deployedName,
				)
			}
			matched = service
		}
	}

	if matched == nil {
		return nil, nil, nil, &deployedAgentServiceNotFoundError{
			deployedName: deployedName,
		}
	}

	return matched, projectResponse.Project, envValues, nil
}

func isDefaultEnvironmentMissing(err error) bool {
	// The host returns a plain sentinel error, serialized by gRPC as Unknown.
	// Do not treat other Unknown/NotFound errors (corrupt state, missing files,
	// transport failures, etc.) as absence of a selected default environment.
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Unknown && st.Message() == "default environment not found"
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
	environmentValues map[string]string,
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
		props := projectpkg.ServiceConfigProps(projectService)
		if props == nil {
			continue
		}
		values := props.AsMap()
		if projectConfig.GetPath() != "" {
			resolved, err := foundry.ResolveFileRefs(
				values,
				projectConfig.GetPath(),
			)
			if err != nil {
				log.Printf(
					"resolve agent service %q: failed to read project "+
						"dependency %q: %v",
					svc.Name,
					dependency,
					err,
				)
				continue
			}
			values = resolved
		}
		expanded, err := expandBrownfieldServiceValues(
			values,
			environmentValues,
		)
		if err != nil {
			log.Printf(
				"resolve agent service %q: failed to expand project "+
					"dependency %q: %v",
				svc.Name,
				dependency,
				err,
			)
			continue
		}
		values, ok := expanded.(map[string]any)
		if !ok {
			continue
		}
		if endpoint, ok := values["endpoint"].(string); ok &&
			strings.TrimSpace(endpoint) != "" {
			projectEndpoint = strings.TrimSpace(endpoint)
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

func expandBrownfieldServiceValues(
	value any,
	environmentValues map[string]string,
) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		expanded := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := expandBrownfieldServiceValues(
				item,
				environmentValues,
			)
			if err != nil {
				return nil, err
			}
			expanded[key] = resolved
		}
		return expanded, nil
	case []any:
		expanded := make([]any, len(typed))
		for index, item := range typed {
			resolved, err := expandBrownfieldServiceValues(
				item,
				environmentValues,
			)
			if err != nil {
				return nil, err
			}
			expanded[index] = resolved
		}
		return expanded, nil
	case string:
		return foundry.ExpandEnv(typed, func(name string) string {
			return environmentValues[name]
		})
	default:
		return value, nil
	}
}

type brownfieldAgentExistenceResolver func(context.Context, string, string) (bool, error)

type agentServiceResolutionOptions struct {
	environmentName                string
	allowBrownfieldInlineName      bool
	brownfieldAgentExists          brownfieldAgentExistenceResolver
	includeProtocolEndpoints       bool
	matchDeployedAgentName         bool
	rejectVoiceInvocation          bool
	resolvePromptModel             bool
	requireHostedKind              bool
	allowMissingDefaultEnvironment bool
	includeHostedKind              bool
}

type agentServiceResolutionOption func(*agentServiceResolutionOptions)

func withHostedKind() agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.includeHostedKind = true
	}
}

// withAgentEnvironment resolves deployment metadata from an explicitly selected environment.
// Environment.GetCurrent returns the project default, not the SDK's --environment override.
func withAgentEnvironment(name string) agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.environmentName = name
	}
}

func withHostedAgentKind() agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.requireHostedKind = true
	}
}

// errVoiceInvocationUnsupported is shared by automatic and explicit-protocol
// invocation so direct-name fallback cannot swallow the voice guidance.
var errVoiceInvocationUnsupported = exterrors.Validation(
	exterrors.CodeUnsupportedAgentKind,
	"voice agents cannot be invoked with this command",
	"open your voice agent in the Microsoft Foundry portal at https://ai.azure.com to try it",
)

func voiceInvocationError(svc *azdext.ServiceConfig, projectRoot string) error {
	// An unreadable definition is not evidence of a non-voice agent. Preserve the
	// detection error so invocation cannot fall back to stale deployment metadata.
	isVoice, err := agentkind.IsPromptVoice(svc, projectRoot)
	if err != nil {
		return exterrors.ValidationFromError(
			err,
			exterrors.CodeInvalidServiceConfig,
			"determining agent kind for invocation",
			"fix the agent service configuration in azure.yaml",
		)
	}
	if isVoice {
		return errVoiceInvocationUnsupported
	}
	return nil
}

func withVoiceInvocationGuidance() agentServiceResolutionOption {
	return func(options *agentServiceResolutionOptions) {
		options.rejectVoiceInvocation = true
	}
}

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
	resolutionOptions := agentServiceResolutionOptions{}
	for _, option := range options {
		option(&resolutionOptions)
	}

	var envValues map[string]string
	svc, projectConfig, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		if isServiceGateError(err) || !resolutionOptions.matchDeployedAgentName || name == "" {
			return nil, err
		}
		svc, projectConfig, envValues, err = resolveAgentServiceByDeployedName(
			ctx, azdClient, name, resolutionOptions.allowMissingDefaultEnvironment,
		)
		if err != nil {
			return nil, err
		}
		if err := requireSelectedServiceFeatures(ctx, svc, projectConfig.Path); err != nil {
			return nil, err
		}
	}

	if resolutionOptions.rejectVoiceInvocation {
		if err := voiceInvocationError(svc, projectConfig.Path); err != nil {
			return nil, err
		}
	}
	if resolutionOptions.requireHostedKind {
		kind, err := agentkind.Kind(svc, projectConfig.Path)
		if err != nil {
			return nil, exterrors.ValidationFromError(err, exterrors.CodeInvalidServiceConfig,
				"determining agent kind for State Stores", "fix the agent service configuration in azure.yaml")
		}
		// Let the service determine support when the definition does not declare a kind.
		if kind != "" && kind != string(agent_yaml.AgentKindHosted) {
			return nil, exterrors.Validation(exterrors.CodeUnsupportedAgentKind,
				fmt.Sprintf("State Stores require a hosted agent; service %q has kind %q", svc.Name, kind),
				"select the hosted agent service (not a prompt or voice wrapper)")
		}
	}

	info := &AgentServiceInfo{ServiceName: svc.Name}
	if resolutionOptions.includeHostedKind && os.Getenv("AGENT_DEFINITION_PATH") == "" {
		// Telemetry classification must never change whether invocation proceeds.
		// Use the deploy path's definition loader so an invalid or non-hosted
		// project service cannot be counted as a hosted invoke.
		if _, hosted, _, err := projectpkg.LoadHostedAgentDefinition(svc, projectConfig.Path); err == nil {
			info.IsHosted = hosted
		}
	}
	if envValues == nil {
		// Resolve deployed metadata from azd environment.
		// Deployed name reflects the created resource.
		var envResponse *azdext.EnvironmentResponse
		var err error
		if resolutionOptions.environmentName != "" {
			envResponse, err = azdClient.Environment().Get(ctx, &azdext.GetEnvironmentRequest{
				Name: resolutionOptions.environmentName,
			})
			if err != nil {
				return info, fmt.Errorf("getting environment %q for agent service %q: %w",
					resolutionOptions.environmentName, svc.Name, err)
			}
		} else {
			envResponse, err = azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		}
		if err != nil {
			if resolutionOptions.allowBrownfieldInlineName {
				return info, fmt.Errorf(
					"getting current environment for agent service %q: %w",
					svc.Name,
					err,
				)
			}
			return info, nil
		}
		if envResponse == nil || envResponse.Environment == nil ||
			envResponse.Environment.Name == "" {
			if resolutionOptions.allowBrownfieldInlineName || resolutionOptions.environmentName != "" {
				return info, fmt.Errorf(
					"current environment is not available for agent service %q",
					svc.Name,
				)
			}
			return info, nil
		}

		info.EnvironmentName = envResponse.Environment.Name
		values, err := getAgentEnvironmentValues(
			ctx,
			azdClient,
			info.EnvironmentName,
		)
		if err != nil {
			if resolutionOptions.includeProtocolEndpoints {
				return info, &agentProtocolEndpointsError{
					err: fmt.Errorf(
						"failed to read environment values for agent service %q: %w",
						svc.Name,
						err,
					),
				}
			}
			if resolutionOptions.allowBrownfieldInlineName || resolutionOptions.environmentName != "" {
				return info, fmt.Errorf(
					"reading environment %q for agent service %q: %w",
					envResponse.Environment.Name,
					svc.Name,
					err,
				)
			}
			log.Printf(
				"resolve agent service %q: failed to read environment values: %v",
				svc.Name,
				err,
			)
			return info, nil
		}
		envValues = values
	}

	serviceKey := toServiceKey(svc.Name)
	nameKey := fmt.Sprintf("AGENT_%s_NAME", serviceKey)
	versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)
	projectEndpointKey := envkey.AgentProjectEndpoint(svc.Name)

	switch {
	case strings.TrimSpace(envValues[nameKey]) != "":
		info.AgentName = strings.TrimSpace(envValues[nameKey])
	case resolutionOptions.allowBrownfieldInlineName:
		reference := brownfieldInlineAgentReference(
			svc,
			projectConfig,
			envValues,
		)
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
			if resolutionOptions.includeProtocolEndpoints {
				info.ProtocolEndpoints, info.ProtocolEndpointsPresent,
					info.ProtocolEndpointsStale, info.ProtocolEndpointsIncomplete =
					resolveAgentProtocolEndpointsFromValues(envValues, svc.Name)
			}
			return info, nil
		}
	}

	if version := strings.TrimSpace(envValues[versionKey]); version != "" {
		info.Version = version
	}

	endpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey)
	if endpoint := strings.TrimSpace(envValues[endpointKey]); endpoint != "" {
		info.AgentEndpoint = endpoint
	}
	if projectEndpoint := strings.TrimSpace(envValues[projectEndpointKey]); projectEndpoint != "" {
		info.ProjectEndpoint = projectEndpoint
	}
	if resolutionOptions.includeProtocolEndpoints {
		info.ProtocolEndpoints, info.ProtocolEndpointsPresent,
			info.ProtocolEndpointsStale, info.ProtocolEndpointsIncomplete =
			resolveAgentProtocolEndpointsFromValues(envValues, svc.Name)
	}

	return info, nil
}

// ServiceRunContext holds the resolved context needed for local development.
type ServiceRunContext struct {
	ServiceName           string            // the resolved service name (from azure.yaml)
	ProjectDir            string            // absolute path to the service source directory
	StartupCommand        string            // startupCommand from AdditionalProperties (may be empty)
	ServiceEnvironment    map[string]string // values already expanded by azd core
	HasServiceEnvironment bool              // service declares env: even when empty
	// Definition is the resolved hosted-agent definition from the azure.yaml
	// service entry or its explicit root $ref. It is nil for supported non-hosted kinds.
	Definition *agent_yaml.ContainerAgent
}

// resolveServiceRunContext queries the azd project to find the matching azure.ai.agent
// service, then returns the service's absolute source directory and startup command.
func resolveServiceRunContext(ctx context.Context, azdClient *azdext.AzdClient, name string, noPrompt bool) (*ServiceRunContext, error) {
	svc, project, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return nil, err
	}
	if err := projectpkg.ResolveServiceConfigInPlace(
		svc,
		project.Path,
	); err != nil {
		return nil, exterrors.ValidationFromError(
			err,
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to resolve agent service %s", svc.Name),
			"fix the agent service configuration in azure.yaml",
		)
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
	if agentConfig, cfgErr := projectpkg.LoadServiceTargetAgentConfig(
		svc,
	); cfgErr == nil {
		startupCmd = agentConfig.StartupCommand
	}
	projectpkg.WarnOrphanedConfigEnv(svc)

	var definition *agent_yaml.ContainerAgent
	def, isHosted, source, err := projectpkg.LoadHostedAgentDefinition(svc, project.Path)
	if err != nil {
		return nil, exterrors.ValidationFromError(
			err,
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to load agent definition for service %s", svc.Name),
			"fix the agent service configuration in azure.yaml",
		)
	}
	if isHosted {
		definition = &def
	}
	if source.IsLegacy() {
		projectpkg.WarnLegacyAgentShape(source)
	}

	// A read failure must not be read as "no env: declared":
	// that silently reopens the full azd environment fallback
	// below. A missing env: is reported as Found=false with no
	// error, so only real failures land here.
	hasServiceEnvironment, err := serviceEnvDeclared(
		ctx,
		azdClient.Project(),
		svc.Name,
	)
	if err != nil {
		return nil, err
	}

	return &ServiceRunContext{
		ServiceName:           svc.Name,
		ProjectDir:            projectDir,
		StartupCommand:        startupCmd,
		ServiceEnvironment:    svc.GetEnvironment(),
		HasServiceEnvironment: hasServiceEnvironment,
		Definition:            definition,
	}, nil
}

// serviceConfigReader reads raw azure.yaml service config values.
// It mirrors the same seam in the routines and toolboxes targets
// so all three read a declared env: the same way.
type serviceConfigReader interface {
	GetServiceConfigValue(
		ctx context.Context,
		in *azdext.GetServiceConfigValueRequest,
		opts ...grpc.CallOption,
	) (*azdext.GetServiceConfigValueResponse, error)
}

// serviceEnvDeclared reports whether the service declares an env:
// block. An explicit empty env: {} declares an isolated scope,
// and core forwards it as an empty map, indistinguishable from an
// omitted env, so the raw config is the only way to tell them
// apart. Errors are returned rather than treated as "not
// declared", which would fall back to the full azd environment.
func serviceEnvDeclared(
	ctx context.Context,
	projectClient serviceConfigReader,
	serviceName string,
) (bool, error) {
	resp, err := projectClient.GetServiceConfigValue(ctx, &azdext.GetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "env",
	})
	if err != nil {
		return false, fmt.Errorf("reading env for service %q: %w", serviceName, err)
	}
	return resp.GetFound(), nil
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

// resolveAgentProtocol loads the service's agent definition and returns the
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
	if isServiceGateError(err) {
		return "", "", err
	}
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

	if err := voiceInvocationError(svc, proj.Path); err != nil {
		return "", "", err
	}
	hosted, isHosted, source, err := projectpkg.LoadHostedAgentDefinition(svc, proj.Path)
	if err != nil {
		return "", "", exterrors.ValidationFromError(
			err,
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("could not resolve the agent definition for %s", svc.Name),
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

func resolveAgentInvocableProtocols(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
) ([]agent_api.AgentProtocol, error) {
	svc, proj, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if isServiceGateError(err) {
		return nil, err
	}
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"could not resolve agent service in azd project: %s",
				err,
			),
			"run from your project directory and ensure "+
				"azure.yaml contains an azure.ai.agent service",
		)
	}

	hosted, isHosted, source, err := projectpkg.LoadHostedAgentDefinition(svc, proj.Path)
	if err != nil {
		return nil, exterrors.ValidationFromError(
			err,
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("could not resolve the agent definition for %s", svc.Name),
			"ensure the agent definition is present in azure.yaml or "+
				"run `azd ai agent init`",
		)
	}
	if source.IsLegacy() {
		projectpkg.WarnLegacyAgentShape(source)
	}
	if !isHosted {
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("agent service %s is not a hosted agent", svc.Name),
			"only hosted agents can be invoked",
		)
	}

	var protocols []agent_api.AgentProtocol
	seen := make(map[agent_api.AgentProtocol]struct{})
	for _, rec := range hosted.Protocols {
		protocol := agent_api.AgentProtocol(strings.TrimSpace(rec.Protocol))
		if protocol == "" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"agent definition declares a protocol entry with an "+
					"empty protocol field",
				"set a non-empty protocol value in the agent definition",
			)
		}
		if protocol.IsInvocable() {
			if _, ok := seen[protocol]; ok {
				continue
			}
			seen[protocol] = struct{}{}
			protocols = append(protocols, protocol)
		}
	}
	return protocols, nil
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
				"the agent definition declares a protocol entry, "+
					"but its protocol field is empty",
				"set a non-empty protocol value in the azure.yaml agent service",
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
				"the agent definition declares only non-invocable protocols: %s",
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
			"the agent definition declares multiple protocols (%s)",
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
