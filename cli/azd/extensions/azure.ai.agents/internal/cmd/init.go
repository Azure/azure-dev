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
	"net/http"
	"net/url"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/containerref"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
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
	acrConnection     string
	modelDeployment   string
	model             string
	manifestPointer   string
	agentName         string
	agentNameExplicit bool
	description       string
	src               string
	env               string
	protocols         []string
	// deploy mode flags for non-interactive code deploy support
	deployMode    string // "container" or "code"; empty = prompt interactively
	runtime       string // e.g. "python_3_13", "python_3_14", "dotnet_10"
	entryPoint    string // e.g. "app.py", "MyAgent.dll"
	depResolution string // "remote_build" or "bundled"; defaults to "remote_build"
	// image specifies a pre-built container image URL (e.g., "myacr.azurecr.io/agent:v1").
	// Init writes the image directly to an azure.yaml service, skipping
	// template/language selection, source scaffolding, and ACR creation.
	// Requires --agent-name and is incompatible with --deploy-mode code.
	image string
	// registryConnection identifies an existing Foundry project connection used
	// to pull a private pre-built image. The value is passed through as a generic
	// connection name or ID; azd does not inspect registry-specific configuration.
	registryConnection string
	// voice optionally overrides the output voice name for prompt-voice agents.
	voice string
	// instructions overrides system instructions for prompt and managed agents.
	instructions string
	// force, when true, lets headless callers (--no-prompt) pre-consent to
	// overwrite prompts that would otherwise return a structured error. It
	// mirrors the `--force` convention used by `azd down`, `azd env remove`,
	// `azd config reset`, and `azd infra generate`.
	force bool
	// kind, when set, explicitly selects the agent runtime ("hosted",
	// "prompt", or "prompt-voice") and bypasses the interactive kind
	// prompt. This is primarily for non-interactive callers (--no-prompt) and
	// automation; interactive users get the kind prompt when this is empty.
	// A harnessed ("managed") agent is not one of these values: it is "prompt"
	// plus a --harness.
	// "prompt-voice" writes a declarative (managed) voice service directly to
	// azure.yaml (no code/image, template/language selection, or ACR).
	kind string
	// harness, when set, names the execution harness written to the scaffolded
	// prompt agent definition (only "github_copilot_preview" is supported today). A
	// harness is what makes a prompt agent a "managed" agent; there is no
	// separate --kind for it. Ignored for hosted agents.
	harness string
	// noPrompt is resolved from the extension context (--no-prompt / AZD_NO_PROMPT)
	// and is not registered as a CLI flag on the init command itself.
	noPrompt bool
	// infra selects the IaC flavor to eject from azure.yaml. Existing projects
	// may receive a dedicated infra/foundry provisioning layer.
	// Empty means the flag was not passed (bicepless default, no files). A bare
	// `--infra` resolves to "bicep" via the flag's NoOptDefVal; `--infra=terraform`
	// and `--infra=bicep` are explicit. The eject runs after a fresh init or
	// standalone when azure.yaml already exists.
	infra string
	// raiPolicy selects the Responsible AI policy a prompt or managed agent
	// binds to. Empty means "ask" (or, with --no-prompt, attach nothing).
	raiPolicy string
}

// AiProjectResourceConfig represents the configuration for an AI project resource
type AiProjectResourceConfig struct {
	Models []map[string]any `json:"models,omitempty"`
}

type InitAction struct {
	azdClient            *azdext.AzdClient
	credential           azcore.TokenCredential
	projectConfig        *azdext.ProjectConfig
	environment          *azdext.Environment
	flags                *initFlags
	serviceNameOverride  string
	createdFolderDisplay string // pre-computed relative display path for the created folder

	// selectedFoundryProject holds the existing Foundry project resolved during
	// init (nil when creating a new project).
	selectedFoundryProject *FoundryProjectInfo
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
			"update azd to the latest version (https://aka.ms/azd/upgrade) and retry",
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

// readInitSourceContent returns the raw source bytes for local files and
// supported public GitHub URLs without invoking the GitHub CLI.
func readInitSourceContent(
	ctx context.Context, manifestPointer string, httpClient *http.Client,
) ([]byte, bool) {
	// Local file path: bypass URL handling entirely so a relative path like
	// A relative path that happens to look URL-ish is still read from disk.
	if !strings.HasPrefix(manifestPointer, "http://") && !strings.HasPrefix(manifestPointer, "https://") {
		info, statErr := os.Stat(manifestPointer)
		if statErr != nil || info.IsDir() {
			return nil, false
		}
		//nolint:gosec // source path is an explicit user-provided local path
		content, err := os.ReadFile(manifestPointer)
		if err != nil {
			log.Printf("peek manifest name: read %s: %v", manifestPointer, err)
			return nil, false
		}
		return content, true
	}

	// GitHub URL: try naive parsing and an unauthenticated HTTP GET for
	// public repositories.
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

// parseGitHubUrlNaive parses public GitHub file URLs whose branch is a
// single path segment.
func parseGitHubUrlNaive(manifestPointer string) *GitHubUrlInfo {
	parsedURL, err := url.Parse(manifestPointer)
	if err != nil {
		return nil
	}

	if strings.EqualFold(parsedURL.Hostname(), "github.com") && strings.Contains(parsedURL.Path, "/blob/") {
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

// kindFlagPromptVoice is the accepted --kind value for a declarative voice agent.
const kindFlagPromptVoice = "prompt-voice"

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

type existingAgentNameConflictOptions struct {
	noPromptSuggestion string
}

type existingAgentNameConflictOption func(*existingAgentNameConflictOptions)

func withNoPromptAgentNameConflictSuggestion(suggestion string) existingAgentNameConflictOption {
	return func(options *existingAgentNameConflictOptions) {
		options.noPromptSuggestion = suggestion
	}
}

func resolveExistingAgentNameConflict(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	environment *azdext.Environment,
	credential azcore.TokenCredential,
	noPrompt bool,
	agentName string,
	options ...existingAgentNameConflictOption,
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
	return resolveExistingAgentNameConflictWithChecker(
		ctx,
		azdClient,
		agentClient,
		noPrompt,
		agentName,
		options...,
	)
}

func resolveExistingAgentNameConflictWithChecker(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentChecker agents.AgentChecker,
	noPrompt bool,
	agentName string,
	options ...existingAgentNameConflictOption,
) (string, error) {
	resolutionOptions := existingAgentNameConflictOptions{
		noPromptSuggestion: "To create a separate agent, re-run init with --agent-name <unique-name>.\n",
	}
	for _, option := range options {
		option(&resolutionOptions)
	}

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
				"%s",
				resolutionOptions.noPromptSuggestion,
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

// agentDefiningFlagsSet reports whether the caller passed any flag that
// describes the agent to set up.
//
// Reusing an already-configured project is only safe when the command was not
// told what to build. Each of these flags feeds a value init would otherwise
// prompt for, so reusing while one is set would silently discard it — notably
// under --no-prompt, where reuse is unconditional.
//
// srcBlocksReuse must come from cmd.Flags().Changed("src") for project reuse,
// not from flags.src:
// applyPositionalArg folds a positional directory into flags.src, so testing
// the field would make `azd ai agent init .` — a documented form — skip reuse
// and re-prompt, which is the very behavior issue #9154 reports.
// --env and --infra are deliberately absent: they describe the environment and
// the IaC output rather than the agent, and both stay meaningful on a reuse run.
func agentDefiningFlagsSet(flags *initFlags, srcBlocksReuse bool) bool {
	return flags.agentName != "" ||
		flags.deployMode != "" ||
		flags.runtime != "" ||
		flags.entryPoint != "" ||
		flags.depResolution != "" ||
		flags.model != "" ||
		flags.modelDeployment != "" ||
		flags.projectResourceId != "" ||
		flags.acrConnection != "" ||
		flags.image != "" ||
		flags.registryConnection != "" ||
		flags.kind != "" ||
		flags.voice != "" ||
		srcBlocksReuse ||
		len(flags.protocols) > 0
}

// canReuseExistingAgentConfiguration reports whether init may reuse an agent
// service already defined by the active unified project without discarding
// caller intent.
func canReuseExistingAgentConfiguration(
	flags *initFlags,
	manifestDetectedButDeclined bool,
	srcBlocksReuse bool,
) bool {
	return flags.manifestPointer == "" &&
		!flags.force &&
		!manifestDetectedButDeclined &&
		!agentDefiningFlagsSet(flags, srcBlocksReuse)
}

func newInitCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &initFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "init [<path>] [-m <azure.yaml pointer>] [--src <source directory>]",
		Short: fmt.Sprintf("Initialize a new prompt, hosted, or voice agent project. %s", color.YellowString("(Preview)")),
		Long: `Initialize a new prompt, hosted, or voice agent project.

Unified projects:
When -m points at a unified azure.yaml (a project manifest that declares
services with host: azure.ai.project / azure.ai.agent / ...), that azure.yaml
is adopted as the project manifest and its referenced files are placed at the
project root. Standalone agent definitions and AgentManifest template wrappers
are rejected with migration guidance.

Voice Agents:
Use --kind prompt-voice to initialize a managed prompt voice agent without
source code or container scaffolding.
The managed model defaults to gpt-realtime and does not require a model deployment.
--voice sets the output voice only when creating a new prompt voice agent through
--kind prompt-voice or the interactive voice option.
Edit azure.yaml to customize existing voice settings.

New prompt voice initialization does not use source directories or code/container
settings. Explicit --src (including a positional directory), --protocol,
--deploy-mode, --runtime, --entry-point, and --dep-resolution are rejected on
the voice path. Use --model for the managed voice model; --model-deployment and
prompt-only or registry options are not supported by this voice initialization.

Prompt voice services support modelType: managed or self_deployed (bring your own model
deployment), audio input/output, structured inputs, tools, greeting, avatar,
handoff, and telephony bindings (acs or twilio). Hosted voice wrappers use
conversationEngine.type: hosted_agent and conversationEngine.name to reference
the hosted target service in azure.yaml. The old modelType: hosted_agent and
targetAgent settings are not supported; use conversationEngine instead. Initialize from a sample
azure.yaml containing both the hosted target and the voice wrapper.
Configure advanced settings in azure.yaml.
Run 'azd provision' and 'azd deploy' to deploy voice services, then connect to
the voice WebSocket endpoint with a Voice Live client.

Agent Names:
The agent name written to azure.yaml is the Foundry agent identity. Foundry
agents are unique by name within a project, so deploying with an existing name
creates a new version of that existing agent instead of a separate agent.

Use --agent-name to choose a unique Foundry agent name when initializing from
a reusable unified project.

File Exclusions:
A default .agentignore file is generated to control which files are excluded
from code-deploy ZIP packaging (uses .gitignore syntax).`,
		Example: `  # Adopt a sample's unified azure.yaml as the project manifest
  azd ai agent init -m ./azure.yaml
  azd ai agent init -m https://github.com/Azure-Samples/<repo>/blob/main/azure.yaml

  # Adopt a unified project with a unique Foundry agent name
  azd ai agent init -m ./azure.yaml --agent-name my-unique-agent

  # Initialize from local agent code
  azd ai agent init --src ./src/my-agent --agent-name my-unique-agent

  # Initialize a managed prompt voice agent
  azd ai agent init --kind prompt-voice --agent-name support-voice

  # Initialize a prompt voice agent with an explicit realtime model and voice
  azd ai agent init --kind prompt-voice --agent-name support-voice \
    --model gpt-realtime --voice en-US-Ava:DragonHDLatestNeural

  # Non-interactive code deploy (CI/CD)
  azd ai agent init --no-prompt --project-id "<resource-id>" \
    --deploy-mode code --runtime python_3_13 --entry-point app.py

  # Non-interactive prompt agent against an existing Foundry project
  azd ai agent init --no-prompt --kind prompt --agent-name my-agent \
    --project-id "<resource-id>" --model-deployment gpt-4.1-mini

  # Non-interactive unified project adoption
  azd ai agent init --no-prompt -m ./azure.yaml --project-id "<resource-id>"

  # Bring your own pre-built image (no template/language selection, Dockerfile, or ACR setup)
  azd ai agent init --no-prompt --agent-name my-agent \
    --image myacr.azurecr.io/agents/my-agent:v1

  # Use an existing Foundry connection for a private pre-built image
  azd ai agent init --no-prompt --agent-name my-agent --project-id "<resource-id>" \
    --image registry.example.com/agents/my-agent:v1 --registry-connection production-registry`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.noPrompt = extCtx.NoPrompt
			if flags.env == "" {
				flags.env = extCtx.Environment
			}

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
			if userProvidedManifest {
				if err := checkNotDirectory(flags.manifestPointer); err != nil {
					return err
				}
			}
			voiceSpecified := cmd.Flags().Changed("voice")
			if err := validateInitVoiceInput(flags, voiceSpecified); err != nil {
				return err
			}
			// Capture explicit inputs before discovery/scaffolding fills internal defaults.
			voiceInputErr := validateVoiceInitOptions(cmd, len(args) > 0 && flags.src != "")
			if voiceSpecified || (flags.manifestPointer == "" &&
				strings.EqualFold(strings.TrimSpace(flags.kind), kindFlagPromptVoice)) {
				if voiceInputErr != nil {
					return voiceInputErr
				}
			}
			isPromptVoice := flags.manifestPointer == "" &&
				strings.EqualFold(strings.TrimSpace(flags.kind), kindFlagPromptVoice)
			if flags.image != "" {
				if err := validateImageFlag(flags.image, flags.deployMode); err != nil {
					return err
				}
			}
			if err := validateFastPathAgentName(flags, isPromptVoice); err != nil {
				return err
			}

			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return exterrors.Internal(exterrors.CodeAzdClientFailed, fmt.Sprintf("failed to create azd client: %s", err))
			}
			defer azdClient.Close()
			printBanner(cmd.OutOrStdout())

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

			// `--infra` inside a project that already declares a Foundry service
			// is a standalone eject: synthesize infra (Bicep or Terraform) from
			// the existing azure.yaml, write ./infra/, and return without
			// prompting.
			//
			// Any other project — including one azd already manages that has no
			// Foundry service yet — has nothing to eject, so `--infra` falls
			// through to the normal init flow and ejects afterwards via
			// ejectInfraAfterInit. See resolveInfraGate.
			if infraProvider != "" {
				gate, gateErr := resolveInfraGate(infraProvider)
				if gateErr != nil {
					return gateErr
				}
				if gate.standaloneEject {
					// Reject init inputs the eject path would silently ignore
					// instead of pretending they were honored. They stay valid
					// on the init fall-through, where they do drive the flow.
					if err := validateStandaloneEjectArgs(cmd, args); err != nil {
						return err
					}
					env, err := readInfraEjectEnvironment(ctx, azdClient)
					if err != nil {
						return err
					}
					return ejectInfra(gate.projectRoot, infraProvider, env)
				}
			}

			flags.agentNameExplicit = cmd.Flags().Changed("agent-name")

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

			// Explicit YAML input is authoritative and must be a unified azure.yaml.
			if userProvidedManifest {
				content, err := loadExplicitAzureYaml(ctx, azdClient, flags, httpClient)
				if err != nil {
					return err
				}
				if err := validateUnifiedInitFlags(cmd); err != nil {
					return err
				}
				if err := runInitFromAzureYaml(ctx, flags, azdClient, httpClient, content); err != nil {
					if exterrors.IsCancellation(err) {
						return exterrors.Cancelled("initialization was cancelled")
					}
					return err
				}
				return ejectInfraAfterInit(ctx, infraProvider, azdClient)
			}

			// With no explicit manifest, --kind selects the runtime directly. A
			// harness is an optional capability of kind: prompt, not a separate
			// agent kind. Omitting --kind preserves the existing hosted flow.
			requestedKind := agentKindChoice(strings.ToLower(strings.TrimSpace(flags.kind)))
			isPromptVoice = strings.EqualFold(strings.TrimSpace(flags.kind), kindFlagPromptVoice)
			if err := validateInitKindHarness(requestedKind, flags.kind, flags.harness, isPromptVoice); err != nil {
				return err
			}

			switch {
			case requestedKind == AgentKindChoicePrompt:
				harness, harnessErr := resolveInitHarness(flags.harness, "")
				if harnessErr != nil {
					return harnessErr
				}
				return runInitManaged(ctx, flags, azdClient, harness)
			}
			if strings.TrimSpace(flags.instructions) != "" {
				return promptOnlyInstructionsError()
			}

			// Project().Get discovers a parent azd project even when init runs
			// from one of its subdirectories.
			projectResponse, projectErr := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})

			// Validate --kind prompt-voice and its incompatible options before either
			// synthesis branch. The image and prompt-voice fast paths both mutate
			// flags.manifestPointer, so validating inside one branch is unreachable
			// when the other runs first (e.g. --kind prompt-voice --image would
			// otherwise silently create a hosted image agent).
			if isPromptVoice {
				if strings.EqualFold(flags.kind, kindFlagPromptVoice) && flags.image != "" {
					return exterrors.Validation(
						exterrors.CodeInvalidParameter,
						"--kind prompt-voice cannot be combined with --image",
						"a voice agent is managed and has no container image; drop --image",
					)
				}
				if strings.EqualFold(flags.kind, kindFlagPromptVoice) && flags.manifestPointer != "" {
					return exterrors.Validation(
						exterrors.CodeInvalidParameter,
						"--kind prompt-voice cannot be combined with --manifest",
						"a voice agent is synthesized from --agent-name/--model; "+
							"drop --manifest, or omit --kind to adopt the manifest as-is",
					)
				}
			}

			if err := validateRegistryConnectionFlag(
				flags.registryConnection,
				flags.image,
				false,
				flags.deployMode,
				flags.kind,
			); err != nil {
				return err
			}
			flags.registryConnection = strings.TrimSpace(flags.registryConnection)

			if flags.image != "" {
				targetDir, folderDisplay := fastPathProjectTarget(
					projectResponse.GetProject(), projectErr, flags.agentName,
				)
				action := &InitFromCodeAction{
					azdClient:         azdClient,
					flags:             flags,
					projectTargetDir:  targetDir,
					createdFolderPath: folderDisplay,
				}
				if err := action.Run(ctx); err != nil {
					return err
				}
				return ejectInfraAfterInit(ctx, infraProvider, azdClient)
			}
			if isPromptVoice {
				targetDir, folderDisplay := fastPathProjectTarget(
					projectResponse.GetProject(), projectErr, flags.agentName,
				)
				if err := runInitVoice(ctx, flags, azdClient, targetDir, folderDisplay); err != nil {
					return err
				}
				return ejectInfraAfterInit(ctx, infraProvider, azdClient)
			}

			if projectErr != nil || projectResponse.GetProject() == nil {
				checkDir := flags.src
				if checkDir == "" {
					checkDir = "."
				}
				legacyFile, err := findExistingAgentYaml(checkDir)
				if err != nil {
					return fmt.Errorf("checking for legacy init files: %w", err)
				}
				if legacyFile != "" {
					return legacyInitSourceError(legacyFile)
				}
			}

			// When the project's own manifest already declares agent
			// service(s), the values init would prompt for (agent name,
			// protocols, deploy mode) are already recorded there. Offer to
			// reuse that configuration instead of re-asking (issue #9154).
			//
			// Any flag that describes the agent to set up states intent to
			// configure that agent, so it opts out of reuse and falls through
			// to the normal flow. Without that, a scripted
			// `--no-prompt --deploy-mode code --runtime ...` in a repo that
			// already declares an agent would silently no-op instead of
			// honoring the flags the caller passed.
			if !voiceSpecified && canReuseExistingAgentConfiguration(
				flags,
				false,
				cmd.Flags().Changed("src"),
			) {
				detection := detectProjectAgentServices(ctx, azdClient)
				if len(detection.services) > 0 &&
					!positionalSourceOptsOutOfReuse(
						flags.src,
						detection.projectRoot,
						detection.services,
					) {
					useExisting := flags.noPrompt
					if !flags.noPrompt {
						confirmResp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
							Options: &azdext.ConfirmOptions{
								Message: fmt.Sprintf(
									"This project already configures %s. Use it?",
									describeProjectAgentServices(detection.services),
								),
								DefaultValue: new(true),
							},
						})
						if promptErr != nil {
							if exterrors.IsCancellation(promptErr) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return fmt.Errorf("prompting for project agent reuse: %w", promptErr)
						}
						useExisting = *confirmResp.Value
					}
					if useExisting {
						if err := runReuseProjectAgentServices(
							ctx, flags, azdClient, detection.services,
						); err != nil {
							return err
						}
						return ejectInfraAfterInit(ctx, infraProvider, azdClient)
					}
				}
			}

			{
				// No manifest provided - prompt user for init mode
				initMode, err := promptInitModeForVoice(ctx, azdClient, flags.noPrompt, voiceSpecified, voiceInputErr)
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

						// Resolve --agent-name only when the user explicitly
						// provided it. Unified azure.yaml adoption can contain
						// multiple agent services, so an interactive/default
						// single name must not be treated as an override.
						defaultName := foundryProjectName(content)
						if defaultName == "" {
							defaultName = folderNameStrippingParenSuffix(selectedTemplate.Title)
						}

						resolvedName := defaultName
						if flags.agentNameExplicit {
							var err error
							resolvedName, err = resolveInitAgentName(ctx, azdClient, flags, defaultName)
							if err != nil {
								if exterrors.IsCancellation(err) {
									return exterrors.Cancelled("initialization was cancelled")
								}
								return err
							}
							flags.agentName = resolvedName
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

					case TemplateTypeAzd:
						if err := validateUnifiedInitFlags(cmd); err != nil {
							return err
						}
						if err := runInitFromAzdTemplate(
							ctx, flags, azdClient, selectedTemplate,
						); err != nil {
							if exterrors.IsCancellation(err) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return err
						}
					default:
						return exterrors.Validation(
							exterrors.CodeInvalidAgentManifest,
							fmt.Sprintf("unsupported agent template type %q", selectedTemplate.EffectiveType()),
							"Choose a unified azure.yaml or full azd repository template.",
						)
					}

				case initModeVoice:
					resolvedName, err := resolveInitAgentName(ctx, azdClient, flags, "voice-agent")
					if err != nil {
						if exterrors.IsCancellation(err) {
							return exterrors.Cancelled("initialization was cancelled")
						}
						return err
					}
					flags.agentName = resolvedName

					targetDir, folderDisplay := fastPathProjectTarget(
						projectResponse.GetProject(), projectErr, resolvedName,
					)
					if err := runInitVoice(ctx, flags, azdClient, targetDir, folderDisplay); err != nil {
						if exterrors.IsCancellation(err) {
							return exterrors.Cancelled("initialization was cancelled")
						}
						return err
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
			return ejectInfraAfterInit(ctx, infraProvider, azdClient)
		},
	}

	cmd.Flags().StringVarP(&flags.projectResourceId, "project-id", "p", "",
		"Existing Microsoft Foundry Project Id to initialize your azd environment with")

	cmd.Flags().StringVar(&flags.acrConnection, "acr-connection", "",
		"Foundry Azure Container Registry connection name to use for an existing project; "+
			"incompatible with code deploy, --image, and prompt-voice agents")

	cmd.Flags().StringVarP(&flags.modelDeployment, "model-deployment", "d", "",
		"Name of an existing model deployment to use from the Foundry project. Only used when paired with an existing Foundry project, either via --project-id or interactive prompts")

	cmd.Flags().StringVar(&flags.model, "model", "",
		fmt.Sprintf(
			"For hosted and prompt agents, name of the AI model to deploy. "+
				"Defaults to '%s' during interactive model selection; "+
				"required to deploy a new model with --no-prompt. If --model-deployment is also provided, "+
				"--model-deployment takes precedence. For new managed prompt voice agents, selects the "+
				"service-hosted model (default: gpt-realtime); no model deployment is created.",
			defaultAgentModel,
		))

	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "",
		"Path or supported GitHub URI to a unified azure.yaml project document")

	cmd.Flags().StringVar(&flags.agentName, "agent-name", "",
		"Foundry agent name to write to azure.yaml. Reusing a name creates a new version of the existing agent.")

	cmd.Flags().StringVar(&flags.description, "description", "",
		"Prompt-agent description to write to azure.yaml. Used as the agent's human-readable summary.")
	cmd.Flags().StringVar(&flags.instructions, "instructions", "",
		"System instructions for a prompt agent, including one using --harness. Written to azure.yaml; not supported for hosted agents.")

	cmd.Flags().StringVarP(&flags.src, "src", "s", "",
		"Source directory for generated agents, or target directory when adopting a unified project")

	cmd.Flags().StringSliceVar(&flags.protocols, "protocol", nil,
		fmt.Sprintf("Protocols supported by the agent (%s). Can be specified multiple times.", knownProtocolNames()))

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
			"Skips template/language selection, code scaffolding, "+
			"Dockerfile generation, and ACR setup, and requires --agent-name. "+
			"Incompatible with --deploy-mode code.")

	cmd.Flags().StringVar(&flags.registryConnection, "registry-connection", "",
		"Name or ID of an existing Foundry project connection used to pull a private pre-built container image. "+
			"Requires a pre-built image and is incompatible with code deploy.")

	cmd.Flags().StringVar(&flags.voice, "voice", "",
		"Output voice for new prompt voice agents (--kind prompt-voice or the interactive voice option). "+
			"Rejected for other init flows. For existing voice services, edit azure.yaml. "+
			"Example: en-US-Ava:DragonHDLatestNeural.")

	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Create a new agent service instead of reusing an existing unified project agent configuration.")

	cmd.Flags().StringVar(&flags.kind, "kind", "",
		"Agent runtime to initialize: 'hosted' (bring your own code/container), 'prompt' "+
			"(model + instructions; Foundry runs the agent), or 'prompt-voice' (a declarative "+
			"voice agent; use --model for the speech-to-speech model and --voice for the output "+
			"voice agent). When omitted, the hosted runtime is used. With --no-prompt, "+
			"'prompt' requires --agent-name and either --model or --model-deployment.")
	cmd.Flags().StringVar(&flags.harness, "harness", "",
		"Optional execution harness for --kind prompt: 'github_copilot_preview' (GitHub Copilot Brain+Hand).")
	_ = cmd.Flags().MarkHidden("harness")
	cmd.Flags().StringVar(&flags.infra, "infra", "",
		"Eject infrastructure-as-code from azure.yaml. Existing infrastructure is preserved and "+
			"Foundry files are generated as a separate infra/foundry layer. "+
			"A bare --infra ejects Bicep; --infra=terraform ejects Terraform and sets "+
			"the Foundry layer provider to terraform; Bicep keeps the microsoft.foundry provider. "+
			"--infra=bicep is explicit Bicep. "+
			"When azure.yaml already declares a Foundry project service, runs as a standalone "+
			"eject and skips the init prompts; otherwise init runs first and the eject follows it.")
	// NoOptDefVal makes a bare `--infra` resolve to "bicep" while still allowing
	// `--infra=terraform` / `--infra=bicep`. Absent flag stays "" (no eject).
	cmd.Flags().Lookup("infra").NoOptDefVal = project.BicepProviderName

	cmd.Flags().StringVar(&flags.raiPolicy, "rai-policy", "",
		"Responsible AI policy for a prompt or managed agent: 'none' to inherit the account's "+
			"default content filters, a policy name on the selected Foundry account, or a policy's "+
			"full ARM resource ID. The policy must already exist; azd attaches it, it does not "+
			"create it. When omitted, you are prompted to pick from the policies on the account; "+
			"with --no-prompt no policy is attached. "+
			"Ignored for hosted agents. Explicit --rai-policy is rejected when adopting unified "+
			"azure.yaml or a full repository template; declare policies in azure.yaml instead.")

	return cmd
}

func fastPathProjectTarget(
	projectConfig *azdext.ProjectConfig,
	projectErr error,
	agentName string,
) (string, string) {
	if projectErr == nil && projectConfig != nil {
		return ".", ""
	}
	targetDir := sanitizeAgentName(agentName)
	if _, err := os.Stat(targetDir); errors.Is(err, fs.ErrNotExist) {
		return targetDir, filepath.ToSlash(targetDir)
	}
	return targetDir, ""
}

func validateFastPathAgentName(flags *initFlags, isPromptVoice bool) error {
	if flags.image == "" && !isPromptVoice {
		return nil
	}
	if flags.agentName == "" {
		flag := "--image"
		if isPromptVoice {
			flag = "--kind prompt-voice"
		}
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			flag+" requires --agent-name",
			"pass --agent-name <name>",
		)
	}
	validatedName, err := validateInitAgentName(flags.agentName)
	if err != nil {
		return err
	}
	flags.agentName = validatedName
	return nil
}

func unusedInitVoiceError() error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"--voice is only supported when creating a new prompt voice agent",
		"use --kind prompt-voice or select the interactive voice option; "+
			"otherwise remove --voice and edit voice settings in azure.yaml",
	)
}

// validateInitVoiceInput rejects known no-op paths before authentication or file
// downloads. With no explicit kind, interactive voice selection remains valid.
func validateInitVoiceInput(flags *initFlags, specified bool) error {
	if !specified {
		return nil
	}
	voiceKind := strings.EqualFold(strings.TrimSpace(flags.kind), kindFlagPromptVoice)
	if flags.manifestPointer != "" || flags.image != "" ||
		(flags.kind != "" && !voiceKind) || (flags.noPrompt && !voiceKind) {
		return unusedInitVoiceError()
	}
	return nil
}

func promptInitModeForVoice(
	ctx context.Context, client *azdext.AzdClient, noPrompt, voiceSpecified bool,
	voiceInputErr error,
) (string, error) {
	mode, err := promptInitMode(ctx, client, noPrompt)
	if err != nil {
		return "", err
	}
	if voiceSpecified && mode != initModeVoice {
		return "", unusedInitVoiceError()
	}
	if mode == initModeVoice && voiceInputErr != nil {
		return "", voiceInputErr
	}
	return mode, nil
}

// validateVoiceInitOptions checks only explicitly supplied options that cannot
// affect a synthesized voice agent. It does not constrain other init flows or
// infer user intent from defaults populated later during scaffolding.
func validateVoiceInitOptions(cmd *cobra.Command, positionalSource bool) error {
	var conflicts []string
	if positionalSource {
		conflicts = append(conflicts, "positional source directory")
	}
	for _, name := range []string{
		"src", "protocol", "deploy-mode", "runtime", "entry-point", "dep-resolution",
		"image", "acr-connection", "registry-connection", "harness", "instructions",
		"description", "rai-policy", "model-deployment",
	} {
		if cmd.Flags().Changed(name) {
			conflicts = append(conflicts, "--"+name)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		"new prompt voice agents cannot use these init inputs: "+strings.Join(conflicts, ", "),
		"remove these inputs to create a prompt voice agent; use the hosted init flow for source/code settings "+
			"or the prompt init flow for prompt-only settings",
	)
}

func validateUnifiedInitFlags(cmd *cobra.Command) error {
	var conflicts []string
	for _, name := range []string{
		"description",
		"force",
		"harness",
		"instructions",
		"kind",
		"protocol",
		"rai-policy",
		"voice",
	} {
		if cmd.Flags().Changed(name) {
			conflicts = append(conflicts, "--"+name)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}

	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		fmt.Sprintf(
			"unified azure.yaml adoption cannot apply these explicitly set inputs: %s",
			strings.Join(conflicts, ", "),
		),
		"Remove the conflicting flags or update the agent services in azure.yaml before running init.",
	)
}

func validateInitKindHarness(requestedKind agentKindChoice, rawKind, harness string, isPromptVoice bool) error {
	if strings.TrimSpace(harness) != "" && requestedKind != AgentKindChoicePrompt {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--harness is only valid with --kind prompt",
			fmt.Sprintf("use --kind prompt --harness %s", agent_api.ManagedAgentHarnessGitHubCopilot),
		)
	}
	if rawKind != "" && !isPromptVoice &&
		requestedKind != AgentKindChoiceHosted &&
		requestedKind != AgentKindChoicePrompt {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown --kind value %q", rawKind),
			"supported values are: hosted, prompt, prompt-voice",
		)
	}
	return nil
}

func validateInitInstructions(
	requestedKind agentKindChoice,
	rawKind string,
	instructions string,
	isPromptVoice bool,
) error {
	if strings.TrimSpace(instructions) == "" {
		return nil
	}
	if requestedKind == AgentKindChoiceHosted || isPromptVoice {
		return promptOnlyInstructionsError()
	}
	if strings.TrimSpace(rawKind) != "" &&
		requestedKind != AgentKindChoicePrompt {
		return promptOnlyInstructionsError()
	}
	return nil
}

func promptOnlyInstructionsError() error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"--instructions is only supported for prompt agents",
		"use --kind prompt, or remove --instructions for a hosted agent",
	)
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
						"the '%s' provider. If you need Azure infrastructure for deployment, "+
						"declare that provider in azure.yaml, or run "+
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
// staging dir when generating a project, or a sample directory carrying a
// unified azure.yaml when adopting it (#8798). azd-core
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

// hasFoundryProviderDeclared reports whether azure.yaml declares the Foundry
// provider at the root or on an infrastructure layer. Project().Get exposes
// only root infra options today, so inspect the project file for layers.
func hasFoundryProviderDeclared(proj *azdext.ProjectConfig) bool {
	if proj == nil || proj.Infra == nil {
		return false
	}
	if proj.Path == "" {
		return proj.Infra.Provider == project.FoundryProviderName
	}

	var raw []byte
	for _, name := range azdcontext.ProjectFileNames {
		data, err := os.ReadFile(filepath.Join(proj.Path, name)) //nolint:gosec // project path is from azd
		if err == nil {
			raw = data
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return proj.Infra.Provider == project.FoundryProviderName
		}
	}
	if raw == nil {
		return proj.Infra.Provider == project.FoundryProviderName
	}
	var doc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Layers   []struct {
				Provider string `yaml:"provider"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return proj.Infra.Provider == project.FoundryProviderName
	}
	if doc.Infra.Provider == project.FoundryProviderName && len(doc.Infra.Layers) == 0 {
		return true
	}
	for _, layer := range doc.Infra.Layers {
		provider := layer.Provider
		if provider == "" {
			provider = doc.Infra.Provider
		}
		if provider == project.FoundryProviderName {
			return true
		}
	}
	return false
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
// instead of a unified azure.yaml file.
func checkNotDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}

	return exterrors.Validation(
		exterrors.CodeInvalidManifestPointer,
		fmt.Sprintf("'%s' is a directory, not a unified azure.yaml file", safeInitSourceDisplay(path)),
		"the --manifest flag must point to a unified azure.yaml file, not a directory",
	)
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

func printAgentAddedMessage(agentName string) {
	fmt.Printf("\nAdded agent '%s' to azure.yaml.\n", agentName)
}

// addVoiceAgentToProject writes a prompt-voice (declarative, managed) agent as an
// azure.ai.agent service entry. Voice agents carry no container/image/code
// config, so this path skips startup-command detection, Docker settings, and
// pre-built image handling entirely. The agent definition is embedded inline
// using the voice-specific writer; sibling Foundry resource services (project)
// are still emitted so provision wires the endpoint.
func (a *InitAction) addVoiceAgentToProject(
	ctx context.Context, targetDir string, voiceDef *agent_yaml.VoiceAgent,
) error {
	if targetDir == "." {
		if cwd, err := os.Getwd(); err == nil && a.projectConfig != nil && a.projectConfig.Path != "" {
			if relPath, err := filepath.Rel(a.projectConfig.Path, cwd); err == nil && relPath != "." {
				targetDir = filepath.ToSlash(relPath)
			}
		}
	}

	if voiceDef == nil {
		return fmt.Errorf("voice agent definition is required")
	}
	if voiceDef.ModelType == agent_yaml.VoiceModelTypeHostedAgent ||
		(voiceDef.ConversationEngine != nil && strings.EqualFold(
			strings.TrimSpace(voiceDef.ConversationEngine.Type), "hosted_agent")) {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"hosted voice wrappers cannot be generated by the standalone voice init flow",
			"use a unified azure.yaml that declares both the hosted target and the voice wrapper",
		)
	}

	agentConfig := project.ServiceTargetAgentConfig{}
	agentProps, err := project.VoiceAgentDefinitionToServiceProperties(*voiceDef, &agentConfig)
	if err != nil {
		return err
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:                 a.serviceNameOverride,
		RelativePath:         targetDir,
		Host:                 AiAgentHost,
		AdditionalProperties: agentProps,
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}
	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding voice agent service to project: %w", err)
	}

	if err := recordFoundryProjectEnv(
		ctx, a.azdClient, a.environment.Name, a.selectedFoundryProject,
	); err != nil {
		return err
	}
	if err := authorSelectedFoundryProject(
		ctx,
		a.azdClient,
		a.environment.Name,
		a.selectedFoundryProject,
		a.projectConfig.GetPath(),
		func() projectAuthoringMode {
			if a.selectedFoundryProject != nil {
				return projectAuthoringExisting
			}
			if a.credential != nil {
				return projectAuthoringNew
			}
			return projectAuthoringCurrent
		}(),
		a.flags.noPrompt,
	); err != nil {
		return err
	}
	if _, err := emitResourceServices(
		ctx, a.azdClient, a.serviceNameOverride,
		foundryResources{},
	); err != nil {
		return err
	}

	fmt.Print(voiceAgentAddedMessage(a.serviceNameOverride))

	var stateOpts []nextstep.Option
	if a.createdFolderDisplay != "" {
		stateOpts = append(stateOpts, nextstep.WithCreatedFolder(a.createdFolderDisplay))
	}
	state, _ := nextstep.AssembleState(ctx, a.azdClient, stateOpts...)
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, a.azdClient)))
	return nil
}

func voiceAgentAddedMessage(serviceName string) string {
	return fmt.Sprintf(
		"\nAdded your voice agent as a service entry named '%s' under the file azure.yaml.\n",
		serviceName,
	)
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
	return a.resolveCollisionsInternal(ctx, agentId, targetDir, serviceName, true)
}

func (a *InitAction) resolveServiceNameCollision(
	ctx context.Context,
	agentId string,
	serviceName string,
) (string, error) {
	_, resolved, err := a.resolveCollisionsInternal(ctx, agentId, "", serviceName, false)
	return resolved, err
}

func (a *InitAction) resolveCollisionsInternal(
	ctx context.Context,
	agentId string,
	targetDir string,
	serviceName string,
	checkDirectory bool,
) (string, string, error) {
	dirExists := checkDirectory && fileExists(targetDir)

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
		a.nextAvailableNameInDir(agentId, filepath.Dir(targetDir), checkDirectory)
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
	return a.nextAvailableNameInDir(agentId, "src", true)
}

func (a *InitAction) nextAvailableNameInDir(
	agentId string,
	parentDir string,
	checkDirectory bool,
) (string, string, string, error) {
	const maxAttempts = 100
	for i := 2; i <= maxAttempts; i++ {
		candidate := fmt.Sprintf("%s-%d", agentId, i)
		candidateDir := filepath.Join(parentDir, candidate)
		candidateSvc := strings.ReplaceAll(candidate, " ", "")

		if checkDirectory && fileExists(candidateDir) {
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

			if err := writeDownloadedFile(itemLocalPath, []byte(fileContent)); err != nil {
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

			if err := writeDownloadedFile(itemLocalPath, fileContent); err != nil {
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

func writeDownloadedFile(path string, content []byte) error {
	permissions := downloadedFilePermissions(path)

	//nolint:gosec // downloaded project files intentionally use project-friendly permissions
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, permissions)
	if err != nil {
		return err
	}

	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}

	return file.Close()
}

func downloadedFilePermissions(path string) os.FileMode {
	if strings.EqualFold(filepath.Ext(path), ".sh") {
		return osutil.PermissionExecutableFile
	}
	return osutil.PermissionFile
}

func verifyRegistryConnectionOnProject(
	ctx context.Context,
	credential azcore.TokenCredential,
	foundryProject FoundryProjectInfo,
	connectionRef string,
) error {
	if err := verifyFoundryProjectConnection(
		ctx,
		credential,
		foundryProject,
		connectionRef,
		listFoundryProjectConnections,
	); err != nil {
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("failed to verify registry connection %q: %s", connectionRef, err),
			"Create the connection on the selected Foundry project or pass the name or ID of an existing connection",
		)
	}
	return nil
}

// validateCodeDeployFlags checks that required flags are present when using
// --deploy-mode code in --no-prompt mode.

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
	if !containerref.IsFullyQualified(image) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid image URL %q: must be in format registry/image[:tag]", image),
			"Provide a fully qualified image URL like 'myacr.azurecr.io/agent:v1'",
		)
	}

	return nil
}

// validateRegistryConnectionFlag validates combinations that can be resolved
// before a manifest is loaded. Manifest-backed image validation is deferred
// until the effective hosted-agent definition is available.
func validateRegistryConnectionFlag(
	connectionRef string,
	image string,
	hasAzureYamlInput bool,
	deployMode string,
	kind string,
) error {
	if connectionRef == "" {
		return nil
	}
	if strings.TrimSpace(connectionRef) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--registry-connection cannot be empty",
			"Pass the name or ID of an existing Foundry project connection",
		)
	}
	if deployMode == "code" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--registry-connection cannot be used with --deploy-mode code",
			"Use --registry-connection with a pre-built image or remove the option",
		)
	}
	if kind != "" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--registry-connection is only valid for hosted container agents",
			"Remove --kind or omit --registry-connection",
		)
	}
	if image == "" && !hasAzureYamlInput {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--registry-connection requires --image when no unified azure.yaml input is provided",
			"Pass --image <registry/image:tag> or provide an image on the hosted agent service in azure.yaml",
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
