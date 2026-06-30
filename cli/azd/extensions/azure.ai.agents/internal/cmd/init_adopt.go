// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
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
	// Adoption is a fresh-project operation: it lays down the project-root
	// azure.yaml. When a project already exists we cannot adopt over it;
	// merging the sample's services into an existing azure.yaml is tracked
	// separately (#8884).
	if fileExists("azure.yaml") {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"a project azure.yaml already exists in this directory, so the sample's "+
				"unified azure.yaml cannot be adopted here",
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

	targetDir, folderDisplay := adoptTargetDir(flags, foundryProjectName(content))

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
		if base == "azure.yaml" || base == "azure.yml" {
			return dir, noop, nil
		}

		// The pointer file isn't named azure.yaml: stage a temp copy of the
		// directory and write the manifest as azure.yaml so azd-core adopts it.
		staging, err := os.MkdirTemp("", "azd-foundry-adopt-*")
		if err != nil {
			return "", noop, fmt.Errorf("creating staging dir: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(staging) }
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

	if urlInfo := parseGitHubUrlNaive(pointer); urlInfo != nil {
		dirPath := parentDirOf(urlInfo.FilePath)
		err := downloadDirectoryContentsWithoutGhCli(
			ctx, urlInfo.RepoSlug, dirPath, dirPath, urlInfo.Branch, staging, httpClient,
		)
		if err == nil && stagedAzureYamlExists(staging) {
			return nil
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

	if !stagedAzureYamlExists(staging) {
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
