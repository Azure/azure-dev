// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

// foundryServiceHosts are the azure.yaml service `host` values that identify a
// unified Microsoft Foundry project manifest. The legacy `microsoft.foundry`
// host is included for backward compatibility with older non-split files.
var foundryServiceHosts = map[string]struct{}{
	"azure.ai.agent":      {},
	"azure.ai.project":    {},
	"azure.ai.connection": {},
	"azure.ai.toolbox":    {},
	"microsoft.foundry":   {},
}

// looksLikeFoundryAzureYaml reports whether the given YAML content is a unified
// Foundry `azure.yaml` project manifest rather than an agent manifest.
//
// It returns true when the document has a top-level `services:` map in which at
// least one service declares a Foundry `host:`. Agent manifests have a top-level
// `template:` and no `services:`, so they never match. This lets `azd ai agent
// init -m <pointer>` route a unified `azure.yaml` to the adoption path and an
// agent manifest to the legacy generate path unambiguously.
func looksLikeFoundryAzureYaml(content []byte) bool {
	var top map[string]any
	if err := yaml.Unmarshal(content, &top); err != nil {
		return false
	}

	services, ok := top["services"].(map[string]any)
	if !ok {
		return false
	}

	for _, svc := range services {
		svcMap, ok := svc.(map[string]any)
		if !ok {
			continue
		}
		host, ok := svcMap["host"].(string)
		if !ok {
			continue
		}
		if _, isFoundry := foundryServiceHosts[host]; isFoundry {
			return true
		}
	}

	return false
}

// foundryProjectName returns the top-level `name:` of a unified azure.yaml, used
// to derive the project folder name. Returns "" when the name is absent or the
// content cannot be parsed.
func foundryProjectName(content []byte) string {
	var top map[string]any
	if err := yaml.Unmarshal(content, &top); err != nil {
		return ""
	}
	if name, ok := top["name"].(string); ok {
		return strings.TrimSpace(name)
	}
	return ""
}

// readManifestContentForInitDetection returns the pointed-at YAML content for
// init-mode routing. It first uses the cheap peek path; when that cannot read a
// GitHub URL (for example, a private repository), it falls back to the
// authenticated GitHub CLI download path so private unified azure.yaml samples
// can still be classified and adopted.
func readManifestContentForInitDetection(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	manifestPointer string,
	httpClient *http.Client,
) ([]byte, bool) {
	if content, ok := readManifestContentForPeek(ctx, manifestPointer, httpClient); ok {
		return content, true
	}
	if azdClient == nil || !strings.Contains(manifestPointer, "://") {
		return nil, false
	}

	parsedURL, err := url.Parse(manifestPointer)
	if err != nil || !strings.Contains(parsedURL.Hostname(), "github") {
		return nil, false
	}

	commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	console := input.NewConsole(
		false, // noPrompt
		true,  // isTerminal
		input.Writers{Output: io.Discard},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil, // formatter
		nil, // externalPromptCfg
	)
	ghCli := github.NewGitHubCli(console, commandRunner)
	if err := ghCli.EnsureInstalled(ctx); err != nil {
		log.Printf("detect unified azure.yaml: ensuring gh is installed: %v", err)
		return nil, false
	}

	urlInfo, err := parseGitHubUrlForAdopt(ctx, azdClient, manifestPointer)
	if err != nil {
		log.Printf("detect unified azure.yaml: parsing GitHub URL: %v", err)
		return nil, false
	}

	apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
	if urlInfo.Branch != "" {
		apiPath += fmt.Sprintf("?ref=%s", urlInfo.Branch)
	}
	content, err := downloadGithubManifest(ctx, urlInfo, apiPath, ghCli)
	if err != nil {
		log.Printf("detect unified azure.yaml: downloading GitHub file: %v", err)
		return nil, false
	}

	return []byte(content), true
}

// runInitFromAzureYaml adopts a sample's unified Foundry `azure.yaml` as the
// project-root manifest instead of generating one from an agent manifest
// (#8798). The sample's `azure.yaml` and the files it references are placed at
// the project root via azd-core's native template adoption; the services it
// already declares (project, connections, toolboxes, agents) are not
// re-derived. `content` is the already-fetched azure.yaml used to derive the
// project folder name.
func runInitFromAzureYaml(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	content []byte,
) error {
	targetDir, folderDisplay := adoptTargetDir(flags, foundryProjectName(content))

	// Adoption is a fresh-project operation: it lays down the project-root
	// azure.yaml. When the target already contains an azd project manifest we
	// cannot adopt over it; merging the sample's services into an existing
	// azure.yaml is tracked separately (#8884).
	if projectManifestExists(targetDir) {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			fmt.Sprintf("a project azure.yaml already exists in %q, so the sample's "+
				"unified azure.yaml cannot be adopted there", targetDir),
			"run this command in an empty directory (or pass a new target directory) to "+
				"adopt the sample, or add an individual agent to this project with "+
				"'azd ai agent init -m <agent.manifest.yaml>'",
		)
	}

	// Stage the sample as a local template directory (azure.yaml at its root
	// alongside referenced files) that azd-core can adopt with `azd init -t`.
	stagingDir, cleanup, err := stageAzureYamlTemplate(ctx, flags, azdClient, httpClient)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Println(output.WithGrayFormat("Adopting the sample's azure.yaml as your project manifest..."))

	envName := deriveEnvName(flags, targetDir)
	if err := scaffoldProject(ctx, azdClient, targetDir, stagingDir, envName); err != nil {
		return err
	}

	// Defensive: the sample should already declare `infra.provider:
	// microsoft.foundry`, but stamp it if missing so provisioning stays
	// bicep-less by default.
	if err := ensureFoundryProviderDeclared(ctx, azdClient); err != nil {
		return err
	}

	// Apply deploy-mode configuration to the adopted agent service (#8923).
	// When the user passes --deploy-mode (and optionally --runtime /
	// --entry-point), or when the service doesn't already specify its deploy
	// mode, resolve code vs container configuration and update the service.
	if err := applyDeployModeToAdoptedProject(ctx, flags, azdClient); err != nil {
		return err
	}

	fmt.Printf(
		"\nAdopted the sample's azure.yaml as the project manifest at %s.\n",
		output.WithHighLightFormat("azure.yaml"),
	)

	printAdoptionNextSteps(ctx, azdClient, folderDisplay)
	return nil
}

// adoptTargetDir resolves the directory the adopted project is created in and
// the display path for the "created folder" next-step hint. An explicit --src
// (or positional directory) wins; otherwise a new folder named after the
// sample's project name is used, falling back to the current directory when the
// sample has no name.
func adoptTargetDir(flags *initFlags, projectName string) (targetDir string, folderDisplay string) {
	if flags.src != "" {
		return flags.src, folderDisplayIfNew(flags.src)
	}
	if projectName == "" {
		return ".", ""
	}
	folder := sanitizeAgentName(projectName)
	if folder == "" {
		return ".", ""
	}
	return folder, folderDisplayIfNew(folder)
}

// folderDisplayIfNew returns a slash-formatted display path when dir does not
// yet exist (so the cd hint is only shown for newly-created folders), else "".
func folderDisplayIfNew(dir string) string {
	if dir == "." {
		return ""
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return filepath.ToSlash(dir)
	}
	return ""
}

func projectManifestExists(dir string) bool {
	return fileExists(filepath.Join(dir, "azure.yaml")) ||
		fileExists(filepath.Join(dir, "azure.yml"))
}

// stageAzureYamlTemplate produces a local directory that azd-core can adopt as a
// template (`azd init -t <dir>`): it contains the sample's azure.yaml at its
// root alongside the sibling files/dirs the manifest references.
//
// For a local pointer the pointer's parent directory is used directly when the
// file is already named azure.yaml(.yml); otherwise a temp copy of the
// directory is staged with the manifest written as azure.yaml. For a remote
// GitHub pointer the azure.yaml's containing directory is downloaded into a temp
// staging dir. The returned cleanup removes any temp directory created.
func stageAzureYamlTemplate(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
) (string, func(), error) {
	noop := func() {}
	pointer := flags.manifestPointer

	if isLocalFilePath(pointer) {
		dir := filepath.Dir(pointer)
		base := strings.ToLower(filepath.Base(pointer))
		if base == "azure.yaml" {
			return dir, noop, nil
		}

		// The pointer file isn't named azure.yaml: stage a temp copy of the
		// directory and write the manifest as azure.yaml so azd-core adopts it.
		staging, err := os.MkdirTemp("", "azd-foundry-adopt-*")
		if err != nil {
			return "", noop, fmt.Errorf("creating staging dir: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(staging) }
		// Staging is all-or-nothing: without azure.yaml at the template root,
		// azd-core would generate a default manifest instead of adopting this
		// sample, so every error path removes the partial copy.
		if err := copyDirectory(dir, staging); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("staging sample directory: %w", err)
		}
		//nolint:gosec // manifest path is an explicit user-provided local path
		data, err := os.ReadFile(pointer)
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("reading sample azure.yaml: %w", err)
		}
		//nolint:gosec // staging dir is from os.MkdirTemp and the filename is a constant
		if err := os.WriteFile(filepath.Join(staging, "azure.yaml"), data, osutil.PermissionFile); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("writing staged azure.yaml: %w", err)
		}
		if err := os.Remove(filepath.Join(staging, filepath.Base(pointer))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanup()
			return "", noop, fmt.Errorf("removing staged source manifest: %w", err)
		}
		return staging, cleanup, nil
	}

	// Remote GitHub pointer: download the directory containing the azure.yaml.
	staging, err := os.MkdirTemp("", "azd-foundry-adopt-*")
	if err != nil {
		return "", noop, fmt.Errorf("creating staging dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := stageRemoteAzureYaml(ctx, azdClient, httpClient, pointer, staging); err != nil {
		cleanup()
		return "", noop, err
	}
	return staging, cleanup, nil
}

// ensureStagedAzureYaml normalizes a staged template so azd-core sees
// azure.yaml at the template root. azd-core only adopts azure.yaml; if a sample
// ships azure.yml, copy it to azure.yaml and remove the alias to avoid leaving
// duplicate project manifests in the initialized project.
func ensureStagedAzureYaml(staging string) (bool, error) {
	azureYaml := filepath.Join(staging, "azure.yaml")
	if fileExists(azureYaml) {
		return true, nil
	}

	azureYml := filepath.Join(staging, "azure.yml")
	if !fileExists(azureYml) {
		return false, nil
	}

	//nolint:gosec // azure.yml is in a temp staging dir produced by this command
	data, err := os.ReadFile(azureYml)
	if err != nil {
		return false, fmt.Errorf("reading staged azure.yml: %w", err)
	}
	//nolint:gosec // staging dir is from os.MkdirTemp and the filename is a constant
	if err := os.WriteFile(azureYaml, data, osutil.PermissionFile); err != nil {
		return false, fmt.Errorf("writing staged azure.yaml: %w", err)
	}
	if err := os.Remove(azureYml); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("removing staged azure.yml: %w", err)
	}
	return true, nil
}

func clearStagingDirectory(staging string) error {
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clearing staging directory: %w", err)
	}
	if err := os.MkdirAll(staging, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("recreating staging directory: %w", err)
	}
	return nil
}

// stageRemoteAzureYaml downloads the directory containing the remote azure.yaml
// into staging. It first tries an unauthenticated public download (no gh CLI),
// then falls back to the GitHub CLI for private repositories or URL forms the
// naive parser can't handle — mirroring downloadAgentYaml's resolution order.
func stageRemoteAzureYaml(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	pointer string,
	staging string,
) error {
	fmt.Println(output.WithGrayFormat("Downloading sample from GitHub..."))

	triedPublicDownload := false
	if urlInfo := parseGitHubUrlNaive(pointer); urlInfo != nil {
		triedPublicDownload = true
		dirPath := parentDirOf(urlInfo.FilePath)
		err := downloadDirectoryContentsWithoutGhCli(
			ctx, urlInfo.RepoSlug, dirPath, dirPath, urlInfo.Branch, staging, httpClient,
		)
		if err == nil {
			hasAzureYaml, normalizeErr := ensureStagedAzureYaml(staging)
			if normalizeErr != nil {
				return normalizeErr
			}
			if hasAzureYaml {
				return nil
			}
		}
	}

	if triedPublicDownload {
		if err := clearStagingDirectory(staging); err != nil {
			return err
		}
	}

	// Fall back to the GitHub CLI (handles private repos and complex URLs).
	commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
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
	ghCli := github.NewGitHubCli(console, commandRunner)
	if err := ghCli.EnsureInstalled(ctx); err != nil {
		return exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("ensuring gh is installed: %s", err),
			"install the GitHub CLI (gh) from https://cli.github.com",
		)
	}

	urlInfo, err := parseGitHubUrlForAdopt(ctx, azdClient, pointer)
	if err != nil {
		return err
	}
	dirPath := parentDirOf(urlInfo.FilePath)
	if err := downloadDirectoryContents(
		ctx, urlInfo.Hostname, urlInfo.RepoSlug, dirPath, dirPath, urlInfo.Branch, staging, ghCli, console,
	); err != nil {
		return exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("downloading sample directory: %s", err),
			"verify the URL points to a valid azure.yaml in the repository and you have access",
		)
	}

	hasAzureYaml, err := ensureStagedAzureYaml(staging)
	if err != nil {
		return err
	}
	if !hasAzureYaml {
		return exterrors.Validation(
			exterrors.CodeInvalidManifestPointer,
			"no azure.yaml was found in the downloaded sample directory",
			"verify the URL points to a directory that contains an azure.yaml",
		)
	}
	return nil
}

// parseGitHubUrlForAdopt resolves GitHub repository info for a pointer using the
// azd host (no InitAction required), mirroring (*InitAction).parseGitHubUrl.
func parseGitHubUrlForAdopt(
	ctx context.Context, azdClient *azdext.AzdClient, pointer string,
) (*GitHubUrlInfo, error) {
	urlInfo, err := azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
		Url: pointer,
	})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("parsing GitHub URL: %s", err),
			"verify the URL points to a file in a GitHub repository",
		)
	}
	return &GitHubUrlInfo{
		RepoSlug: urlInfo.RepoSlug,
		Branch:   urlInfo.Branch,
		FilePath: urlInfo.FilePath,
		Hostname: urlInfo.Hostname,
	}, nil
}

// parentDirOf returns the directory portion of a repo-relative file path, or ""
// when the file lives at the repository root (so the download lists the root).
func parentDirOf(filePath string) string {
	parts := strings.Split(filePath, "/")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// stagedAzureYamlExists reports whether the staging directory contains an
// adopted azure.yaml (or azure.yml) at its root.
func stagedAzureYamlExists(staging string) bool {
	return fileExists(filepath.Join(staging, "azure.yaml")) ||
		fileExists(filepath.Join(staging, "azure.yml"))
}

// ensureFoundryProviderDeclared stamps `infra.provider: microsoft.foundry` onto
// the adopted azure.yaml when the sample didn't already declare it, keeping
// provisioning bicep-less by default.
func ensureFoundryProviderDeclared(ctx context.Context, azdClient *azdext.AzdClient) error {
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			fmt.Sprintf("failed to get project after adoption: %s", err),
			"",
		)
	}
	if hasFoundryProviderDeclared(resp.Project) {
		return nil
	}
	return writeFoundryProvider(ctx, azdClient)
}

// printAdoptionNextSteps emits context-aware next-step guidance after adoption,
// reusing the shared nextstep resolver. State-assembly errors are intentionally
// ignored: the resolver degrades gracefully on partial state.
func printAdoptionNextSteps(ctx context.Context, azdClient *azdext.AzdClient, folderDisplay string) {
	var stateOpts []nextstep.Option
	if folderDisplay != "" {
		stateOpts = append(stateOpts, nextstep.WithCreatedFolder(folderDisplay))
	}
	state, _ := nextstep.AssembleState(ctx, azdClient, stateOpts...)
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, azdClient)))
}

// applyDeployModeToAdoptedProject locates the azure.ai.agent service in the
// adopted project and applies deploy-mode configuration (code or container)
// based on the --deploy-mode, --runtime, and --entry-point flags. When no
// explicit flag is passed and the service already has a codeConfiguration or
// docker property, the service is left unchanged (the sample is pre-configured).
func applyDeployModeToAdoptedProject(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
) error {
	// Validate --image flag early (incompatible with --deploy-mode code).
	if err := validateImageFlag(flags.image, flags.deployMode); err != nil {
		return err
	}

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("reading adopted project: %w", err)
	}

	// Collect all agent services in the adopted project.
	type agentEntry struct {
		name string
		svc  *azdext.ServiceConfig
	}
	var agentServices []agentEntry
	for name, svc := range resp.GetProject().GetServices() {
		if svc.GetHost() == AiAgentHost {
			agentServices = append(agentServices, agentEntry{name: name, svc: svc})
		}
	}
	if len(agentServices) == 0 {
		// No agent service found -- nothing to configure.
		return nil
	}

	// Apply configuration to each agent service.
	for _, agent := range agentServices {
		if err := applyDeployModeToService(ctx, flags, azdClient, agent.name, agent.svc); err != nil {
			return err
		}
	}
	return nil
}

// applyDeployModeToService applies deploy-mode configuration to a single agent service.
func applyDeployModeToService(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	serviceName string,
	svc *azdext.ServiceConfig,
) error {
	// Apply --image override to the agent service when provided.
	if flags.image != "" {
		imageValue, err := structpb.NewValue(flags.image)
		if err != nil {
			return fmt.Errorf("encoding image value: %w", err)
		}
		if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "image",
			Value:       imageValue,
		}); err != nil {
			return fmt.Errorf("writing image to agent service %q: %w", serviceName, err)
		}
		log.Printf("Applied --image %q to agent service %q", flags.image, serviceName)

		// --image implies container deploy; apply container config and return.
		return applyContainerDeployToService(ctx, azdClient, serviceName)
	}

	// Check whether the service already specifies its deploy mode.
	hasCodeConfig := adoptedServiceHasCodeConfig(svc)
	hasDocker := adoptedServiceHasDocker(svc)

	// When no explicit --deploy-mode flag is passed and the service is
	// already configured, respect the sample's existing configuration.
	if flags.deployMode == "" && (hasCodeConfig || hasDocker) {
		return nil
	}

	// Use the service's subdirectory for language detection (not project root).
	targetDir := svc.GetRelativePath()
	if targetDir == "" {
		targetDir = "."
	}
	showCodeDeploy := isPythonProject(targetDir) || isDotnetProject(targetDir)
	// userProvidedManifest is true: -m was explicitly provided.
	deployMode, err := promptDeployMode(ctx, azdClient, flags.noPrompt, showCodeDeploy, flags.deployMode, true)
	if err != nil {
		return fmt.Errorf("resolving deploy mode for adopted project: %w", err)
	}

	if deployMode == "code" {
		return applyCodeDeployToService(ctx, flags, azdClient, serviceName, targetDir)
	}
	return applyContainerDeployToService(ctx, azdClient, serviceName)
}

// adoptedServiceHasCodeConfig checks whether the adopted agent service already
// declares a codeConfiguration in its properties.
func adoptedServiceHasCodeConfig(svc *azdext.ServiceConfig) bool {
	props := svc.GetAdditionalProperties()
	if props == nil {
		return false
	}
	fields := props.GetFields()
	if fields == nil {
		return false
	}
	v, ok := fields["codeConfiguration"]
	if !ok {
		return false
	}
	// A null value doesn't count as having a codeConfiguration.
	return v != nil && v.GetStructValue() != nil
}

// adoptedServiceHasDocker checks whether the adopted agent service already
// declares a docker configuration in its properties. We check
// additionalProperties rather than svc.GetDocker() because the gRPC mapper
// always returns a non-nil Docker pointer (even for the zero-value struct).
func adoptedServiceHasDocker(svc *azdext.ServiceConfig) bool {
	props := svc.GetAdditionalProperties()
	if props == nil {
		return false
	}
	fields := props.GetFields()
	if fields == nil {
		return false
	}
	v, ok := fields["docker"]
	if !ok {
		return false
	}
	// A null value doesn't count as having docker configured.
	return v != nil && v.GetStructValue() != nil
}

// applyCodeDeployToService writes codeConfiguration onto the adopted agent
// service and updates the service language from "docker" to the appropriate
// language for the selected runtime.
func applyCodeDeployToService(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	serviceName string,
	targetDir string,
) error {
	codeConfig, err := promptCodeConfig(ctx, azdClient, targetDir, flags.noPrompt, codeDeployOptions{
		runtime:       flags.runtime,
		entryPoint:    flags.entryPoint,
		depResolution: flags.depResolution,
	}, true) // userProvidedManifest=true since -m was provided
	if err != nil {
		return fmt.Errorf("resolving code configuration for adopted project: %w", err)
	}

	// Write codeConfiguration onto the service (camelCase keys match the
	// azure.yaml inline format read by the deploy path via JSON unmarshal).
	codeConfigMap := map[string]any{
		"runtime":    codeConfig.Runtime,
		"entryPoint": codeConfig.EntryPoint,
	}
	if codeConfig.DependencyResolution != nil {
		codeConfigMap["dependencyResolution"] = *codeConfig.DependencyResolution
	}

	codeConfigValue, err := structpb.NewValue(codeConfigMap)
	if err != nil {
		return fmt.Errorf("encoding codeConfiguration: %w", err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "codeConfiguration",
		Value:       codeConfigValue,
	}); err != nil {
		return fmt.Errorf("writing codeConfiguration to agent service: %w", err)
	}

	// Update the service language to match the runtime.
	language := "python"
	if strings.HasPrefix(codeConfig.Runtime, "dotnet_") {
		language = "csharp"
	}
	langValue, err := structpb.NewValue(language)
	if err != nil {
		return fmt.Errorf("encoding language value: %w", err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "language",
		Value:       langValue,
	}); err != nil {
		return fmt.Errorf("updating service language to %s: %w", language, err)
	}

	// Remove docker property if it was previously set (switching from container to code).
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "docker",
		Value:       structpb.NewNullValue(),
	}); err != nil {
		log.Printf("warning: could not clear docker property on service %q: %v", serviceName, err)
	}

	log.Printf("Applied code deploy configuration (runtime=%s, entryPoint=%s) to service %q",
		codeConfig.Runtime, codeConfig.EntryPoint, serviceName)
	return nil
}

// applyContainerDeployToService sets the docker property on the adopted agent
// service and ensures the language is "docker". Removes any codeConfiguration
// if present.
func applyContainerDeployToService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
) error {
	// Set docker property with remote build enabled.
	dockerMap := map[string]any{"remoteBuild": true}
	dockerValue, err := structpb.NewValue(dockerMap)
	if err != nil {
		return fmt.Errorf("encoding docker configuration: %w", err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "docker",
		Value:       dockerValue,
	}); err != nil {
		return fmt.Errorf("writing docker property to agent service: %w", err)
	}

	// Set language to docker.
	langValue, err := structpb.NewValue("docker")
	if err != nil {
		return fmt.Errorf("encoding language value: %w", err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "language",
		Value:       langValue,
	}); err != nil {
		return fmt.Errorf("updating service language to docker: %w", err)
	}

	// Remove codeConfiguration if present (switching from code to container).
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "codeConfiguration",
		Value:       structpb.NewNullValue(),
	}); err != nil {
		log.Printf("warning: could not clear codeConfiguration on service %q: %v", serviceName, err)
	}

	log.Printf("Applied container deploy configuration to service %q", serviceName)
	return nil
}
