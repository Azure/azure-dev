// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	osExec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azdignore"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type initFlags struct {
	projectResourceId string
	modelDeployment   string
	model             string
	manifestPointer   string
	agentName         string
	src               string
	env               string
	protocols         []string
	// deploy mode flags for non-interactive code deploy support
	deployMode    string // "container" or "code"; empty = prompt interactively
	runtime       string // e.g. "python_3_13", "python_3_14", "dotnet_10"
	entryPoint    string // e.g. "app.py", "MyAgent.dll"
	depResolution string // "remote_build" or "bundled"; defaults to "remote_build"
	// image specifies a pre-built container image URL (e.g., "myacr.azurecr.io/agent:v1").
	// When set without --manifest, init synthesizes a minimal hosted container manifest and
	// routes through the manifest flow, skipping template/language selection and code
	// scaffolding (there is no source to scaffold). The image is written to the generated
	// azure.yaml service's top-level image field, skips Dockerfile generation, and skips ACR
	// connection prompts. Requires --agent-name when no --manifest is given. Incompatible
	// with --deploy-mode code.
	image string
	// force, when true, lets headless callers (--no-prompt) pre-consent to
	// overwrite prompts that would otherwise return a structured error. It
	// mirrors the `--force` convention used by `azd down`, `azd env remove`,
	// `azd config reset`, and `azd infra generate`.
	force bool
	// noPrompt is resolved from the extension context (--no-prompt / AZD_NO_PROMPT)
	// and is not registered as a CLI flag on the init command itself.
	noPrompt bool
	// infra selects the IaC flavor to eject from azure.yaml into ./infra/.
	// Empty means the flag was not passed (bicepless default, no files). A bare
	// `--infra` resolves to "bicep" via the flag's NoOptDefVal; `--infra=terraform`
	// and `--infra=bicep` are explicit. The eject runs after a fresh init or
	// standalone when azure.yaml already exists.
	infra string
}

// AiProjectResourceConfig represents the configuration for an AI project resource
type AiProjectResourceConfig struct {
	Models []map[string]any `json:"models,omitempty"`
}

type InitAction struct {
	azdClient *azdext.AzdClient
	//azureClient       *azure.AzureClient
	azureContext *azdext.AzureContext
	//composedResources []*azdext.ComposedResource
	console       input.Console
	credential    azcore.TokenCredential
	projectConfig *azdext.ProjectConfig
	environment   *azdext.Environment
	flags         *initFlags
	models        *modelSelector

	deploymentDetails    []project.Deployment
	containerSettings    *project.ContainerSettings
	isCodeDeploy         bool // true when user selects code deploy mode; skips ACR config
	httpClient           *http.Client
	serviceNameOverride  string // when set, addToProject uses this instead of the manifest name
	createdFolderDisplay string // pre-computed relative display path for the created folder

	// selectedFoundryProject holds the existing Foundry project resolved during
	// init (nil when creating a new project). It carries NetworkInjected so
	// addToProject can disable remote build for VNET-injected accounts
	// without issuing a second account read.
	selectedFoundryProject *FoundryProjectInfo

	// userProvidedManifest is true when the init flow is driven by a manifest —
	// either explicitly via the -m flag/positional argument, or when the user
	// interactively selects a template that resolves to a manifest. When true,
	// the init flow applies opinionated defaults to minimize interactive prompts.
	userProvidedManifest bool
}

// skipACR returns true when ACR provisioning and configuration should be skipped.
// This happens when:
// - Code deploy mode is selected (ZIP upload, no container build)
// - Pre-built image is provided via --image flag (user manages their own registry)
func (a *InitAction) skipACR() bool {
	return a.isCodeDeploy || a.flags.image != ""
}

// modelSelector encapsulates the dependencies needed for model selection and
// deployment resolution during init. It avoids constructing partial InitAction
// structs when only the model-selection call chain is needed.
type modelSelector struct {
	azdClient    *azdext.AzdClient
	azureContext *azdext.AzureContext
	environment  *azdext.Environment
	flags        *initFlags

	modelCatalog         map[string]*azdext.AiModel
	locationWarningShown bool

	// allDeployments holds existing deployments in the selected Foundry project.
	// Populated by getModelDeploymentDetails so getModelDetails can offer
	// "Use an existing deployment" alongside "Choose a different model".
	allDeployments []FoundryDeploymentInfo
}

func (a *InitAction) getModelSelector() *modelSelector {
	if a.models == nil {
		a.models = &modelSelector{
			azdClient:    a.azdClient,
			azureContext: a.azureContext,
			environment:  a.environment,
			flags:        a.flags,
		}
	}
	return a.models
}

// GitHubUrlInfo holds parsed information from a GitHub URL
type GitHubUrlInfo struct {
	RepoSlug string
	Branch   string
	FilePath string
	Hostname string
}

const AiAgentHost = "azure.ai.agent"
const agentsV2ModelCapability = "agentsV2"

// checkAiModelServiceAvailable is a temporary check to ensure the azd host supports
// required gRPC services. Remove once azd core enforces requiredAzdVersion.
func checkAiModelServiceAvailable(ctx context.Context, azdClient *azdext.AzdClient) error {
	_, err := azdClient.Ai().ListModels(ctx, &azdext.ListModelsRequest{})
	if err == nil {
		return nil
	}

	if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
		return exterrors.Compatibility(
			exterrors.CodeIncompatibleAzdVersion,
			"this version of the azure.ai.agents extension is incompatible with your installed version of azd.",
			"upgrade azd to the latest version (https://aka.ms/azd/upgrade) and retry",
		)
	}

	return nil
}

// ensureLoggedIn verifies that the user is authenticated before any file-modifying
// operations take place.
//
// We need to parse the JSON output of `azd auth status --output json` because the
// Workflow API's Run method returns EmptyResponse and does not expose command output,
// and `azd auth status` always exits 0 regardless of authentication state — it reports
// the result in its output, not via its exit code or a gRPC error.
// If the Workflow API is extended to return structured command results in the future,
// this subprocess workaround can be replaced with a Workflow API call.
//
// getAuthStatusJSON is the function that runs the command and returns stdout. Production
// callers pass authStatusFromCLI; tests inject a stub.
func ensureLoggedIn(ctx context.Context, getAuthStatusJSON func(ctx context.Context) ([]byte, error)) error {
	out, err := getAuthStatusJSON(ctx)

	// Context cancellation / deadline always takes priority.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Try to parse whatever output we got, even if the command returned a non-zero
	// exit code (ExitError). azd auth status writes JSON to stdout regardless of
	// exit code, so the output may still be usable.
	if len(out) > 0 {
		authStatus, parseErr := parseAuthStatusJSON(out)
		if parseErr == nil {
			if authStatus == "unauthenticated" {
				return exterrors.Auth(
					exterrors.CodeNotLoggedIn,
					"not logged in",
					"run `azd auth login` to authenticate before running init",
				)
			}

			if authStatus == "authenticated" {
				return nil
			}

			// Unrecognized status value — fall through to best-effort skip.
		}
	}

	// No usable output. If the command itself failed, log and skip so unrelated
	// issues (azd not in PATH, network blips) don't block init.
	if err != nil {
		log.Printf("auth status check skipped: %v", err)
	}

	return nil
}

// authStatusFromCLI runs `azd auth status --output json --no-prompt` as a subprocess
// and returns the raw stdout bytes.
func authStatusFromCLI(ctx context.Context) ([]byte, error) {
	return osExec.CommandContext(ctx, "azd", "auth", "status", "--output", "json", "--no-prompt").Output()
}

// parseAuthStatusJSON extracts the "status" field from `azd auth status --output json`.
func parseAuthStatusJSON(data []byte) (string, error) {
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal auth status: %w", err)
	}
	if result.Status == "" {
		return "", fmt.Errorf("missing \"status\" field in auth status output")
	}
	return result.Status, nil
}

func resolveInitAgentName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	defaultName string,
) (string, error) {
	if flags.agentName != "" {
		return validateInitAgentName(flags.agentName)
	}

	defaultName, err := validateInitAgentName(defaultName)
	if err != nil {
		return "", err
	}

	if flags.noPrompt {
		return defaultName, nil
	}

	for {
		promptResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      "Enter a name for your agent",
				DefaultValue: defaultName,
				HelpMessage: "Foundry agents are unique by name within a project. " +
					"Reusing a name creates a new version of the existing agent.",
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return "", exterrors.Cancelled("agent name prompt was cancelled")
			}
			return "", exterrors.FromPrompt(err, "failed to prompt for agent name")
		}

		agentName := strings.TrimSpace(promptResp.Value)
		if agentName == "" {
			agentName = defaultName
		}

		validName, err := validateInitAgentName(agentName)
		if err != nil {
			writeValidationRetryError(err)
			continue
		}

		return validName, nil
	}
}

// resolveAgentNameFromManifestPointer resolves the agent name BEFORE any
// project folder is created so the project folder, the agent identity, the
// service entry, the src subfolder, and the post-init "cd" hint all use the
// same name.
//
// Resolution order:
//   - If --agent-name is set, that value is used (validated) and pinned.
//   - Else, peek the manifest's `name` field to seed the prompt default.
//     If peek succeeds, prompt the user (or use the default in --no-prompt mode)
//     and pin the result.
//   - Else (peek failed: unsupported URL form, parse error, missing name),
//     return "" so the caller falls back to today's defer behavior, which
//     leaves name resolution to the inner downloadAgentYaml flow once the
//     fully-loaded manifest is available.
//
// When a name is resolved, flags.agentName is set so the inner
// resolveInitAgentName call short-circuits without re-prompting the user.
func resolveAgentNameFromManifestPointer(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	manifestPointer string,
	httpClient *http.Client,
) (string, error) {
	if flags.agentName != "" {
		validated, err := validateInitAgentName(flags.agentName)
		if err != nil {
			return "", err
		}
		flags.agentName = validated
		return validated, nil
	}

	peeked := peekManifestName(ctx, manifestPointer, httpClient)
	if peeked == "" {
		// Defer to the inner flow which has access to the fully-loaded manifest.
		return "", nil
	}

	resolved, err := resolveInitAgentName(ctx, azdClient, flags, peeked)
	if err != nil {
		return "", err
	}
	// Pin so the inner resolveInitAgentName call is a no-op.
	flags.agentName = resolved
	return resolved, nil
}

func validateInitAgentName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := agent_yaml.ValidateAgentName(name); err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			fmt.Sprintf("invalid agent name %q: %s", name, err),
			"choose a 1-63 character name that starts and ends with a letter or number "+
				"and contains only letters, numbers, and internal hyphens",
		)
	}

	return name, nil
}

func agentNameFromTemplate(template any) (string, error) {
	var agentName string
	_, err := updateAgentDefinition(template, func(def *agent_yaml.AgentDefinition) {
		agentName = def.Name
	})
	if err != nil {
		return "", err
	}

	return agentName, nil
}

func setAgentNameOnTemplate(agentManifest *agent_yaml.AgentManifest, agentName string) error {
	if agentManifest == nil {
		return fmt.Errorf("agent manifest is nil")
	}

	template, err := updateAgentDefinition(agentManifest.Template, func(def *agent_yaml.AgentDefinition) {
		def.Name = agentName
	})
	if err != nil {
		return err
	}

	agentManifest.Template = template
	return nil
}

// absolutizeRelativeManifestPaths converts the -m manifest pointer to absolute
// when it refers to a local path so it remains valid after ensureProject
// changes into a newly created project directory. URLs and already-absolute
// paths are left unchanged. Errors here are surfaced because they indicate a
// problem the user can fix (e.g. invalid pathname).
//
// Note: flags.src is intentionally left unchanged. It is the output target
// for the downloaded agent definition (defaults to src/<agent-id> inside the
// project). InitAction.Run rewrites absolute --src values relative to the
// project root via filepath.Rel; converting a user-supplied relative --src
// to absolute before ensureProject changes into the new project folder would
// cause that rewrite to produce a "..\<src>" path that escapes the project
// directory.
func absolutizeRelativeManifestPaths(flags *initFlags) error {
	if flags.manifestPointer == "" {
		return nil
	}
	if strings.HasPrefix(flags.manifestPointer, "http://") ||
		strings.HasPrefix(flags.manifestPointer, "https://") {
		return nil
	}
	if filepath.IsAbs(flags.manifestPointer) {
		return nil
	}

	abs, err := filepath.Abs(flags.manifestPointer)
	if err != nil {
		return fmt.Errorf("resolve manifest path: %w", err)
	}
	flags.manifestPointer = abs
	return nil
}

func folderNameStrippingParenSuffix(title string) string {
	if idx := strings.IndexByte(title, '('); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}
	return sanitizeAgentName(title)
}

// peekManifestName makes a best-effort attempt to read just the top-level
// "name" field from an agent manifest at the given pointer. It is used by the
// -m flow to derive a project folder name before the full manifest is loaded
// inside InitAction.Run. Any failure (read error, parse error, missing name,
// unsupported pointer type) returns an empty string, leaving the caller to
// choose a conservative fallback. Errors are logged at debug level only — the
// authoritative download/parse happens later in downloadAgentYaml and surfaces
// the real diagnostic to the user.
//
// Supported pointer types:
//   - local file paths (read via os.ReadFile)
//   - GitHub URLs in the form recognized by parseGitHubUrlNaive (fetched via
//     plain HTTP against the contents API, no `gh` CLI required)
//
// Other URL forms (private GitHub repos that need `gh` auth, non-GitHub URLs)
// return "" — the caller falls back to not creating a subdirectory in that
// case.
func peekManifestName(ctx context.Context, manifestPointer string, httpClient *http.Client) string {
	if manifestPointer == "" {
		return ""
	}

	content, ok := readManifestContentForPeek(ctx, manifestPointer, httpClient)
	if !ok {
		return ""
	}

	var head struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(content, &head); err != nil {
		log.Printf("peek manifest name: parse: %v", err)
		return ""
	}
	return strings.TrimSpace(head.Name)
}

// readManifestContentForPeek returns the raw manifest bytes for a best-effort
// peek. It mirrors the local-file and naive-GitHub-URL fast paths of
// downloadAgentYaml without invoking the `gh` CLI. Returns ok=false on any
// failure or unsupported pointer type.
func readManifestContentForPeek(
	ctx context.Context, manifestPointer string, httpClient *http.Client,
) ([]byte, bool) {
	// Local file path: bypass URL handling entirely so a relative path like
	// "agent.yaml" that happens to look URL-ish is still read from disk.
	if !strings.HasPrefix(manifestPointer, "http://") && !strings.HasPrefix(manifestPointer, "https://") {
		info, statErr := os.Stat(manifestPointer)
		if statErr != nil || info.IsDir() {
			return nil, false
		}
		//nolint:gosec // manifest path is an explicit user-provided local path; same trust model as downloadAgentYaml
		content, err := os.ReadFile(manifestPointer)
		if err != nil {
			log.Printf("peek manifest name: read %s: %v", manifestPointer, err)
			return nil, false
		}
		return content, true
	}

	// GitHub URL: try the same naive parse + unauthenticated HTTP GET used
	// inside downloadAgentYaml for public repositories.
	urlInfo := parseGitHubUrlNaive(manifestPointer)
	if urlInfo == nil {
		return nil, false
	}
	if httpClient == nil {
		return nil, false
	}

	fileApiUrl := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
	if urlInfo.Branch != "" {
		fileApiUrl += "?ref=" + url.QueryEscape(urlInfo.Branch)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileApiUrl, nil)
	if err != nil {
		log.Printf("peek manifest name: request: %v", err)
		return nil, false
	}
	req.Header.Set("Accept", "application/vnd.github.v3.raw")

	//nolint:gosec // URL is constrained to the GitHub contents API built from a parsed GitHub URL
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("peek manifest name: http: %v", err)
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("peek manifest name: http status %d", resp.StatusCode)
		return nil, false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("peek manifest name: read body: %v", err)
		return nil, false
	}
	return body, true
}

// parseGitHubUrlNaive mirrors (*InitAction).parseGitHubUrlNaive for callers
// that do not have an InitAction available (e.g. peekManifestName, which runs
// before the action is constructed). The receiver-bound version is kept in
// place so the existing downloadAgentYaml code path is untouched.
func parseGitHubUrlNaive(manifestPointer string) *GitHubUrlInfo {
	parsedURL, err := url.Parse(manifestPointer)
	if err != nil {
		return nil
	}

	if parsedURL.Host == "github.com" && strings.Contains(parsedURL.Path, "/blob/") {
		parts := strings.SplitN(parsedURL.Path, "/blob/", 2)
		if len(parts) != 2 {
			return nil
		}
		repoSlug := strings.TrimPrefix(parts[0], "/")
		branch, filePath, ok := strings.Cut(parts[1], "/")
		if !ok || strings.Contains(branch, "/") {
			return nil
		}
		return &GitHubUrlInfo{
			RepoSlug: repoSlug,
			Branch:   branch,
			FilePath: filePath,
			Hostname: "github.com",
		}
	}

	if parsedURL.Host == "raw.githubusercontent.com" {
		pathPart := strings.TrimPrefix(parsedURL.Path, "/")
		parts := strings.SplitN(pathPart, "/", 3)
		if len(parts) < 3 {
			return nil
		}
		repoSlug := parts[0] + "/" + parts[1]
		if rest, ok := strings.CutPrefix(parts[2], "refs/heads/"); ok {
			branch, filePath, ok := strings.Cut(rest, "/")
			if !ok || strings.Contains(branch, "/") {
				return nil
			}
			return &GitHubUrlInfo{
				RepoSlug: repoSlug,
				Branch:   branch,
				FilePath: filePath,
				Hostname: "github.com",
			}
		}
	}

	return nil
}

func updateAgentDefinition(
	template any,
	update func(*agent_yaml.AgentDefinition),
) (any, error) {
	switch t := template.(type) {
	case agent_yaml.ContainerAgent:
		update(&t.AgentDefinition)
		return t, nil
	case *agent_yaml.ContainerAgent:
		if t == nil {
			return nil, fmt.Errorf("agent template is nil")
		}
		update(&t.AgentDefinition)
		return t, nil
	case agent_yaml.Workflow:
		update(&t.AgentDefinition)
		return t, nil
	case *agent_yaml.Workflow:
		if t == nil {
			return nil, fmt.Errorf("agent template is nil")
		}
		update(&t.AgentDefinition)
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported agent template type %T", template)
	}
}

// preBuiltImageForInit returns the pre-built image that should be written to the
// azure.yaml service image field. The --image flag wins over any image carried by
// a user-provided manifest because it is the explicit CLI override.
func preBuiltImageForInit(agentManifest *agent_yaml.AgentManifest, flagImage string) string {
	if image := strings.TrimSpace(flagImage); image != "" {
		return image
	}
	if agentManifest == nil {
		return ""
	}
	ca, ok := agentManifest.Template.(agent_yaml.ContainerAgent)
	if !ok {
		return ""
	}
	return strings.TrimSpace(ca.Image)
}

// synthesizeImageManifestFile writes a minimal hosted container agent manifest to a
// temporary file for the bring-your-own-image flow (--image without --manifest).
// Routing through the manifest path lets init skip template/language selection and code
// scaffolding, since a pre-built image needs none of those. The returned cleanup removes
// the temp directory; callers should defer it. The image is intentionally not embedded
// in the temporary manifest; init writes --image to the generated azure.yaml service's
// top-level image field.
func synthesizeImageManifestFile(agentName, image string) (string, func(), error) {
	noop := func() {}

	tmpDir, err := os.MkdirTemp("", "azd-agent-image-")
	if err != nil {
		return "", noop, fmt.Errorf("creating temp directory for synthesized manifest: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	doc := map[string]any{
		"name": agentName,
		"template": map[string]any{
			"kind":        string(agent_yaml.AgentKindHosted),
			"name":        agentName,
			"description": fmt.Sprintf("Hosted container agent using pre-built image %s", image),
			"protocols": []map[string]any{
				{"protocol": "responses", "version": "2.0.0"},
			},
		},
	}

	content, err := yaml.Marshal(doc)
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("marshaling synthesized manifest: %w", err)
	}

	manifestPath := filepath.Join(tmpDir, "agent.yaml")
	if err := os.WriteFile(manifestPath, content, osutil.PermissionFile); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("writing synthesized manifest: %w", err)
	}

	return manifestPath, cleanup, nil
}

func nextAgentNameSuggestion(agentName string) string {
	const maxAgentNameLength = 63
	const defaultAgentName = "agent"

	base := strings.TrimRight(agentName, "-")
	suffixNumber := "2"
	if dashIndex := strings.LastIndex(base, "-"); dashIndex >= 0 && dashIndex < len(base)-1 {
		if candidate := base[dashIndex+1:]; isDecimalString(candidate) {
			suffixNumber = incrementDecimalString(candidate)
			base = strings.TrimRight(base[:dashIndex], "-")
		}
	}

	suffix := "-" + suffixNumber
	if len(suffix) >= maxAgentNameLength {
		suffix = "-2"
	}

	maxBaseLength := maxAgentNameLength - len(suffix)
	if len(base) > maxBaseLength {
		base = strings.TrimRight(base[:maxBaseLength], "-")
	}
	if base == "" {
		base = defaultAgentName
		if len(base) > maxBaseLength {
			base = base[:maxBaseLength]
		}
	}

	return base + suffix
}

func isDecimalString(value string) bool {
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}

	return value != ""
}

func incrementDecimalString(value string) string {
	digits := []byte(value)
	for i := len(digits) - 1; i >= 0; i-- {
		if digits[i] < '9' {
			digits[i]++
			return string(digits)
		}
		digits[i] = '0'
	}

	return "1" + string(digits)
}

func resolveExistingAgentNameConflict(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	environment *azdext.Environment,
	credential azcore.TokenCredential,
	noPrompt bool,
	agentName string,
) (string, error) {
	if azdClient == nil || environment == nil || environment.Name == "" || credential == nil {
		return agentName, nil
	}

	endpointResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: environment.Name,
		Key:     "FOUNDRY_PROJECT_ENDPOINT",
	})
	if err != nil {
		log.Printf(
			"existing agent name check skipped: failed to read FOUNDRY_PROJECT_ENDPOINT for environment %q: %v",
			environment.Name,
			err,
		)
		return agentName, nil
	}
	if endpointResp == nil || endpointResp.Value == "" {
		log.Printf(
			"existing agent name check skipped: FOUNDRY_PROJECT_ENDPOINT is empty for environment %q",
			environment.Name,
		)
		return agentName, nil
	}

	agentClient := agent_api.NewAgentClient(endpointResp.Value, credential)
	return resolveExistingAgentNameConflictWithChecker(ctx, azdClient, agentClient, noPrompt, agentName)
}

func resolveExistingAgentNameConflictWithChecker(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentChecker agents.AgentChecker,
	noPrompt bool,
	agentName string,
) (string, error) {
	for {
		exists, err := agents.AgentExists(ctx, agentChecker, agentName, DefaultAgentAPIVersion)
		if err != nil {
			if exterrors.IsCancellation(err) || errors.Is(err, context.DeadlineExceeded) {
				return "", err
			}

			// This check is a convenience to warn the user about name conflicts; it should
			// never block init. Log a warning and continue with the requested name.
			fmt.Fprintf(os.Stderr, "%s", output.WithWarningFormat(
				"WARNING: unable to check whether agent %q already exists: %v\n",
				agentName,
				err,
			))
			return agentName, nil
		}
		if !exists {
			return agentName, nil
		}

		fmt.Fprintf(os.Stderr, "%s", agents.ExistingAgentWarning(agentName))
		if noPrompt {
			fmt.Fprintf(os.Stderr, "%s", output.WithGrayFormat(
				"To create a separate agent, re-run init with --agent-name <unique-name>.\n",
			))
			return agentName, nil
		}

		confirmResp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "Continue with this existing agent name?",
				DefaultValue: new(false),
				HelpMessage:  "Choose no to enter a different Foundry agent name.",
			},
		})
		if err != nil {
			return "", exterrors.FromPrompt(err, "failed to confirm existing agent name")
		}
		if confirmResp != nil && confirmResp.Value != nil && *confirmResp.Value {
			return agentName, nil
		}

		agentName, err = promptForReplacementAgentName(ctx, azdClient, agentName)
		if err != nil {
			return "", err
		}
	}
}

func promptForReplacementAgentName(ctx context.Context, azdClient *azdext.AzdClient, agentName string) (string, error) {
	for {
		promptResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      "Enter a different name for your agent",
				DefaultValue: nextAgentNameSuggestion(agentName),
				HelpMessage:  "Foundry agents are unique by name within a project.",
			},
		})
		if err != nil {
			return "", exterrors.FromPrompt(err, "failed to prompt for a different agent name")
		}

		nextName := strings.TrimSpace(promptResp.Value)
		if nextName == "" {
			nextName = nextAgentNameSuggestion(agentName)
		}

		validName, err := validateInitAgentName(nextName)
		if err != nil {
			writeValidationRetryError(err)
			continue
		}

		return validName, nil
	}
}

func writeValidationRetryError(err error) {
	if localErr, ok := errors.AsType[*azdext.LocalError](err); ok && localErr.Suggestion != "" {
		fmt.Fprintf(
			os.Stderr,
			"%s\n%s\n",
			output.WithErrorFormat(localErr.Message),
			output.WithGrayFormat(localErr.Suggestion),
		)
		return
	}

	fmt.Fprintf(os.Stderr, "%s\n", output.WithErrorFormat(err.Error()))
}

// runInitFromManifest sets up Azure context, credentials, console, and runs the
// InitAction for a given manifest pointer. This is the shared code path used when
// initializing from a manifest URL/path (the -m flag, agent template, or azd template
// that contains an agent manifest).
func runInitFromManifest(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	targetDir string,
	createdFolderDisplay string,
	userProvidedManifest bool,
) error {
	// Ensure project and environment exist (no subscription/location prompting yet)
	projectConfig, err := ensureProject(ctx, flags, azdClient, targetDir)
	if err != nil {
		return err
	}

	// Get or create environment
	env := getExistingEnvironment(ctx, flags.env, azdClient)
	if env == nil {
		fmt.Println("Lets create a new default azd environment for your project.")
		env, err = createNewEnvironment(ctx, azdClient, flags.env)
		if err != nil {
			return err
		}
	}

	// Load whatever Azure context values already exist in the environment
	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return err
	}

	// Create credential with whatever tenant is available (may be empty → default tenant)
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   azureContext.Scope.TenantId,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
	}

	console := input.NewConsole(
		false, // noPrompt
		true,  // isTerminal
		input.Writers{Output: os.Stdout},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil, // formatter
		nil, // externalPromptCfg
	)

	action := &InitAction{
		azdClient:            azdClient,
		azureContext:         azureContext,
		console:              console,
		credential:           credential,
		projectConfig:        projectConfig,
		environment:          env,
		flags:                flags,
		httpClient:           httpClient,
		createdFolderDisplay: createdFolderDisplay,
		userProvidedManifest: userProvidedManifest,
	}

	return action.Run(ctx)
}

func newInitCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &initFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "init [<path>] [-m <manifest pointer>] [--src <source directory>]",
		Short: fmt.Sprintf("Initialize a new AI agent project. %s", color.YellowString("(Preview)")),
		Long: `Initialize a new AI agent project.

When -m points at a sample's unified azure.yaml (a project manifest that
declares services with host: azure.ai.project / azure.ai.agent / ...), that
azure.yaml is adopted as the project manifest and its referenced files are
placed at the project root. When -m points at an agent manifest instead, the
project's azure.yaml is generated from it.

The agent name written to agent.yaml is the Foundry agent identity. Foundry
agents are unique by name within a project, so deploying with an existing name
creates a new version of that existing agent instead of a separate agent.

Use --agent-name to choose a unique Foundry agent name when initializing from
a reusable sample or manifest.

A default .agentignore file is generated to control which files are excluded
from code-deploy ZIP packaging (uses .gitignore syntax).`,
		Example: `  # Adopt a sample's unified azure.yaml as the project manifest
  azd ai agent init -m ./azure.yaml
  azd ai agent init -m https://github.com/Azure-Samples/<repo>/blob/main/azure.yaml

  # Initialize from an agent manifest
  azd ai agent init -m ./agent.manifest.yaml

  # Initialize from a manifest with a unique Foundry agent name
  azd ai agent init -m ./agent.manifest.yaml --agent-name my-unique-agent

  # Initialize from local agent code
  azd ai agent init --src ./src/my-agent --agent-name my-unique-agent

  # Non-interactive code deploy (CI/CD)
  azd ai agent init --no-prompt --project-id "<resource-id>" \
    --deploy-mode code --runtime python_3_13 --entry-point app.py

  # Bring your own pre-built image (no template/language selection, Dockerfile, or ACR setup)
  azd ai agent init --no-prompt --agent-name my-agent \
    --image myacr.azurecr.io/agents/my-agent:v1`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.noPrompt = extCtx.NoPrompt
			if flags.env == "" {
				flags.env = extCtx.Environment
			}

			printBanner(cmd.OutOrStdout())

			// Resolve optional positional argument into --manifest or --src
			if len(args) == 1 {
				if err := applyPositionalArg(args[0], flags, cmd); err != nil {
					return err
				}
			}

			// Capture whether the user explicitly provided a manifest (via -m flag
			// or positional argument) BEFORE the auto-detection logic below may also
			// set flags.manifestPointer. This drives the opinionated-defaults path.
			userProvidedManifest := flags.manifestPointer != ""

			// Resolve the eject provider once (when --infra was passed) so an
			// invalid value fails fast regardless of whether azure.yaml exists
			// yet, and both the standalone and post-init eject paths agree.
			infraProvider := ""
			if flags.infra != "" {
				p, err := parseInfraProvider(flags.infra)
				if err != nil {
					return err
				}
				infraProvider = p
			}

			// `--infra` on a directory that already has an azd agent project
			// is a standalone eject: synthesize infra (Bicep or Terraform) from
			// the existing azure.yaml, write ./infra/, and return without
			// prompting.
			if infraProvider != "" && fileExists("azure.yaml") {
				// Reject inputs the eject path would silently ignore (a
				// positional arg, -m, or --src) instead of pretending they
				// were honored.
				if err := validateStandaloneEjectArgs(args, flags); err != nil {
					return err
				}
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve current directory: %w", err)
				}
				return ejectInfra(cwd, infraProvider)
			}

			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return exterrors.Internal(exterrors.CodeAzdClientFailed, fmt.Sprintf("failed to create azd client: %s", err))
			}
			defer azdClient.Close()

			if err := checkAiModelServiceAvailable(ctx, azdClient); err != nil {
				return err
			}

			// Wait for debugger if AZD_EXT_DEBUG is set
			if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
					return nil
				}
				return fmt.Errorf("failed waiting for debugger: %w", err)
			}

			if err := ensureLoggedIn(ctx, authStatusFromCLI); err != nil {
				return err
			}

			var httpClient = &http.Client{
				Timeout: 30 * time.Second,
			}

			// Track whether a project already exists so the cd hint is
			// only shown for brand-new top-level project folders, not
			// when a template adds a subfolder to an existing project.
			existingProject := fileExists("azure.yaml")

			// Bring-your-own-image fast path: when --image is set without a manifest,
			// there is no source to scaffold and no template/language to choose.
			// Synthesize a minimal hosted container manifest and route it through the
			// manifest flow, which skips the init-mode / template / language prompts
			// and code scaffolding. The image is wired into azure.yaml and ACR is
			// skipped by the existing --image handling in InitAction.Run.
			if flags.image != "" && flags.manifestPointer == "" {
				// Validate early so we fail before initializing a project/template.
				if err := validateImageFlag(flags.image, flags.deployMode); err != nil {
					return err
				}
				if flags.agentName == "" {
					return exterrors.Validation(
						exterrors.CodeInvalidParameter,
						"--image requires --agent-name when no --manifest is provided",
						"pass --agent-name <name> (or provide --manifest with the agent definition)",
					)
				}
				manifestPath, cleanup, err := synthesizeImageManifestFile(flags.agentName, flags.image)
				if err != nil {
					return err
				}
				defer cleanup()
				flags.manifestPointer = manifestPath
				// Treat the synthesized manifest as user-provided so deploy-mode
				// resolution auto-selects container without prompting.
				userProvidedManifest = true
			}

			// Auto-detect an existing agent manifest in the target directory
			// when no --manifest flag was provided.
			//
			// manifestDetectedButDeclined: gates the definition-reuse scan below so
			// a declined manifest is not re-discovered and mis-classified.
			manifestDetectedButDeclined := false
			if flags.manifestPointer == "" {
				checkDir := flags.src
				if checkDir == "" {
					checkDir = "."
				}
				detected, detectErr := detectLocalManifest(checkDir)
				if detectErr != nil {
					return fmt.Errorf("checking for existing manifest: %w", detectErr)
				}
				if detected != "" {
					useExisting := flags.noPrompt
					if !flags.noPrompt {
						confirmResp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
							Options: &azdext.ConfirmOptions{
								Message: fmt.Sprintf(
									"An existing agent manifest was found at %q. Use it?",
									detected,
								),
								DefaultValue: new(true),
							},
						})
						if promptErr != nil {
							if exterrors.IsCancellation(promptErr) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return fmt.Errorf("prompting for manifest detection: %w", promptErr)
						}
						useExisting = *confirmResp.Value
					}
					if useExisting {
						flags.manifestPointer = detected
						if flags.src == "" {
							flags.src = checkDir
						}
					} else {
						manifestDetectedButDeclined = true
					}
				}
			}

			// When no manifest was detected, look for a bare agent.yaml definition
			// to reuse (issue #7268). Skips the init-mode prompt and from-code
			// scaffolding. Bypassed when the user already declined a manifest above.
			if flags.manifestPointer == "" && !manifestDetectedButDeclined {
				checkDir := flags.src
				if checkDir == "" {
					checkDir = "."
				}
				existing, findErr := findExistingAgentYaml(checkDir)
				if findErr != nil {
					return findErr
				}
				if existing != "" {
					useExisting := flags.noPrompt
					if !flags.noPrompt {
						confirmResp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
							Options: &azdext.ConfirmOptions{
								Message: fmt.Sprintf(
									"An existing agent definition was found at %q. Use it?",
									existing,
								),
								DefaultValue: new(true),
							},
						})
						if promptErr != nil {
							if exterrors.IsCancellation(promptErr) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return fmt.Errorf("prompting for definition reuse: %w", promptErr)
						}
						useExisting = *confirmResp.Value
					}
					if useExisting {
						if flags.src == "" {
							flags.src = checkDir
						}
						return runReuseDefinition(ctx, flags, azdClient, httpClient, checkDir, existing)
					}
				}
			}

			if flags.manifestPointer != "" {
				// Fail fast when the user accidentally passes a directory
				// instead of a manifest file — before downloading templates.
				if err := checkNotDirectory(flags.manifestPointer); err != nil {
					return err
				}

				// Detect whether the pointer is a unified Foundry azure.yaml
				// (adopt it as the project manifest) versus an agent manifest
				// (generate the project). For private GitHub URLs, the detector
				// falls back to the authenticated gh CLI download path before
				// deciding whether this is a unified azure.yaml. See #8798.
				if content, ok := readManifestContentForInitDetection(
					ctx, azdClient, flags.manifestPointer, httpClient,
				); ok && looksLikeFoundryAzureYaml(content) {
					if err := runInitFromAzureYaml(ctx, flags, azdClient, httpClient, content); err != nil {
						if exterrors.IsCancellation(err) {
							return exterrors.Cancelled("initialization was cancelled")
						}
						return err
					}
					return nil
				}

				// Resolve the agent name BEFORE creating the project folder
				// so the folder, the agent identity, the service entry, and
				// the cd hint all use the same user-chosen name. Peeking the
				// manifest seeds the prompt default; an explicit --agent-name
				// flag wins outright. See resolveAgentNameFromManifestPointer.
				resolvedName, err := resolveAgentNameFromManifestPointer(
					ctx, azdClient, flags, flags.manifestPointer, httpClient,
				)
				if err != nil {
					if exterrors.IsCancellation(err) {
						return exterrors.Cancelled("initialization was cancelled")
					}
					return err
				}

				// Mirror the template flow (#8210) and create a project folder
				// derived from the resolved agent name. When the peek failed
				// AND no --agent-name was provided (resolvedName == ""), fall
				// back to the prior behavior of initializing in the current
				// directory so we never leave the user with an empty folder +
				// starter project after a downloadAgentYaml failure.
				targetDir := "."
				var folderDisplay string
				if resolvedName != "" {
					folderName := sanitizeAgentName(resolvedName)
					// Make a local relative manifest path absolute before
					// ensureProject changes into the new project directory,
					// otherwise downloadAgentYaml will look for the manifest
					// in the wrong place. flags.src is left as-is (see
					// absolutizeRelativeManifestPaths comment for why).
					if err := absolutizeRelativeManifestPaths(flags); err != nil {
						return err
					}

					// When the manifest lives in the current directory, the agent
					// source code is already here — treat it like --from-code and
					// initialize in-place rather than copying files into a new
					// subdirectory. The existing isSamePath guard in copyDirectory
					// will skip the copy when src and dst resolve to the same path.
					manifestInCwd := false
					if isLocalFilePath(flags.manifestPointer) {
						if cwd, cwdErr := os.Getwd(); cwdErr == nil {
							manifestInCwd = isSamePath(filepath.Dir(flags.manifestPointer), cwd)
						}
					}

					if !manifestInCwd {
						_, statErr := os.Stat(folderName)
						newlyCreated := errors.Is(statErr, fs.ErrNotExist)
						targetDir = folderName
						if newlyCreated && !existingProject {
							folderDisplay = filepath.ToSlash(folderName)
						}
					} else if flags.src == "" {
						flags.src = "."
					}
				}

				if err := runInitFromManifest(
					ctx, flags, azdClient, httpClient, targetDir, folderDisplay, userProvidedManifest,
				); err != nil {
					if exterrors.IsCancellation(err) {
						return exterrors.Cancelled("initialization was cancelled")
					}
					return err
				}
			} else {
				// No manifest provided - prompt user for init mode
				initMode, err := promptInitMode(ctx, azdClient, flags.noPrompt)
				if err != nil {
					if exterrors.IsCancellation(err) {
						return exterrors.Cancelled("initialization was cancelled")
					}
					return err
				}

				switch initMode {
				case initModeTemplate:
					// User chose to start from a template - select one
					selectedTemplate, err := promptAgentTemplate(ctx, azdClient, httpClient, flags.noPrompt)
					if err != nil {
						if exterrors.IsCancellation(err) {
							return exterrors.Cancelled("initialization was cancelled")
						}
						return err
					}

					switch selectedTemplate.EffectiveType() {
					case TemplateTypeAzureYaml:
						// Unified azure.yaml template — download and adopt via
						// the Foundry adoption flow (not git clone).
						flags.manifestPointer = selectedTemplate.Source
						content, ok := readManifestContentForInitDetection(
							ctx, azdClient, flags.manifestPointer, httpClient,
						)
						if !ok {
							return exterrors.Dependency(
								exterrors.CodeProjectInitFailed,
								fmt.Sprintf(
									"failed to download template source: %s",
									selectedTemplate.Source,
								),
								"",
							)
						}

						// Resolve the agent name BEFORE creating the project
						// folder so the folder and agent identity use the same
						// name. Use the azure.yaml project name as the default,
						// falling back to the template title.
						defaultName := foundryProjectName(content)
						if defaultName == "" {
							defaultName = folderNameStrippingParenSuffix(selectedTemplate.Title)
						}

						resolvedName, err := resolveInitAgentName(ctx, azdClient, flags, defaultName)
						if err != nil {
							if exterrors.IsCancellation(err) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return err
						}

						if flags.src == "" && resolvedName != "" {
							flags.src = sanitizeAgentName(resolvedName)
						}

						if err := runInitFromAzureYaml(ctx, flags, azdClient, httpClient, content); err != nil {
							if exterrors.IsCancellation(err) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return err
						}

					default:
						// Agent manifest template - use existing -m flow.
						flags.manifestPointer = selectedTemplate.Source

						// Resolve the agent name BEFORE creating the project
						// folder so the folder, the agent identity, the service
						// entry, and the cd hint all use the same user-chosen
						// name. Peeking the template's manifest seeds the prompt
						// default; --agent-name wins outright.
						resolvedName, err := resolveAgentNameFromManifestPointer(
							ctx, azdClient, flags, selectedTemplate.Source, httpClient,
						)
						if err != nil {
							if exterrors.IsCancellation(err) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return err
						}

						// Prefer the resolved agent name for the project folder.
						// Fall back to the template title only when manifest peek
						// failed (e.g. unsupported URL form) AND no --agent-name
						// was provided.
						folderName := folderNameStrippingParenSuffix(selectedTemplate.Title)
						if resolvedName != "" {
							folderName = sanitizeAgentName(resolvedName)
						}
						// Check whether the target directory already exists so we
						// only report "created" when a new directory was made.
						_, statErr := os.Stat(folderName)
						newlyCreated := errors.Is(statErr, fs.ErrNotExist)
						var folderDisplay string
						if newlyCreated && !existingProject {
							folderDisplay = filepath.ToSlash(folderName)
						}
						if err := runInitFromManifest(
							ctx, flags, azdClient, httpClient, folderName, folderDisplay, true,
						); err != nil {
							if exterrors.IsCancellation(err) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return err
						}
					}

				default:
					// initModeFromCode - use existing code in current directory
					action := &InitFromCodeAction{
						azdClient:  azdClient,
						flags:      flags,
						httpClient: httpClient,
					}

					if err := action.Run(ctx); err != nil {
						if exterrors.IsCancellation(err) {
							return exterrors.Cancelled("initialization was cancelled")
						}
						return err
					}
				}
			}

			// New-project eject: when --infra is set on a fresh init that just
			// wrote azure.yaml, chain the eject step. Skip silently when init
			// didn't produce a foundry-bearing azure.yaml (cancelled or
			// non-foundry flow) to avoid a confusing "nothing to eject" error.
			if infraProvider != "" {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve current directory: %w", err)
				}
				//nolint:gosec // G304: azure.yaml in the current azd project directory
				rawYAML, readErr := os.ReadFile(filepath.Join(cwd, "azure.yaml"))
				switch {
				case errors.Is(readErr, fs.ErrNotExist):
					// Init didn't write azure.yaml; nothing to eject.
				case readErr != nil:
					return fmt.Errorf("read azure.yaml after init: %w", readErr)
				default:
					// Skip silently when no foundry service is present.
					if _, svcErr := findFoundryServiceForEject(rawYAML); svcErr == nil {
						if err := ejectInfra(cwd, infraProvider); err != nil {
							return err
						}
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.projectResourceId, "project-id", "p", "",
		"Existing Microsoft Foundry Project Id to initialize your azd environment with")

	cmd.Flags().StringVarP(&flags.modelDeployment, "model-deployment", "d", "",
		"Name of an existing model deployment to use from the Foundry project. Only used when paired with an existing Foundry project, either via --project-id or interactive prompts")

	cmd.Flags().StringVar(&flags.model, "model", "",
		"Name of the AI model to use (e.g., 'gpt-4o'). If not specified, defaults to 'gpt-4.1-mini'. Mutually exclusive with --model-deployment, with --model-deployment being used if both are provided")

	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "",
		"Path or URI to an agent manifest, or to a sample's unified azure.yaml to adopt as the project manifest")

	cmd.Flags().StringVar(&flags.agentName, "agent-name", "",
		"Foundry agent name to write to agent.yaml. Reusing a name creates a new version of the existing agent.")

	cmd.Flags().StringVarP(&flags.src, "src", "s", "",
		"Directory to download the agent definition to (defaults to 'src/<agent-id>')")

	cmd.Flags().StringSliceVar(&flags.protocols, "protocol", nil,
		"Protocols supported by the agent (e.g., 'responses', 'invocations', 'invocations_ws'). Can be specified multiple times.")

	cmd.Flags().StringVar(&flags.deployMode, "deploy-mode", "",
		"Deployment mode: 'container' (Docker image) or 'code' (ZIP upload). Defaults to 'code' for Python/.NET projects in --no-prompt.")

	cmd.Flags().StringVar(&flags.runtime, "runtime", "",
		"Runtime for code deploy (e.g., 'python_3_13', 'python_3_14', 'dotnet_10'). Required with --deploy-mode code --no-prompt.")

	cmd.Flags().StringVar(&flags.entryPoint, "entry-point", "",
		"Entry point file for code deploy (e.g., 'app.py', 'MyAgent.dll'). Required with --deploy-mode code --no-prompt.")

	cmd.Flags().StringVar(&flags.depResolution, "dep-resolution", "",
		"Dependency resolution for code deploy: 'remote_build' or 'bundled'. Defaults to 'remote_build'.")

	cmd.Flags().StringVar(&flags.image, "image", "",
		"Pre-built container image URL (e.g., 'myacr.azurecr.io/agent:v1'). "+
			"When set without --manifest, skips template/language selection, code scaffolding, "+
			"Dockerfile generation, and ACR setup, and requires --agent-name. "+
			"Incompatible with --deploy-mode code.")

	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Overwrite an input manifest that already lives inside the generated src tree without prompting. "+
			"Required together with --no-prompt when init would otherwise need confirmation.")

	cmd.Flags().StringVar(&flags.infra, "infra", "",
		"Eject infrastructure-as-code from azure.yaml into ./infra/. "+
			"A bare --infra ejects Bicep; --infra=terraform ejects Terraform and sets "+
			"infra.provider: terraform; --infra=bicep is explicit Bicep. "+
			"When azure.yaml already exists, runs as a standalone eject and skips the init prompts.")
	// NoOptDefVal makes a bare `--infra` resolve to "bicep" while still allowing
	// `--infra=terraform` / `--infra=bicep`. Absent flag stays "" (no eject).
	cmd.Flags().Lookup("infra").NoOptDefVal = project.BicepProviderName

	return cmd
}

func (a *InitAction) Run(ctx context.Context) error {

	// If src path is absolute, convert it to relative path compared to the azd project path
	if a.flags.src != "" && filepath.IsAbs(a.flags.src) {
		projectResponse, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return fmt.Errorf("failed to get project path: %w", err)
		}

		relPath, err := filepath.Rel(projectResponse.Project.Path, a.flags.src)
		if err != nil {
			return fmt.Errorf("failed to convert src path to relative path: %w", err)
		}
		a.flags.src = relPath
	}

	// Validate code deploy flags
	if err := a.validateCodeDeployFlags(); err != nil {
		return err
	}

	// If --manifest is given
	if a.flags.manifestPointer != "" {
		// Validate that the manifest pointer is either a valid URL or existing file path
		isValidURL := false
		isValidFile := false

		if _, err := url.ParseRequestURI(a.flags.manifestPointer); err == nil {
			isValidURL = true
		} else if _, fileErr := os.Stat(a.flags.manifestPointer); fileErr == nil {
			isValidFile = true
		}

		if !isValidURL && !isValidFile {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("agent manifest pointer is invalid: '%s' is neither a valid URI nor an existing file path", a.flags.manifestPointer),
				"provide a valid URL or an existing local agent.yaml/agent.yml path",
			)
		}

		// Catch the common mistake of passing a directory instead of a file
		if err := checkNotDirectory(a.flags.manifestPointer); err != nil {
			return err
		}

		// Download/read agent.yaml file from the provided URI or file path
		agentManifest, targetDir, err := a.downloadAgentYaml(ctx, a.flags.manifestPointer, a.flags.src)
		if err != nil {
			return fmt.Errorf("downloading agent.yaml: %w", err)
		}

		// Prompt for deploy mode (code vs container) for hosted agents.
		// Code deploy is supported for Python and .NET projects.
		if _, ok := agentManifest.Template.(agent_yaml.ContainerAgent); ok {
			showCodeDeploy := isPythonProject(targetDir) || isDotnetProject(targetDir)
			deployMode, err := promptDeployMode(ctx, a.azdClient, a.flags.noPrompt, showCodeDeploy, a.flags.deployMode, a.userProvidedManifest)
			if err != nil {
				return fmt.Errorf("prompting for deploy mode: %w", err)
			}
			a.isCodeDeploy = (deployMode == "code")

			if a.isCodeDeploy {
				// Prompt for code configuration and update the manifest
				codeConfig, err := promptCodeConfig(ctx, a.azdClient, targetDir, a.flags.noPrompt, codeDeployOptions{
					runtime:       a.flags.runtime,
					entryPoint:    a.flags.entryPoint,
					depResolution: a.flags.depResolution,
				}, a.userProvidedManifest)
				if err != nil {
					return fmt.Errorf("prompting for code configuration: %w", err)
				}

				// Remove container-only files only after configuration succeeds,
				// so a cancelled prompt or error doesn't leave the directory in
				// an inconsistent state. Only applies to GitHub-downloaded
				// templates to avoid deleting user-owned files.
				if a.isGitHubUrl(a.flags.manifestPointer) {
					removeContainerFiles(targetDir)
				}

				hostedAgent := agentManifest.Template.(agent_yaml.ContainerAgent)
				hostedAgent.CodeConfiguration = codeConfig
				agentManifest.Template = hostedAgent
			} else {
				// Container mode: ensure any pre-existing code_configuration is removed
				// (e.g. when switching from code deploy back to container)
				hostedAgent := agentManifest.Template.(agent_yaml.ContainerAgent)
				if hostedAgent.CodeConfiguration != nil {
					hostedAgent.CodeConfiguration = nil
					agentManifest.Template = hostedAgent
				}
			}
		}

		// Model configuration: prompt user for "use existing" vs "deploy new"
		agentManifest, err = a.configureModelChoice(ctx, agentManifest)
		if err != nil {
			return fmt.Errorf("configuring model choice: %w", err)
		}

		// For hosted agents, prompt for container resources before writing agent.yaml
		// so the selected values are persisted into the definition file.
		if hostedAgent, ok := agentManifest.Template.(agent_yaml.ContainerAgent); ok {
			containerSettings, err := a.populateContainerSettings(ctx, hostedAgent.Resources)
			if err != nil {
				return fmt.Errorf("failed to populate container settings: %w", err)
			}
			a.containerSettings = containerSettings

			// Update the agent definition with the selected resources
			hostedAgent.Resources = &agent_yaml.ContainerResources{
				Cpu:    containerSettings.Resources.Cpu,
				Memory: containerSettings.Resources.Memory,
			}
			agentManifest.Template = hostedAgent
		}

		// Prompt for manifest parameters (e.g. tool credentials) after project selection
		agentManifest, err = agent_yaml.ProcessManifestParameters(
			ctx, agentManifest, a.azdClient, a.flags.noPrompt,
		)
		if err != nil {
			return fmt.Errorf("failed to process manifest parameters: %w", err)
		}

		// Inject toolbox MCP endpoint env vars into hosted agent definitions
		// so agent.yaml is self-documenting about what env vars will be set.
		if err := injectToolboxEnvVarsIntoDefinition(agentManifest); err != nil {
			return fmt.Errorf("injecting toolbox env vars: %w", err)
		}

		agentName, err := agentNameFromTemplate(agentManifest.Template)
		if err != nil {
			return err
		}
		agentName, err = resolveExistingAgentNameConflict(
			ctx,
			a.azdClient,
			a.environment,
			a.credential,
			a.flags.noPrompt,
			agentName,
		)
		if err != nil {
			return err
		}
		if err := setAgentNameOnTemplate(agentManifest, agentName); err != nil {
			return err
		}

		// Generate .agentignore. The agent definition now lives in azure.yaml,
		// not in an on-disk agent.yaml, but .agentignore is still used to scope
		// code-deploy ZIP packaging.
		if err := writeAgentIgnoreFile(targetDir); err != nil {
			return fmt.Errorf("writing .agentignore: %w", err)
		}

		// Add the agent to the azd project (azure.yaml) services
		if err := a.addToProject(ctx, targetDir, agentManifest); err != nil {
			return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
		}

		// Run post-init validations (advisory warnings only)
		if ca, ok := agentManifest.Template.(agent_yaml.ContainerAgent); ok {
			validatePostInit(targetDir, ca.CodeConfiguration)
		}

		color.Green("\nAI agent definition added to your azd project successfully!")
	}

	return nil
}

func ensureProject(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	targetDir string,
) (*azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Let's get your project initialized.")

		envName := deriveEnvName(flags, targetDir)

		// Scaffold a minimal project via `azd init -t <empty dir> <targetDir>`.
		// We use an empty template dir rather than `--minimal` (which can't
		// take a positional target) or `-C` (a no-op on workflow re-entry,
		// since azd-core parses global flags once at startup). The empty
		// template skips the network call and produces just azure.yaml +
		// .azure/<env>/ + git init; writeFoundryProvider then stamps the
		// provider name onto azure.yaml.
		emptyTemplateDir, err := os.MkdirTemp("", "azd-foundry-empty-*")
		if err != nil {
			return nil, exterrors.Dependency(
				exterrors.CodeProjectInitFailed,
				fmt.Sprintf("creating empty template staging dir: %s", err),
				"check write permissions on the system temp directory",
			)
		}
		defer os.RemoveAll(emptyTemplateDir)

		if err := scaffoldProject(ctx, azdClient, targetDir, emptyTemplateDir, envName); err != nil {
			return nil, err
		}

		if err := writeFoundryProvider(ctx, azdClient); err != nil {
			return nil, err
		}

		projectResponse, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, exterrors.Dependency(
				exterrors.CodeProjectNotFound,
				fmt.Sprintf("failed to get project after initialization: %s", err),
				"",
			)
		}

		fmt.Println()
	} else if projectResponse.Project != nil {
		fmt.Println(output.WithGrayFormat(
			"Found existing azd project at %q. Adding agent to it.", projectResponse.Project.Path,
		))

		// Skip the warning when the project has already opted into the
		// extension's provisioning provider (which intentionally omits infra/).
		if !hasFoundryProviderDeclared(projectResponse.Project) {
			infraDir := filepath.Join(projectResponse.Project.Path, "infra")
			if _, statErr := os.Stat(infraDir); os.IsNotExist(statErr) {
				fmt.Printf("%s", output.WithWarningFormat(
					"No infra/ directory found in the project, and azure.yaml does not declare "+
						"'infra.provider: %s'. If you need Azure infrastructure for deployment, "+
						"set that provider in azure.yaml, or run "+
						"'azd ai agent init --infra' to generate an infra/ directory.\n",
					project.FoundryProviderName,
				))
			}
		}
	}

	if projectResponse.Project == nil {
		return nil, exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"project not found",
			"",
		)
	}

	return projectResponse.Project, nil
}

// deriveEnvName resolves the azd environment name for a new project: the
// explicit --environment flag when set, otherwise a sanitized name derived from
// the target folder (or the current directory when targetDir is ".").
func deriveEnvName(flags *initFlags, targetDir string) string {
	if flags.env != "" {
		return flags.env
	}

	envBase := targetDir
	if targetDir == "." {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			envBase = filepath.Base(cwd)
		}
	}
	base := sanitizeAgentName(envBase)
	if len(base) > 59 {
		base = strings.TrimRight(base[:59], "-")
	}
	return base + "-dev"
}

// scaffoldProject runs `azd init -t <templateDir> <targetDir> --environment
// <envName>` via the Workflow API, seeds best-effort salt/resource-group env
// vars, and changes the extension process into the new project directory.
//
// templateDir is the directory azd-core copies as the template: an empty
// staging dir when generating a project from an agent manifest, or a sample
// directory carrying a unified azure.yaml when adopting it (#8798). azd-core
// copies a local template directory wholesale and only writes azure.yaml when
// one is absent, so an adopted sample's azure.yaml lands at the project root
// unchanged.
func scaffoldProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	targetDir string,
	templateDir string,
	envName string,
) error {
	workflow := &azdext.Workflow{
		Name: "init",
		Steps: []*azdext.WorkflowStep{
			{Command: &azdext.WorkflowCommand{Args: []string{
				"init", "-t", templateDir, targetDir,
				"--environment", envName,
			}}},
		},
	}

	if _, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: workflow,
	}); err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("project initialization was cancelled")
		}
		return exterrors.Dependency(
			exterrors.CodeProjectInitFailed,
			fmt.Sprintf("failed to initialize project: %s", err),
			"",
		)
	}

	// Best-effort: generate a salt so uniqueString()-based resource names
	// differ across project recreations, and write a salted
	// AZURE_RESOURCE_GROUP so recreated projects get a fresh RG. If anything
	// fails the Bicep templates fall back to the original deterministic hash.
	salt := ensureResourceTokenSalt(ctx, azdClient, envName)
	ensureResourceGroupName(ctx, azdClient, envName, salt)

	// Sync the extension process into the new project directory so that
	// subsequent local file operations see the scaffolded project.
	if targetDir != "." {
		if chdirErr := os.Chdir(targetDir); chdirErr != nil {
			return fmt.Errorf(
				"changing to project directory %q: %w",
				targetDir, chdirErr,
			)
		}
	}

	return nil
}

// writeFoundryProvider stamps `infra.provider: <FoundryProviderName>`
// onto azure.yaml and removes the starter's `infra.path: ./infra`.
func writeFoundryProvider(ctx context.Context, azdClient *azdext.AzdClient) error {
	value, err := structpb.NewValue(project.FoundryProviderName)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeProjectInitFailed,
			fmt.Sprintf("failed to encode provider name as protobuf value: %s", err),
		)
	}

	_, err = azdClient.Project().SetConfigValue(ctx, &azdext.SetProjectConfigValueRequest{
		Path:  "infra.provider",
		Value: value,
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("writing infra.provider was cancelled")
		}
		return exterrors.Dependency(
			exterrors.CodeProjectInitFailed,
			fmt.Sprintf(
				"failed to set infra.provider=%s on azure.yaml: %s",
				project.FoundryProviderName, err,
			),
			"check that azure.yaml is writable and re-run the command",
		)
	}

	// UnsetConfig is idempotent for missing keys.
	_, err = azdClient.Project().UnsetConfig(ctx, &azdext.UnsetProjectConfigRequest{
		Path: "infra.path",
	})
	if err != nil && !exterrors.IsCancellation(err) {
		return exterrors.Dependency(
			exterrors.CodeProjectInitFailed,
			fmt.Sprintf("failed to unset infra.path on azure.yaml: %s", err),
			"check that azure.yaml is writable and re-run the command",
		)
	}

	return nil
}

// hasFoundryProviderDeclared reports whether azure.yaml already
// declares this extension's provisioning provider.
func hasFoundryProviderDeclared(proj *azdext.ProjectConfig) bool {
	if proj == nil || proj.Infra == nil {
		return false
	}
	return proj.Infra.Provider == project.FoundryProviderName
}

func getExistingEnvironment(ctx context.Context, envName string, azdClient *azdext.AzdClient) *azdext.Environment {
	var env *azdext.Environment
	if envName == "" {
		if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
			env = envResponse.Environment
		}
	} else {
		if envResponse, err := azdClient.Environment().Get(ctx, &azdext.GetEnvironmentRequest{
			Name: envName,
		}); err == nil {
			env = envResponse.Environment
		}
	}

	return env
}

// manifestHasModelResources returns true if the manifest contains any model resources
// that need deployment configuration (i.e. resources with kind "model").
func manifestHasModelResources(manifest *agent_yaml.AgentManifest) bool {
	if manifest.Resources != nil {
		for _, resource := range manifest.Resources {
			if _, ok := resource.(agent_yaml.ModelResource); ok {
				return true
			}
		}
	}

	return false
}

// configureModelChoice presents the "use existing / deploy new" model configuration choice
// and establishes the necessary Azure context (subscription, location, project) before
// ProcessModels is called. This defers subscription/location prompting until we know
// which path the user wants.
func (a *InitAction) configureModelChoice(
	ctx context.Context, agentManifest *agent_yaml.AgentManifest,
) (*agent_yaml.AgentManifest, error) {
	// When no --project-id flag was given, check whether the azd environment already
	// has a Foundry project configured from a previous init. If so, reuse it so the
	// user isn't prompted to select a project they already chose.
	if a.flags.projectResourceId == "" {
		if existing, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: a.environment.Name,
			Key:     "AZURE_AI_PROJECT_ID",
		}); err == nil && existing.Value != "" {
			a.flags.projectResourceId = existing.Value
			log.Printf("Reusing existing Foundry project from environment: %s", existing.Value)
			fmt.Println(output.WithGrayFormat(
				"Using Foundry project from environment: %s", existing.Value,
			))
		}
	}

	// If --project-id is provided (or reused from environment), validate the ARM
	// format and extract the subscription ID so ensureSubscription can skip the
	// prompt and just resolve the tenant.
	if a.flags.projectResourceId != "" {
		projectDetails, err := extractProjectDetails(a.flags.projectResourceId)
		if err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidProjectResourceId,
				fmt.Sprintf("invalid --project-id value: %s", err),
				"Provide a valid Foundry project resource ID in the format:\n"+
					"/subscriptions/<SUBSCRIPTION_ID>/resourceGroups/<RESOURCE_GROUP>/providers/"+
					"Microsoft.CognitiveServices/accounts/<ACCOUNT_NAME>/projects/<PROJECT_NAME>",
			)
		}
		a.azureContext.Scope.SubscriptionId = projectDetails.SubscriptionId
	}

	hasModelResources := manifestHasModelResources(agentManifest)
	if a.flags.projectResourceId == "" && shouldDeferInitAzureContext(a.flags.noPrompt, a.azureContext) {
		// In headless init, missing Azure values should not block local scaffold generation.
		// Defer project/model setup and print the values required before provisioning.
		if err := configureDeferredInitAzureContext(
			ctx, a.azdClient, a.environment.Name, a.azureContext, hasModelResources,
		); err != nil {
			return nil, err
		}
		if err := setACREnvVar(ctx, a.azdClient, a.environment.Name, a.skipACR()); err != nil {
			return nil, err
		}
		return agentManifest, nil
	}

	// If the manifest has no model resources, skip the model configuration prompt
	// but still ensure subscription and location are set for agent creation.
	// When --project-id is provided, use the existing project to derive location
	// and configure Foundry env vars (ACR, AppInsights, etc.) instead of prompting.
	if !hasModelResources {
		result, err := configureFoundryProject(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			a.flags.projectResourceId, a.flags.noPrompt, a.skipACR(),
		)
		if err != nil {
			return nil, err
		}
		if result.Credential != nil {
			a.credential = result.Credential
		}
		a.selectedFoundryProject = result.FoundryProject

		return agentManifest, nil
	}

	// Step 1: Foundry project selection
	if a.flags.projectResourceId != "" {
		// --project-id provided: auto-select "existing" path
		newCred, err := ensureSubscription(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			"Select an Azure subscription to look up available models and provision your Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred

		selectedProject, err := selectFoundryProject(
			ctx, a.azdClient, a.credential, a.azureContext, a.environment.Name,
			a.azureContext.Scope.SubscriptionId, a.flags.projectResourceId,
			a.skipACR(),
			true, // bicepless
		)
		if err != nil {
			return nil, err
		}
		a.selectedFoundryProject = selectedProject

		if selectedProject != nil {
			if err := setEnvValue(
				ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "true",
			); err != nil {
				return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
			}
			if err := updatePendingProjectSignal(
				ctx, a.azdClient, a.environment.Name, true,
			); err != nil {
				log.Printf("warning: failed to update project provision signal: %v", err)
			}
		} else {
			return nil, fmt.Errorf("specified foundry project was not found or is not eligible for the current configuration: %s", a.flags.projectResourceId)
		}
	} else if a.flags.noPrompt {
		newCred, err := ensureSubscriptionAndLocation(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			"Select an Azure subscription to look up available models and provision your Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred

		// Creating new resources — clear any stale existing-project flag
		if err := setEnvValue(
			ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "false",
		); err != nil {
			return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
		}
		if err := updatePendingProjectSignal(
			ctx, a.azdClient, a.environment.Name, false,
		); err != nil {
			log.Printf("warning: failed to update project provision signal: %v", err)
		}
	} else {
		// Prompt user to pick an existing Foundry project or create new resources
		projectChoices := []*azdext.SelectChoice{
			{Label: "Use an existing Foundry project", Value: "existing"},
			{Label: "Create a new Foundry project", Value: "new"},
		}

		defaultIdx := int32(0)
		projectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select a Foundry project to host your agent and any models or tools it uses.",
				Choices:       projectChoices,
				SelectedIndex: &defaultIdx,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("project selection was cancelled")
			}
			return nil, exterrors.FromPrompt(err, "failed to prompt for Foundry project configuration choice")
		}

		switch projectChoices[*projectResp.Value].Value {
		case "existing":
			newCred, err := ensureSubscription(
				ctx, a.azdClient, a.azureContext, a.environment.Name,
				"Select an Azure subscription to find existing Foundry projects.",
			)
			if err != nil {
				return nil, err
			}
			a.credential = newCred

			selectedProject, err := selectFoundryProject(
				ctx, a.azdClient, a.credential, a.azureContext, a.environment.Name,
				a.azureContext.Scope.SubscriptionId, "",
				a.skipACR(),
				true, // bicepless
			)
			if err != nil {
				return nil, err
			}
			a.selectedFoundryProject = selectedProject

			if selectedProject == nil {
				// No existing project selected → fall back to "create new" path
				fmt.Println(output.WithGrayFormat(
					"No existing Foundry project was selected. Falling back to creating new resources.",
				))
				if err := setEnvValue(
					ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "false",
				); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
				if err := ensureLocation(ctx, a.azdClient, a.azureContext, a.environment.Name); err != nil {
					return nil, err
				}
			} else {
				if err := setEnvValue(
					ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "true",
				); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
			}
		default:
			newCred, err := ensureSubscriptionAndLocation(
				ctx, a.azdClient, a.azureContext, a.environment.Name,
				"Select an Azure subscription to look up available models and provision your Foundry project resources.",
			)
			if err != nil {
				return nil, err
			}
			a.credential = newCred

			// Creating new resources — clear any stale existing-project flag
			if err := setEnvValue(
				ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "false",
			); err != nil {
				return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
			}
			if err := updatePendingProjectSignal(
				ctx, a.azdClient, a.environment.Name, false,
			); err != nil {
				log.Printf("warning: failed to update project provision signal: %v", err)
			}
			if err := ensureLocation(ctx, a.azdClient, a.azureContext, a.environment.Name); err != nil {
				return nil, err
			}
		}
	}

	// Now process models — getModelDeploymentDetails will branch based on AZURE_AI_PROJECT_ID
	agentManifest, deploymentDetails, err := a.ProcessModels(ctx, agentManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to process model resources: %w", err)
	}
	a.deploymentDetails = deploymentDetails

	// Set AZD_AGENT_SKIP_ACR so Bicep knows whether to create a container registry.
	if err := setACREnvVar(ctx, a.azdClient, a.environment.Name, a.skipACR()); err != nil {
		return nil, err
	}

	return agentManifest, nil
}

// isLocalFilePath reports whether path refers to a local file (not an http/https URL).
func isLocalFilePath(path string) bool {
	// Check if it starts with http:// or https://
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return false
	} else if _, err := os.Stat(path); err == nil {
		return true
	}

	return false
}

// checkNotDirectory returns a validation error when path is a directory
// instead of a manifest file. If an AgentManifest (a YAML file with a
// top-level "template" field) is found inside the directory, the suggestion
// includes the candidate manifest file path.
func checkNotDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}

	// Look for a manifest file inside the directory.  We check several
	// common names and only suggest a candidate when it actually looks like
	// an AgentManifest (has a top-level "template" key) rather than an
	// AgentDefinition that happens to share the same file name.
	for _, name := range []string{"agent.manifest.yaml", "agent.manifest.yml", "agent.yaml", "agent.yml"} {
		candidate := filepath.Join(path, name)
		if looksLikeManifest(candidate) {
			return exterrors.Validation(
				exterrors.CodeInvalidManifestPointer,
				fmt.Sprintf(
					"'%s' is a directory, not a manifest file",
					path,
				),
				fmt.Sprintf(
					"the --manifest flag must point to a manifest file, not a directory. Did you mean:\n  -m %q",
					candidate,
				),
			)
		}
	}

	return exterrors.Validation(
		exterrors.CodeInvalidManifestPointer,
		fmt.Sprintf("'%s' is a directory, not a manifest file", path),
		"the --manifest flag must point to a manifest file (e.g. agent.manifest.yaml), not a directory",
	)
}

// looksLikeManifest returns true when path is a regular file whose YAML
// content contains a top-level "template" key — the hallmark of an
// AgentManifest as opposed to an AgentDefinition.
func looksLikeManifest(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}

	//nolint:gosec // candidate path comes from a user-provided directory + known file names
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return false
	}

	_, hasTemplate := top["template"]
	return hasTemplate
}

// resolvePositionalArg classifies a positional argument as either a manifest
// pointer (explicit URI or existing file) or a source directory (existing
// directory). It returns (isManifest=true, isSrc=false) for explicit URIs and
// files, (isManifest=false, isSrc=true) for directories, or an error for
// unrecognized inputs.
//
// For non-existent paths, a heuristic is applied: .yaml/.yml extensions are
// treated as manifest pointers, while all other paths are treated as source
// directories (the downstream init flow creates them via MkdirAll).
func resolvePositionalArg(arg string) (isManifest bool, isSrc bool, err error) {
	// Check for an explicit URI form first. Requiring "://" avoids
	// misclassifying Windows drive paths such as C:\...
	if strings.Contains(arg, "://") {
		if parsed, parseErr := url.Parse(arg); parseErr == nil && parsed.Scheme != "" {
			return true, false, nil
		}
	}

	info, statErr := os.Stat(arg)
	if statErr == nil {
		if info.IsDir() {
			return false, true, nil
		}
		return true, false, nil
	}

	// Path does not exist — use file extension heuristic.
	ext := strings.ToLower(filepath.Ext(arg))
	if ext == ".yaml" || ext == ".yml" {
		return true, false, nil
	}

	// Default to source directory; the downstream flow will create it via MkdirAll.
	return false, true, nil
}

// applyPositionalArg resolves a positional argument and maps it to the
// appropriate flag, returning an error if the flag was already set explicitly.
func applyPositionalArg(arg string, flags *initFlags, cmd *cobra.Command) error {
	isManifest, isSrc, err := resolvePositionalArg(arg)
	if err != nil {
		return err
	}

	if isManifest {
		if cmd.Flags().Changed("manifest") {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"cannot pass both a positional argument and --manifest",
				"use either 'azd ai agent init <path>' or "+
					"'azd ai agent init -m <manifest>', not both",
			)
		}
		flags.manifestPointer = arg
	}

	if isSrc {
		if cmd.Flags().Changed("src") {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"cannot pass both a positional directory argument and --src",
				"use either 'azd ai agent init <dir>' or "+
					"'azd ai agent init --src <dir>', not both",
			)
		}
		flags.src = arg
	}

	return nil
}

func (a *InitAction) isGitHubUrl(manifestPointer string) bool {
	// Check if it's a GitHub URL based on the patterns from downloadGithubManifest
	parsedURL, err := url.Parse(manifestPointer)
	if err != nil {
		return false
	}
	hostname := parsedURL.Hostname()

	// Check for GitHub URL patterns as defined in downloadGithubManifest
	return strings.HasPrefix(hostname, "raw.githubusercontent") ||
		strings.HasPrefix(hostname, "api.github") ||
		strings.Contains(hostname, "github")
}

func (a *InitAction) downloadAgentYaml(
	ctx context.Context, manifestPointer string, targetDir string) (*agent_yaml.AgentManifest, string, error) {
	if manifestPointer == "" {
		return nil, "", fmt.Errorf("the path to an agent manifest needs to be provided (manifestPointer cannot be empty)")
	}

	var content []byte
	var err error
	var isGitHubUrl bool
	var urlInfo *GitHubUrlInfo
	var ghCli *github.Cli
	var console input.Console
	useGhCli := false

	// Check if manifestPointer is a local file path or a URI
	if isLocalFilePath(manifestPointer) {
		// Guard against directories (defense in depth — the caller should
		// have caught this already, but check here for safety).
		if err := checkNotDirectory(manifestPointer); err != nil {
			return nil, "", err
		}

		// Handle local file path
		log.Printf("Reading agent.yaml from local file: %s", manifestPointer)
		//nolint:gosec // manifest path is an explicit user-provided local path
		content, err = os.ReadFile(manifestPointer)
		if err != nil {
			return nil, "", exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("reading local file %s: %s", manifestPointer, err),
				"verify the file path exists and is readable",
			)
		}

		// Parse the YAML content into genericManifest
		var genericManifest map[string]any
		if err := yaml.Unmarshal(content, &genericManifest); err != nil {
			return nil, "", exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("parsing YAML from manifest file: %s", err),
				"verify the manifest file contains valid YAML",
			)
		}

		var name string
		var ok bool
		if name, ok = genericManifest["name"].(string); !ok {
			name = ""
		}

		if name != "" {
			// Check if the manifest file is under current directory + "src/<name>"
			currentDir, err := os.Getwd()
			if err != nil {
				return nil, "", fmt.Errorf("getting current directory: %w", err)
			}
			srcDir := filepath.Join(currentDir, "src", name)
			absManifestPath, err := filepath.Abs(manifestPointer)
			if err != nil {
				return nil, "", fmt.Errorf("getting absolute path for manifest %s: %w", manifestPointer, err)
			}

			// Check if manifest is under src directory
			if isSubpath(absManifestPath, srcDir) {
				// `--force` is the explicit pre-consent path for headless
				// callers: skip both the prompt and the no-prompt refusal,
				// and accept the overwrite. We log the decision so debug
				// output makes the choice visible in CI logs.
				if a.flags.force {
					log.Printf("--force: overwriting manifest %q inside src directory %q", manifestPointer, srcDir)
				} else if a.flags.noPrompt {
					// In no-prompt mode, refuse to silently overwrite a manifest
					// that lives inside the project's src tree. Headless callers
					// can pass --force to pre-consent, move the manifest, choose
					// a different --src target, or run interactively to confirm.
					return nil, "", exterrors.Validation(
						exterrors.CodeInvalidManifestPointer,
						fmt.Sprintf("manifest %q is inside the project src directory %q and would be overwritten", manifestPointer, srcDir),
						"pass --force to overwrite, move the manifest outside the src tree, "+
							"pass a different --src directory, or run interactively to confirm the overwrite",
					)
				} else {
					confirmResponse, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
						Options: &azdext.ConfirmOptions{
							Message:      "This operation will overwrite the provided manifest file. Continue?",
							DefaultValue: new(false),
						},
					})
					if err != nil {
						return nil, "", fmt.Errorf("prompting for confirmation: %w", err)
					}
					if !*confirmResponse.Value {
						return nil, "", exterrors.Cancelled("operation cancelled by user")
					}
				}
			}
		}
	} else if a.isGitHubUrl(manifestPointer) {
		// Handle GitHub URLs using downloadGithubManifest
		// manifestPointer validation:
		// - accepts only URLs with the following format:
		//  - https://raw.<hostname>/<owner>/<repo>/refs/heads/<branch>/<path>/<file>.json
		//    - This url comes from a user clicking the `raw` button on a file in a GitHub repository (web view).
		//  - https://<hostname>/<owner>/<repo>/blob/<branch>/<path>/<file>.json
		//    - This url comes from a user browsing GitHub repository and copy-pasting the url from the browser.
		//  - https://api.<hostname>/repos/<owner>/<repo>/contents/<path>/<file>.json
		//    - This url comes from users familiar with the GitHub API. Usually for programmatic registration of templates.

		fmt.Println(output.WithGrayFormat("Downloading manifest from GitHub..."))
		log.Printf("Downloading manifest from GitHub: %s", manifestPointer)
		isGitHubUrl = true

		// Create a simple console and command runner for GitHub CLI
		commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		})

		console = input.NewConsole(
			false, // noPrompt
			true,  // isTerminal
			input.Writers{Output: os.Stdout},
			input.ConsoleHandles{
				Stderr: os.Stderr,
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
			},
			nil, // formatter
			nil, // externalPromptCfg
		)

		ghCli = github.NewGitHubCli(console, commandRunner)
		if err := ghCli.EnsureInstalled(ctx); err != nil {
			return nil, "", exterrors.Dependency(
				exterrors.CodeGitHubDownloadFailed,
				fmt.Sprintf("ensuring gh is installed: %s", err),
				"install the GitHub CLI (gh) from https://cli.github.com",
			)
		}

		var contentStr string
		// First try naive parsing assuming branch is a single word. This allows users to not have to authenticate
		// with gh CLI for public repositories.
		urlInfo = parseGitHubUrlNaive(manifestPointer)
		if urlInfo != nil {
			// Construct GitHub Contents API URL with ref query parameter
			fileApiUrl := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
			if urlInfo.Branch != "" {
				escapedBranch := url.QueryEscape(urlInfo.Branch)
				fileApiUrl += fmt.Sprintf("?ref=%s", escapedBranch)
			}
			log.Printf("Attempting to download manifest from '%s' in repository '%s', branch '%s'", urlInfo.FilePath, urlInfo.RepoSlug, urlInfo.Branch)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileApiUrl, nil)
			if err == nil {
				req.Header.Set("Accept", "application/vnd.github.v3.raw")
				//nolint:gosec // URL is constrained to GitHub API endpoint built from parsed GitHub URL
				resp, err := a.httpClient.Do(req)
				if err == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						bodyBytes, readErr := io.ReadAll(resp.Body)
						if readErr == nil {
							contentStr = string(bodyBytes)
							log.Printf("Downloaded manifest from branch: %s", urlInfo.Branch)
						}
					}
				}
			}
			if contentStr == "" {
				log.Print("Naive GitHub URL parsing failed to download manifest")
				log.Print("Proceeding with full parsing and download logic...")
			}
		}

		if contentStr == "" {
			// Fall back to complex parsing via azd GitHub CLI handling
			useGhCli = true
			urlInfo, err = a.parseGitHubUrl(ctx, manifestPointer)
			if err != nil {
				return nil, "", err
			}

			apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
			if urlInfo.Branch != "" {
				log.Printf("Downloaded manifest from branch: %s", urlInfo.Branch)
				apiPath += fmt.Sprintf("?ref=%s", urlInfo.Branch)
			}

			contentStr, err = downloadGithubManifest(ctx, urlInfo, apiPath, ghCli)
			if err != nil {
				return nil, "", exterrors.Dependency(
					exterrors.CodeGitHubDownloadFailed,
					fmt.Sprintf("downloading from GitHub: %s", err),
					"verify the URL points to a valid agent.yaml file in the repository",
				)
			}
		}

		content = []byte(contentStr)
	} else {
		// If we reach here, the manifest pointer didn't match any known type
		return nil, "", exterrors.Validation(
			exterrors.CodeInvalidManifestPointer,
			fmt.Sprintf("manifest pointer '%s' is not a valid local file path or GitHub URL", manifestPointer),
			"provide a valid URL or an existing local agent.yaml/agent.yml path",
		)
	}

	// Parse and validate the YAML content against AgentManifest structure
	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(content)
	if err != nil {
		return nil, "", err
	}

	templateAgentName, err := agentNameFromTemplate(agentManifest.Template)
	if err != nil {
		return nil, "", err
	}
	selectedAgentName, err := resolveInitAgentName(ctx, a.azdClient, a.flags, templateAgentName)
	if err != nil {
		return nil, "", err
	}
	if err := setAgentNameOnTemplate(agentManifest, selectedAgentName); err != nil {
		return nil, "", err
	}

	fmt.Println(output.WithGrayFormat("✓ Manifest validated successfully"))

	// Use the (possibly user-renamed) selected agent name as the canonical
	// identifier for naming the service entry and target directory. The
	// outer manifest's Name field is not updated when the user resolves a
	// conflict with an existing Foundry agent, so prefer selectedAgentName
	// to keep azure.yaml's service entry consistent with the agent name
	// written to agent.yaml.
	agentId := selectedAgentName
	if agentId == "" {
		agentId = agentManifest.Name
	}
	serviceName := strings.ReplaceAll(agentId, " ", "")

	// Use targetDir if provided, otherwise default to "src/{agentId}"
	autoDir := targetDir == ""
	if autoDir {
		targetDir = filepath.Join("src", agentId)
	}

	// When the target directory was auto-computed (no --src flag), check for
	// collisions with an existing directory or an existing azure.yaml service.
	// If a collision is found, prompt for a new service name (or auto-suffix
	// in no-prompt mode).
	if autoDir {
		targetDir, serviceName, err = a.resolveCollisions(
			ctx, agentId, targetDir, serviceName,
		)
		if err != nil {
			return nil, "", err
		}
	}
	a.serviceNameOverride = serviceName

	// Safety checks for local container-based agents should happen before prompting for model SKU, etc.
	if isLocalFilePath(manifestPointer) {
		if _, isContainerAgent := agentManifest.Template.(agent_yaml.ContainerAgent); isContainerAgent {
			if err := a.validateLocalContainerAgentCopy(ctx, manifestPointer, targetDir); err != nil {
				return nil, "", err
			}
		}
	}

	// Create target directory if it doesn't exist
	//nolint:gosec // project scaffold directory should be readable and traversable
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, "", fmt.Errorf("creating target directory %s: %w", targetDir, err)
	}

	if isLocalFilePath(manifestPointer) {
		// Check if the template is a ContainerAgent
		_, isHostedContainer := agentManifest.Template.(agent_yaml.ContainerAgent)

		if isHostedContainer {
			// For container agents, copy the entire parent directory.
			// If the manifest already lives in the target directory (re-init), skip the copy.
			manifestDir := filepath.Dir(manifestPointer)
			srcAbs, err := filepath.Abs(manifestDir)
			if err != nil {
				return nil, "", fmt.Errorf("resolving manifest directory %s: %w", manifestDir, err)
			}
			dstAbs, err := filepath.Abs(targetDir)
			if err != nil {
				return nil, "", fmt.Errorf("resolving target directory %s: %w", targetDir, err)
			}
			if !isSamePath(srcAbs, dstAbs) {
				log.Print("Copying full directory for container agent")
				err := copyDirectory(manifestDir, targetDir)
				if err != nil {
					return nil, "", fmt.Errorf("copying parent directory: %w", err)
				}
				// Honor .azdignore in the copied template tree (parity
				// with `azd init -t <template>`). This runs after the
				// full copy so the matcher reads the root .azdignore
				// from targetDir and prunes matches, then removes the
				// root + any nested .azdignore files from the output.
				if err := azdignore.Apply(targetDir); err != nil {
					return nil, "", fmt.Errorf("applying %s rules: %w", azdignore.FileName, err)
				}
			}
		}
	} else if isGitHubUrl {
		// Check if the template is a ContainerAgent
		_, isHostedContainer := agentManifest.Template.(agent_yaml.ContainerAgent)

		if isHostedContainer {
			// For container agents, download the entire parent directory
			log.Print("Downloading full directory for container agent")
			err := downloadParentDirectory(ctx, urlInfo, targetDir, ghCli, console, useGhCli, a.httpClient)
			if err != nil {
				return nil, "", exterrors.Dependency(
					exterrors.CodeGitHubDownloadFailed,
					fmt.Sprintf("downloading parent directory: %s", err),
					"verify the URL points to a valid repository and you have access",
				)
			}
			// Honor .azdignore in the downloaded template tree. Apply
			// matches core's "copy then prune" model: the recursive
			// GitHub download intentionally fetches everything, then
			// the ignore rules trim the result and the .azdignore
			// files themselves are removed.
			if err := azdignore.Apply(targetDir); err != nil {
				return nil, "", fmt.Errorf("applying %s rules: %w", azdignore.FileName, err)
			}
		}
	}

	return agentManifest, targetDir, nil
}

// removeContainerFiles removes Dockerfile and .dockerignore from targetDir.
// Called when code deploy is selected so container-only files from downloaded
// samples are not left in the project.
func removeContainerFiles(targetDir string) {
	for _, name := range []string{"Dockerfile", ".dockerignore"} {
		p := filepath.Join(targetDir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: failed to remove %s: %v", p, err)
		}
	}
}

// writeAgentIgnoreFile generates a default .agentignore in targetDir if one does
// not already exist. The agent definition itself is no longer written to disk —
// it lives as service-level properties in azure.yaml — but .agentignore is still
// used to scope which files are included in code-deploy ZIP packaging.
func writeAgentIgnoreFile(targetDir string) error {
	agentIgnorePath := filepath.Join(targetDir, ".agentignore")
	if _, err := os.Stat(agentIgnorePath); os.IsNotExist(err) {
		if err := os.WriteFile(agentIgnorePath, []byte(project.DefaultAgentIgnoreContent()), osutil.PermissionFile); err != nil {
			return fmt.Errorf("writing .agentignore: %w", err)
		}
	}

	return nil
}

func (a *InitAction) addToProject(ctx context.Context, targetDir string, agentManifest *agent_yaml.AgentManifest) error {
	// If targetDir is ".", resolve the actual relative path from the project root to cwd.
	// This ensures azure.yaml gets the correct "project:" value when init is run from a subdirectory.
	if targetDir == "." {
		if cwd, err := os.Getwd(); err == nil && a.projectConfig != nil && a.projectConfig.Path != "" {
			if relPath, err := filepath.Rel(a.projectConfig.Path, cwd); err == nil && relPath != "." {
				targetDir = filepath.ToSlash(relPath)
			}
		}
	}

	// Convert the template to bytes
	templateBytes, err := json.Marshal(agentManifest.Template)
	if err != nil {
		return fmt.Errorf("failed to marshal agent template to JSON: %w", err)
	}

	// Convert the bytes to a dictionary
	var templateDict map[string]any
	if err := json.Unmarshal(templateBytes, &templateDict); err != nil {
		return fmt.Errorf("failed to unmarshal agent template from JSON: %w", err)
	}

	// Convert the dictionary to bytes
	dictJsonBytes, err := json.Marshal(templateDict)
	if err != nil {
		return fmt.Errorf("failed to marshal templateDict to JSON: %w", err)
	}

	// Convert the bytes to an Agent Definition
	var agentDef agent_yaml.AgentDefinition
	if err := json.Unmarshal(dictJsonBytes, &agentDef); err != nil {
		return fmt.Errorf("failed to unmarshal JSON to AgentDefinition: %w", err)
	}

	var agentConfig = project.ServiceTargetAgentConfig{}

	resourceDetails := []project.Resource{}
	switch agentDef.Kind {
	case agent_yaml.AgentKindHosted:
		// Handle tool resources that require connection names
		if agentManifest.Resources != nil {
			for _, resource := range agentManifest.Resources {
				// Try to cast to ToolResource
				if toolResource, ok := resource.(agent_yaml.ToolResource); ok {
					// Check if this is a resource that requires a connection name
					if toolResource.Id == "bing_grounding" || toolResource.Id == "azure_ai_search" {
						// Prompt the user for a connection name
						resp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
							Options: &azdext.PromptOptions{
								Message:        fmt.Sprintf("Enter a connection name for adding the resource %s to your Microsoft Foundry project", toolResource.Id),
								IgnoreHintKeys: true,
								DefaultValue:   toolResource.Id,
							},
						})
						if err != nil {
							return fmt.Errorf("prompting for connection name for %s: %w", toolResource.Id, err)
						}

						// Add to resource details
						resourceDetails = append(resourceDetails, project.Resource{
							Resource:       toolResource.Id,
							ConnectionName: resp.Value,
						})
					}
				}
				// Skip the resource if the cast fails
			}
		}

		// Use container settings that were already populated before writing agent.yaml
		agentConfig.Container = a.containerSettings
	}

	agentConfig.Deployments = a.deploymentDetails
	agentConfig.Resources = resourceDetails

	// Process toolbox resources from the manifest
	toolboxes, toolConnections, credEnvVars, err := extractToolboxAndConnectionConfigs(agentManifest)
	if err != nil {
		return err
	}
	agentConfig.Toolboxes = toolboxes
	agentConfig.ToolConnections = toolConnections

	// Persist credential values as azd environment variables so they are
	// resolved at provision/deploy time instead of stored in azure.yaml.
	for envKey, envVal := range credEnvVars {
		if _, setErr := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: a.environment.Name,
			Key:     envKey,
			Value:   envVal,
		}); setErr != nil {
			return fmt.Errorf("storing credential env var %s: %w", envKey, setErr)
		}
	}

	// Process connection resources from the manifest
	connections, connCredEnvVars, err := extractConnectionConfigs(agentManifest)
	if err != nil {
		return err
	}
	agentConfig.Connections = connections

	// Store connection credential env vars alongside toolbox ones
	for envKey, envVal := range connCredEnvVars {
		if _, setErr := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: a.environment.Name,
			Key:     envKey,
			Value:   envVal,
		}); setErr != nil {
			return fmt.Errorf("storing credential env var %s: %w", envKey, setErr)
		}
	}

	preBuiltImage := preBuiltImageForInit(agentManifest, a.flags.image)

	// Detect startup command. Skipped for code deploy (uses ZIP packaging) and
	// when the agent uses a pre-built container image, since the image's own
	// entrypoint runs and no startup command applies.
	if !a.isCodeDeploy && preBuiltImage == "" {
		startupCmd, err := resolveStartupCommandForInit(ctx, a.azdClient, a.projectConfig.Path, targetDir, a.flags.noPrompt)
		if err != nil {
			return err
		}
		agentConfig.StartupCommand = startupCmd
	}

	// Each Foundry resource is written as its own azure.yaml service entry, so
	// the deployments, connections, and toolboxes move out of the agent config
	// into sibling azure.ai.project/connection/toolbox services emitted below.
	// The agent keeps its container, resources, tool connections, and startup
	// command. The provisioning handlers re-source the moved data from the
	// sibling services.
	resourceDeployments := agentConfig.Deployments
	resourceConnections := agentConfig.Connections
	resourceToolboxes := agentConfig.Toolboxes
	agentConfig.Deployments = nil
	agentConfig.Connections = nil
	agentConfig.Toolboxes = nil

	// The agent definition (formerly written to agent.yaml) now lives as
	// service-level properties on the azure.ai.agent entry. Rebuild the full
	// container agent from the manifest template so it can be embedded inline
	// alongside the remaining agent config (container, tool connections,
	// startup command).
	var containerDef agent_yaml.ContainerAgent
	templateYAML, err := yaml.Marshal(agentManifest.Template)
	if err != nil {
		return fmt.Errorf("marshaling agent definition: %w", err)
	}
	if err := yaml.Unmarshal(templateYAML, &containerDef); err != nil {
		return fmt.Errorf("parsing agent definition: %w", err)
	}

	agentProps, err := project.AgentDefinitionToServiceProperties(containerDef, &agentConfig)
	if err != nil {
		return err
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:                 a.serviceNameOverride,
		RelativePath:         targetDir,
		Host:                 AiAgentHost,
		Language:             "docker",
		Image:                preBuiltImage,
		AdditionalProperties: agentProps,
	}

	// For hosted agents, configure Docker or code deploy settings
	if agentDef.Kind == agent_yaml.AgentKindHosted {
		if a.isCodeDeploy {
			serviceConfig.Language = "python"
			// If the agent uses a dotnet runtime, set language to csharp
			if ca, ok := agentManifest.Template.(agent_yaml.ContainerAgent); ok &&
				ca.CodeConfiguration != nil &&
				strings.HasPrefix(ca.CodeConfiguration.Runtime, "dotnet_") {
				serviceConfig.Language = "csharp"
			}
		} else {
			// Disable remote build when the Foundry account is VNET-injected; remote
			// build runs on worker IPs that can't reach a registry in the VNET.
			networkInjected := a.selectedFoundryProject != nil && a.selectedFoundryProject.NetworkInjected
			serviceConfig.Docker = &azdext.DockerProjectOptions{RemoteBuild: !networkInjected}
		}
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}

	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	// Emit the sibling Foundry resource services (project + deployments,
	// connections, toolboxes) and wire the agent's uses: to them. A selected
	// existing project contributes its endpoint so provision reuses it.
	if err := emitResourceServices(
		ctx, a.azdClient, a.serviceNameOverride,
		projectNameHint(ctx, a.azdClient, a.environment.Name, a.selectedFoundryProject),
		a.selectedFoundryProject.Endpoint(),
		resourceDeployments, resourceConnections, resourceToolboxes,
	); err != nil {
		return err
	}

	fmt.Printf(
		"\nAdded your agent as a service entry named '%s' under the file azure.yaml.\n",
		a.serviceNameOverride,
	)

	// Replace the legacy hardcoded `azd up` / `azd deploy` hint with the
	// shared nextstep resolver. The resolver inspects the current azd
	// environment plus each azure.ai.agent service's agent.yaml and emits
	// context-aware guidance: `azd provision` when infra outputs are
	// unset, `azd env set <KEY> <value>` lines when agent.yaml references
	// user-supplied variables that are unset, or `azd ai agent run` when
	// everything is configured. All paths append the deploy hint as the
	// trailing line. State-assembly errors are intentionally ignored: the
	// resolver degrades gracefully on partial state per the design spec.
	var stateOpts []nextstep.Option
	if a.createdFolderDisplay != "" {
		stateOpts = append(stateOpts, nextstep.WithCreatedFolder(a.createdFolderDisplay))
	}
	state, _ := nextstep.AssembleState(ctx, a.azdClient, stateOpts...)
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, a.azdClient)))
	return nil
}

//nolint:gosec // env var key name, not a credential
const resourceTokenSaltKey = "AZD_RESOURCE_TOKEN_SALT"

// read by azd's Bicep provider to scope resource-group deployments to a unique name per environment
const resourceGroupEnvKey = "AZURE_RESOURCE_GROUP"

// maxResourceGroupNameLen is the Azure resource group name length limit
// (Microsoft.Resources/resourceGroups: 1-90 chars).
const maxResourceGroupNameLen = 90

// ensureResourceTokenSalt checks whether the current azd environment already
// has a resource token salt. If not, it generates and stores one. Returns the
// persisted salt value (existing or newly-generated), or an empty string if
// no salt could be persisted (failures are silently ignored so the Bicep
// templates fall back to the original deterministic uniqueString() hash).
func ensureResourceTokenSalt(ctx context.Context, azdClient *azdext.AzdClient, envName string) string {
	// Already have a salt from a previous init — keep it so resource names stay stable.
	existing, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     resourceTokenSaltKey,
	})
	if err == nil && existing.Value != "" {
		return existing.Value
	}

	// Generate a random salt; if entropy fails, fall back to deterministic naming.
	salt, err := generateResourceTokenSalt()
	if err != nil {
		return ""
	}

	// Persist the salt into the azd environment; if storage fails, provision
	// will still work with the original deterministic resource names.
	if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     resourceTokenSaltKey,
		Value:   salt,
	}); err != nil {
		return ""
	}
	return salt
}

// generateResourceTokenSalt returns a random 8-character hex string.
func generateResourceTokenSalt() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ensureResourceGroupName writes a salted AZURE_RESOURCE_GROUP value to the
// azd environment when scaffolding a new project, so that recreating a
// project with the same environment name produces a fresh resource group
// (avoiding collisions with any leftover resources from a prior teardown).
//
// Best-effort: skipped when salt is empty or AZURE_RESOURCE_GROUP is
// already set (preserving BYO / previously-provisioned values). Storage
// failures are silently ignored so Bicep's default `rg-${environmentName}`
// continues to work.
func ensureResourceGroupName(ctx context.Context, azdClient *azdext.AzdClient, envName, salt string) {
	if salt == "" {
		return
	}
	existing, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     resourceGroupEnvKey,
	})
	if err == nil && existing.Value != "" {
		return
	}
	name := composeSaltedResourceGroupName(envName, salt)
	_, _ = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     resourceGroupEnvKey,
		Value:   name,
	})
}

// composeSaltedResourceGroupName returns `rg-{envName}-{salt}` with envName
// truncated first so the salt is always appended and the final name fits
// inside Azure's 90-char RG limit. Trailing "-" and "." characters are
// trimmed off the truncated envName so the join doesn't produce "--" /
// ".-" and the final name doesn't end with "." (which Azure disallows).
//
// Caller is expected to pass a non-empty salt; the function still produces
// a valid name if salt is empty (no trailing dash).
func composeSaltedResourceGroupName(envName, salt string) string {
	const prefix = "rg-"
	suffixLen := 0
	if salt != "" {
		suffixLen = 1 + len(salt) // joiner "-" + salt
	}
	maxEnvName := max(maxResourceGroupNameLen-len(prefix)-suffixLen, 0)
	truncated := envName
	if len(truncated) > maxEnvName {
		truncated = truncated[:maxEnvName]
	}
	truncated = strings.TrimRight(truncated, "-.")
	if salt == "" {
		return prefix + truncated
	}
	return prefix + truncated + "-" + salt
}

// resolveCollisions checks whether the auto-computed target directory or
// service name already exist. When a collision is detected, the user is
// prompted for a new name (or a numeric suffix is appended in no-prompt
// mode). Returns the (possibly adjusted) targetDir and serviceName.
func (a *InitAction) resolveCollisions(
	ctx context.Context,
	agentId string,
	targetDir string,
	serviceName string,
) (string, string, error) {
	dirExists := fileExists(targetDir)

	serviceExists := false
	if a.projectConfig != nil {
		for _, svc := range a.projectConfig.Services {
			if svc.Name == serviceName {
				serviceExists = true
				break
			}
		}
	}

	if !dirExists && !serviceExists {
		return targetDir, serviceName, nil
	}

	// Find the next available name for use as the default suggestion
	// (interactive) or the final answer (no-prompt).
	suggestion, suggestionDir, suggestionSvc, err :=
		a.nextAvailableName(agentId)
	if err != nil {
		return "", "", err
	}

	if a.flags.noPrompt {
		log.Printf(
			"Collision on %q; using %q", agentId, suggestion,
		)
		return suggestionDir, suggestionSvc, nil
	}

	// Build a collision message tailored to what actually collided.
	collisionMsg := buildCollisionMessage(
		dirExists, serviceExists, targetDir, serviceName,
	)

	// Interactive mode: let the user choose.
	choices := []*azdext.SelectChoice{
		{
			Label: "Overwrite existing",
			Value: "overwrite",
		},
		{
			Label: "Use a different service name",
			Value: "rename",
		},
	}

	defaultIdx := int32(1)
	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       collisionMsg,
			Choices:       choices,
			SelectedIndex: &defaultIdx,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", "", exterrors.Cancelled(
				"initialization was cancelled",
			)
		}
		return "", "", fmt.Errorf(
			"prompting for collision resolution: %w", err,
		)
	}

	if choices[*resp.Value].Value == "overwrite" {
		return targetDir, serviceName, nil
	}

	// Prompt for a new name — default to the next available suffix.
	nameResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Enter a new service name for this agent",
			DefaultValue:   suggestion,
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", "", exterrors.Cancelled(
				"initialization was cancelled",
			)
		}
		return "", "", fmt.Errorf(
			"prompting for new service name: %w", err,
		)
	}

	newName := strings.TrimSpace(nameResp.Value)
	if newName == "" {
		newName = suggestion
	}

	newDir, newSvc, err := validateRenameInput(newName)
	if err != nil {
		return "", "", err
	}
	return newDir, newSvc, nil
}

// validateRenameInput validates a user-provided rename input and returns
// the target directory and sanitized service name. It rejects names with
// path separators, dot segments, or invalid service-name characters.
func validateRenameInput(newName string) (string, string, error) {
	if filepath.IsAbs(newName) ||
		strings.ContainsAny(newName, `/\`) ||
		newName == "." ||
		newName == ".." {
		return "", "", fmt.Errorf(
			"invalid service name %q: name must be a single directory"+
				" name without path separators or dot segments",
			newName,
		)
	}

	newSvc := strings.ReplaceAll(newName, " ", "")
	if err := azdext.ValidateServiceName(newSvc); err != nil {
		return "", "", fmt.Errorf(
			"invalid service name %q: %w", newName, err,
		)
	}

	newDir := filepath.Join("src", newName)
	return newDir, newSvc, nil
}

// buildCollisionMessage returns a user-facing prompt string tailored to the
// type of collision detected (directory, service name, or both).
func buildCollisionMessage(
	dirExists, serviceExists bool,
	targetDir, serviceName string,
) string {
	switch {
	case dirExists && serviceExists:
		return fmt.Sprintf(
			"A service named '%s' and its directory '%s' already exist."+
				" Overwrite or use a different name?",
			serviceName, targetDir,
		)
	case serviceExists:
		return fmt.Sprintf(
			"A service named '%s' already exists in your azure.yaml."+
				" Overwrite it or use a different name?",
			serviceName,
		)
	default: // dirExists only
		return fmt.Sprintf(
			"The directory '%s' already exists."+
				" Overwrite it or use a different name?",
			targetDir,
		)
	}
}

// nextAvailableName finds the next unused name by appending -2, -3, etc.
// Returns the candidate name, directory, and service name.
func (a *InitAction) nextAvailableName(
	agentId string,
) (string, string, string, error) {
	const maxAttempts = 100
	for i := 2; i <= maxAttempts; i++ {
		candidate := fmt.Sprintf("%s-%d", agentId, i)
		candidateDir := filepath.Join("src", candidate)
		candidateSvc := strings.ReplaceAll(candidate, " ", "")

		if fileExists(candidateDir) {
			continue
		}

		svcTaken := false
		if a.projectConfig != nil {
			for _, svc := range a.projectConfig.Services {
				if svc.Name == candidateSvc {
					svcTaken = true
					break
				}
			}
		}
		if svcTaken {
			continue
		}

		return candidate, candidateDir, candidateSvc, nil
	}

	return "", "", "", fmt.Errorf(
		"could not find a unique name after %d attempts "+
			"(tried %s-2 through %s-%d)",
		maxAttempts-1, agentId, agentId, maxAttempts,
	)
}

func (a *InitAction) populateContainerSettings(
	ctx context.Context,
	manifestResources *agent_yaml.ContainerResources,
) (*project.ContainerSettings, error) {
	defaultIndex := int32(0)
	if manifestResources != nil {
		for i, t := range project.ResourceTiers {
			if t.Cpu == manifestResources.Cpu && t.Memory == manifestResources.Memory {
				defaultIndex = boundedInt32Index(i)
				break
			}
		}
	}

	// When the user provided a manifest explicitly (-m), auto-select the default
	// resource tier without prompting to minimize interactive steps. In the
	// primary -m quickstart path (Python/.NET), deploy mode auto-selects
	// "container" so this function is reached for the default flow.
	if a.userProvidedManifest {
		selected := project.ResourceTiers[defaultIndex]
		log.Printf("Defaulted compute tier: %s", selected.String())
		return &project.ContainerSettings{
			Resources: &project.ResourceSettings{
				Memory: selected.Memory,
				Cpu:    selected.Cpu,
			},
		}, nil
	}

	choices := make([]*azdext.SelectChoice, len(project.ResourceTiers))
	for i, t := range project.ResourceTiers {
		choices[i] = &azdext.SelectChoice{
			Label: t.String(),
			Value: fmt.Sprintf("%d", i),
		}
	}

	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select resources (CPU and Memory) for your agent. You can adjust these settings later in the azure.yaml file if needed.",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for container resources: %w", err)
	}

	selected := project.ResourceTiers[*resp.Value]

	containerSettings := &project.ContainerSettings{
		Resources: &project.ResourceSettings{
			Memory: selected.Memory,
			Cpu:    selected.Cpu,
		},
	}

	return containerSettings, nil
}

func downloadGithubManifest(
	ctx context.Context, urlInfo *GitHubUrlInfo, apiPath string, ghCli *github.Cli) (string, error) {
	// This method assumes that either the repo is public, or the user has already been prompted to log in to the github cli
	// through our use of the underlying azd logic.

	content, err := ghCli.ApiCall(ctx, urlInfo.Hostname, apiPath, github.ApiCallOptions{
		Headers: []string{"Accept: application/vnd.github.v3.raw"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get content: %w", err)
	}

	return content, nil
}

// parseGitHubUrl extracts repository information from various GitHub URL formats using extension framework
func (a *InitAction) parseGitHubUrl(ctx context.Context, manifestPointer string) (*GitHubUrlInfo, error) {
	urlInfo, err := a.azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
		Url: manifestPointer,
	})
	if err != nil {
		return nil, err
	}

	return &GitHubUrlInfo{
		RepoSlug: urlInfo.RepoSlug,
		Branch:   urlInfo.Branch,
		FilePath: urlInfo.FilePath,
		Hostname: urlInfo.Hostname,
	}, nil
}

func downloadParentDirectory(
	ctx context.Context, urlInfo *GitHubUrlInfo, targetDir string, ghCli *github.Cli, console input.Console, useGhCli bool, httpClient *http.Client) error {

	// Get parent directory by removing the filename from the file path
	pathParts := strings.Split(urlInfo.FilePath, "/")
	if len(pathParts) <= 1 {
		log.Print("The file agent.yaml is at repository root, no parent directory to download")
		return nil
	}

	parentDirPath := strings.Join(pathParts[:len(pathParts)-1], "/")
	log.Printf("Downloading parent directory '%s' from repository '%s', branch '%s'", parentDirPath, urlInfo.RepoSlug, urlInfo.Branch)
	fmt.Println(output.WithGrayFormat("Downloading files..."))

	// Download directory contents
	if useGhCli {
		if err := downloadDirectoryContents(ctx, urlInfo.Hostname, urlInfo.RepoSlug, parentDirPath, parentDirPath, urlInfo.Branch, targetDir, ghCli, console); err != nil {
			return fmt.Errorf("failed to download directory contents with GH CLI: %w", err)
		}
	} else {
		if err := downloadDirectoryContentsWithoutGhCli(ctx, urlInfo.RepoSlug, parentDirPath, parentDirPath, urlInfo.Branch, targetDir, httpClient); err != nil {
			return fmt.Errorf("failed to download directory contents without GH CLI: %w", err)
		}
	}

	fmt.Println(output.WithGrayFormat("Downloaded to: %s", targetDir))
	fmt.Println()
	return nil
}

func downloadDirectoryContents(
	ctx context.Context, hostname string, repoSlug string, dirPath string, rootDirPath string, branch string, localPath string, ghCli *github.Cli, console input.Console) error {

	// Get directory contents using GitHub API
	apiPath := fmt.Sprintf("/repos/%s/contents/%s", repoSlug, dirPath)
	if branch != "" {
		apiPath += fmt.Sprintf("?ref=%s", branch)
	}

	dirContentsJson, err := ghCli.ApiCall(ctx, hostname, apiPath, github.ApiCallOptions{})
	if err != nil {
		return fmt.Errorf("failed to get directory contents: %w", err)
	}

	// Parse the directory contents JSON
	var dirContents []map[string]any
	if err := json.Unmarshal([]byte(dirContentsJson), &dirContents); err != nil {
		return fmt.Errorf("failed to parse directory contents JSON: %w", err)
	}

	// Download each file and subdirectory
	for _, item := range dirContents {
		name, ok := item["name"].(string)
		if !ok {
			continue
		}

		itemType, ok := item["type"].(string)
		if !ok {
			continue
		}

		itemPath := fmt.Sprintf("%s/%s", dirPath, name)
		itemLocalPath := filepath.Join(localPath, name)

		if itemType == "file" {
			// Download file
			relativePath := strings.TrimPrefix(itemPath, rootDirPath+"/")
			fmt.Println(output.WithGrayFormat("  %s", relativePath))
			log.Printf("Downloading file: %s", itemPath)
			fileApiPath := fmt.Sprintf("/repos/%s/contents/%s", repoSlug, itemPath)
			if branch != "" {
				fileApiPath += fmt.Sprintf("?ref=%s", branch)
			}

			fileContent, err := ghCli.ApiCall(ctx, hostname, fileApiPath, github.ApiCallOptions{
				Headers: []string{"Accept: application/vnd.github.v3.raw"},
			})
			if err != nil {
				return fmt.Errorf("failed to download file %s: %w", itemPath, err)
			}

			//nolint:gosec // downloaded project files are intended to be readable by project tooling
			if err := os.WriteFile(itemLocalPath, []byte(fileContent), 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", itemLocalPath, err)
			}
		} else if itemType == "dir" {
			// Recursively download subdirectory
			log.Printf("Downloading directory: %s", itemPath)
			//nolint:gosec // scaffolded directories are intended to be readable/traversable
			if err := os.MkdirAll(itemLocalPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", itemLocalPath, err)
			}

			// Recursively download directory contents
			if err := downloadDirectoryContents(ctx, hostname, repoSlug, itemPath, rootDirPath, branch, itemLocalPath, ghCli, console); err != nil {
				return fmt.Errorf("failed to download subdirectory %s: %w", itemPath, err)
			}
		}
	}

	return nil
}

func downloadDirectoryContentsWithoutGhCli(
	ctx context.Context, repoSlug string, dirPath string, rootDirPath string, branch string, localPath string, httpClient *http.Client) error {

	// Get directory contents using GitHub API directly
	apiUrl := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repoSlug, dirPath)
	if branch != "" {
		apiUrl += fmt.Sprintf("?ref=%s", branch)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	//nolint:gosec // URL is explicitly constructed for GitHub contents API
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get directory contents: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get directory contents: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read directory contents response: %w", err)
	}

	// Parse the directory contents JSON
	var dirContents []map[string]any
	if err := json.Unmarshal(body, &dirContents); err != nil {
		return fmt.Errorf("failed to parse directory contents JSON: %w", err)
	}

	// Download each file and subdirectory
	for _, item := range dirContents {
		name, ok := item["name"].(string)
		if !ok {
			continue
		}

		itemType, ok := item["type"].(string)
		if !ok {
			continue
		}

		itemPath := fmt.Sprintf("%s/%s", dirPath, name)
		itemLocalPath := filepath.Join(localPath, name)

		if itemType == "file" {
			// Download file using GitHub Contents API with raw accept header
			relativePath := strings.TrimPrefix(itemPath, rootDirPath+"/")
			fmt.Println(output.WithGrayFormat("  %s", relativePath))
			log.Printf("Downloading file: %s", itemPath)
			fileURL := &url.URL{
				Scheme: "https",
				Host:   "api.github.com",
				Path:   fmt.Sprintf("/repos/%s/contents/%s", repoSlug, itemPath),
			}
			if branch != "" {
				query := url.Values{}
				query.Set("ref", branch)
				fileURL.RawQuery = query.Encode()
			}

			fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL.String(), nil)
			if err != nil {
				return fmt.Errorf("failed to create file request %s: %w", itemPath, err)
			}
			fileReq.Header.Set("Accept", "application/vnd.github.v3.raw")

			//nolint:gosec // URL is explicitly constructed for GitHub contents API
			fileResp, err := httpClient.Do(fileReq)
			if err != nil {
				return fmt.Errorf("failed to download file %s: %w", itemPath, err)
			}

			if fileResp.StatusCode != http.StatusOK {
				return fmt.Errorf("failed to download file %s: status %d", itemPath, fileResp.StatusCode)
			}

			fileContent, err := io.ReadAll(fileResp.Body)
			_ = fileResp.Body.Close()
			if err != nil {
				return fmt.Errorf("failed to read file content %s: %w", itemPath, err)
			}

			//nolint:gosec // downloaded project files are intended to be readable by project tooling
			if err := os.WriteFile(itemLocalPath, fileContent, 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", itemLocalPath, err)
			}
		} else if itemType == "dir" {
			// Recursively download subdirectory
			log.Printf("Downloading directory: %s", itemPath)
			//nolint:gosec // scaffolded directories are intended to be readable/traversable
			if err := os.MkdirAll(itemLocalPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", itemLocalPath, err)
			}

			// Recursively download directory contents
			if err := downloadDirectoryContentsWithoutGhCli(ctx, repoSlug, itemPath, rootDirPath, branch, itemLocalPath, httpClient); err != nil {
				return fmt.Errorf("failed to download subdirectory %s: %w", itemPath, err)
			}
		}
	}

	return nil
}

// extractToolboxAndConnectionConfigs extracts toolbox resource definitions from the agent manifest
// and converts them into project.Toolbox config entries and project.ToolConnection entries.
// Tools with a target/authType also produce connection entries for Bicep provisioning.
// Built-in tools (bing_grounding, azure_ai_search, etc.) produce toolbox tools but no connections.
func extractToolboxAndConnectionConfigs(
	manifest *agent_yaml.AgentManifest,
) ([]project.Toolbox, []project.ToolConnection, map[string]string, error) {
	if manifest == nil || manifest.Resources == nil {
		return nil, nil, nil, nil
	}

	var toolboxes []project.Toolbox
	var connections []project.ToolConnection
	// credentialEnvVars maps generated env var names to their raw values so
	// the caller can persist them in the azd environment.
	credentialEnvVars := map[string]string{}

	for _, resource := range manifest.Resources {
		tbResource, ok := resource.(agent_yaml.ToolboxResource)
		if !ok {
			continue
		}

		description := tbResource.Description

		if len(tbResource.Tools) == 0 {
			return nil, nil, nil, fmt.Errorf(
				"toolbox resource '%s' is missing required 'tools'",
				tbResource.Name,
			)
		}

		var tools []map[string]any
		for i, rawTool := range tbResource.Tools {
			toolMap, ok := rawTool.(map[string]any)
			if !ok {
				return nil, nil, nil, fmt.Errorf(
					"toolbox resource '%s' has invalid tool entry: expected object",
					tbResource.Name,
				)
			}

			// Manifest and API both use "type" for tool kind
			toolType, _ := toolMap["type"].(string)

			target, _ := toolMap["target"].(string)
			if target == "" {
				// No target — either a built-in tool or a pre-configured tool
				// that already has project_connection_id. Pass through as-is.
				result := make(map[string]any, len(toolMap))
				maps.Copy(result, toolMap)
				tools = append(tools, result)
				continue
			}

			if toolType == "" {
				return nil, nil, nil, fmt.Errorf(
					"toolbox resource '%s': external tool at index %d has a 'target' but no 'type'",
					tbResource.Name, i,
				)
			}

			// External tools with target/authType need a connection
			toolName, _ := toolMap["name"].(string)
			authType, _ := toolMap["authType"].(string)
			authType = string(agent_yaml.NormalizeConnectionAuthType(agent_yaml.AuthType(authType)))
			credentials, _ := toolMap["credentials"].(map[string]any)

			connName := toolName
			if connName == "" {
				connName = fmt.Sprintf("%s-%s-%d", tbResource.Name, toolType, i)
			}

			conn := project.ToolConnection{
				Name:     connName,
				Category: "RemoteTool",
				Target:   target,
				AuthType: authType,
			}

			// Extract credentials, storing raw values as env vars and
			// replacing them with ${VAR} references in the config.
			if len(credentials) > 0 {
				conn.Credentials = externalizeCredentials(
					credentials, []string{connName}, credentialEnvVars,
				)
			}

			connections = append(connections, conn)

			// Preserve all tool fields, replacing consumed connection fields
			// with the project_connection_id reference.
			tool := make(map[string]any, len(toolMap))
			maps.Copy(tool, toolMap)
			tool["type"] = toolType
			tool["project_connection_id"] = connName
			delete(tool, "target")
			delete(tool, "authType")
			delete(tool, "credentials")
			tools = append(tools, tool)
		}

		toolboxes = append(toolboxes, project.Toolbox{
			Name:        tbResource.Name,
			Description: description,
			Tools:       tools,
		})
	}

	return toolboxes, connections, credentialEnvVars, nil
}

// credentialEnvVarName builds a deterministic env var name for a connection
// credential key, e.g. ("github-copilot", "clientSecret") → "PARAM_GITHUB_COPILOT_CLIENTSECRET".
// All non-alphanumeric characters are replaced with underscores and consecutive
// underscores are collapsed to produce a valid [A-Z0-9_]+ environment variable name.
var nonAlphanumRe = regexp.MustCompile(`[^A-Z0-9]+`)

func credentialEnvVarName(parts ...string) string {
	s := "PARAM_" + strings.ToUpper(strings.Join(parts, "_"))
	return nonAlphanumRe.ReplaceAllString(s, "_")
}

// externalizeCredentials recursively walks a credential map. String leaf values
// are stored as env vars and replaced with ${VAR} references. Nested maps are
// preserved structurally. keyPath accumulates segments for the env var name.
func externalizeCredentials(
	creds map[string]any,
	keyPath []string,
	envVars map[string]string,
) map[string]any {
	result := make(map[string]any, len(creds))
	for k, v := range creds {
		path := append(keyPath, k)
		switch val := v.(type) {
		case map[string]any:
			result[k] = externalizeCredentials(val, path, envVars)
		default:
			envName := credentialEnvVarName(path...)
			envVars[envName] = fmt.Sprintf("%v", val)
			result[k] = fmt.Sprintf("${%s}", envName)
		}
	}
	return result
}

// injectToolboxEnvVarsIntoDefinition adds TOOLBOX_{NAME}_MCP_ENDPOINT entries
// to the environment_variables section of a hosted agent definition for each toolbox
// resource in the manifest. Returns an error if two toolboxes produce the same
// environment variable name or if the key already exists in the definition.
func injectToolboxEnvVarsIntoDefinition(manifest *agent_yaml.AgentManifest) error {
	if manifest == nil || manifest.Resources == nil {
		return nil
	}

	containerAgent, ok := manifest.Template.(agent_yaml.ContainerAgent)
	if !ok {
		return nil
	}

	// Collect toolbox resource names
	var toolboxNames []string
	for _, resource := range manifest.Resources {
		if tbResource, ok := resource.(agent_yaml.ToolboxResource); ok {
			toolboxNames = append(toolboxNames, tbResource.Name)
		}
	}
	if len(toolboxNames) == 0 {
		return nil
	}

	if containerAgent.EnvironmentVariables == nil {
		envVars := []agent_yaml.EnvironmentVariable{}
		containerAgent.EnvironmentVariables = &envVars
	}

	existingNames := make(map[string]bool, len(*containerAgent.EnvironmentVariables))
	for _, ev := range *containerAgent.EnvironmentVariables {
		existingNames[ev.Name] = true
	}

	for _, tbName := range toolboxNames {
		envKey := envkey.ToolboxMCPEndpoint(tbName)
		if existingNames[envKey] {
			return fmt.Errorf(
				"duplicate toolbox environment variable %q (from toolbox %q)",
				envKey, tbName,
			)
		}
		existingNames[envKey] = true
		*containerAgent.EnvironmentVariables = append(
			*containerAgent.EnvironmentVariables,
			agent_yaml.EnvironmentVariable{
				Name:  envKey,
				Value: fmt.Sprintf("${%s}", envKey),
			},
		)
	}

	manifest.Template = containerAgent
	return nil
}

// extractConnectionConfigs extracts connection resource definitions from the agent manifest
// and converts them into project.Connection config entries. Credential values are externalized
// to environment variables and replaced with ${VAR} references in the returned connections.
func extractConnectionConfigs(
	manifest *agent_yaml.AgentManifest,
) ([]project.Connection, map[string]string, error) {
	if manifest == nil || manifest.Resources == nil {
		return nil, nil, nil
	}

	var connections []project.Connection
	credentialEnvVars := map[string]string{}

	for _, resource := range manifest.Resources {
		connResource, ok := resource.(agent_yaml.ConnectionResource)
		if !ok {
			continue
		}

		creds := maps.Clone(connResource.Credentials)
		authType := string(agent_yaml.NormalizeConnectionAuthType(connResource.AuthType))

		// Surface credentials.type to top-level authType when not explicitly set.
		// Do this before externalization so "type" isn't converted into an env var entry,
		// and normalize legacy auth types for provisioning compatibility.
		if authType == "" && len(creds) > 0 {
			if credType, ok := creds["type"].(string); ok && credType != "" {
				authType = string(agent_yaml.NormalizeConnectionAuthType(agent_yaml.AuthType(credType)))
				delete(creds, "type")
			}
		}

		// Externalize credential values to env vars and replace with ${VAR} references.
		if len(creds) > 0 {
			creds = externalizeCredentials(
				creds, []string{connResource.Name}, credentialEnvVars,
			)
		}

		conn := project.Connection{
			Name:                        connResource.Name,
			Category:                    string(connResource.Category),
			Target:                      connResource.Target,
			AuthType:                    authType,
			Credentials:                 creds,
			Metadata:                    connResource.Metadata,
			ExpiryTime:                  connResource.ExpiryTime,
			IsSharedToAll:               connResource.IsSharedToAll,
			SharedUserList:              connResource.SharedUserList,
			PeRequirement:               connResource.PeRequirement,
			PeStatus:                    connResource.PeStatus,
			UseWorkspaceManagedIdentity: connResource.UseWorkspaceManagedIdentity,
			Error:                       connResource.Error,
			AuthorizationUrl:            connResource.AuthorizationUrl,
			TokenUrl:                    connResource.TokenUrl,
			RefreshUrl:                  connResource.RefreshUrl,
			Scopes:                      connResource.Scopes,
			Audience:                    connResource.Audience,
			ConnectorName:               connResource.ConnectorName,
		}

		connections = append(connections, conn)
	}

	return connections, credentialEnvVars, nil
}

// validateCodeDeployFlags checks that required flags are present when using
// --deploy-mode code in --no-prompt mode.
func (a *InitAction) validateCodeDeployFlags() error {
	// First validate image flag (it has incompatibilities with other flags)
	if err := validateImageFlag(a.flags.image, a.flags.deployMode); err != nil {
		return err
	}
	return validateCodeDeployInput(
		a.flags.noPrompt, a.flags.deployMode, a.flags.runtime, a.flags.entryPoint, a.flags.depResolution)
}

var initImageRefRe = regexp.MustCompile(
	`^(?:(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?::[0-9]+)?|` +
		`localhost(?::[0-9]+)?|[a-z0-9](?:[a-z0-9-]*[a-z0-9])?:[0-9]+)/` +
		`[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*` +
		`(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*` +
		`(?::[\w][\w.-]{0,127}|@sha256:[0-9a-fA-F]{64})?$`,
)

// validateImageFlag checks that --image is valid when provided.
// Returns an error if:
// - --image is used with --deploy-mode code (incompatible)
// - --image URL format is invalid (must contain a fully qualified registry/image reference)
func validateImageFlag(image, deployMode string) error {
	if image == "" {
		return nil
	}

	// --image is incompatible with --deploy-mode code
	if deployMode == "code" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--image cannot be used with --deploy-mode code",
			"Use --image with --deploy-mode container (default) or omit --deploy-mode",
		)
	}

	// Require a fully-qualified image reference with an explicit registry host,
	// e.g. "myacr.azurecr.io/agent", "docker.io/myorg/agent:v1", or
	// "localhost:5000/agent@sha256:<digest>".
	if !initImageRefRe.MatchString(image) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid image URL %q: must be in format registry/image[:tag]", image),
			"Provide a fully qualified image URL like 'myacr.azurecr.io/agent:v1'",
		)
	}

	return nil
}

// validateCodeDeployInput is the shared validation logic for code deploy flags.
// Used by both InitAction and InitFromCodeAction.
func validateCodeDeployInput(noPrompt bool, deployMode, runtime, entryPoint, depResolution string) error {
	if deployMode != "" && deployMode != "container" && deployMode != "code" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--deploy-mode must be 'container' or 'code'",
			"Specify --deploy-mode container or --deploy-mode code",
		)
	}
	if runtime != "" {
		validRuntimes := map[string]bool{
			"python_3_13": true,
			"python_3_14": true,
			"dotnet_10":   true,
		}
		if !validRuntimes[runtime] {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--runtime must be one of: python_3_13, python_3_14, dotnet_10",
				"Specify a valid runtime value",
			)
		}
	}
	if depResolution != "" && depResolution != "remote_build" && depResolution != "bundled" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--dep-resolution must be 'remote_build' or 'bundled'",
			"Specify --dep-resolution remote_build or --dep-resolution bundled",
		)
	}
	if noPrompt && deployMode == "code" {
		if runtime == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--runtime is required when using --deploy-mode code with --no-prompt",
				"Specify --runtime (e.g., python_3_13, python_3_14, dotnet_10)",
			)
		}
		if entryPoint == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--entry-point is required when using --deploy-mode code with --no-prompt",
				"Specify --entry-point (e.g., app.py, main.py, MyAgent.dll)",
			)
		}
	}
	return nil
}

// formatCreatedFolderMessage builds the user-facing message shown after a new
// project folder is created. It computes a cross-platform relative display path
// and optionally notes the original template title when the folder name differs.
func formatCreatedFolderMessage(originalCwd, createdFolder, createdFromTitle string) string {
	displayPath := createdFolder
	if relPath, err := filepath.Rel(originalCwd, createdFolder); err == nil {
		displayPath = filepath.ToSlash(relPath)
	}

	msg := fmt.Sprintf("\nYour project has been created in %s", displayPath)
	if createdFromTitle != "" && filepath.Base(createdFolder) != createdFromTitle {
		msg += fmt.Sprintf(" (from template %q)", createdFromTitle)
	}
	msg += fmt.Sprintf("\n  cd %s\n", displayPath)

	return msg
}
