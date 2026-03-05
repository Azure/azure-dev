// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	posixpath "path"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/registry_api"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type InitFromTemplateAction struct {
	azdClient         *azdext.AzdClient
	flags             *initFlags
	projectConfig     *azdext.ProjectConfig
	azureContext      *azdext.AzureContext
	environment       *azdext.Environment
	credential        azcore.TokenCredential
	deploymentDetails []project.Deployment
	httpClient        *http.Client
}

func (a *InitFromTemplateAction) Run(ctx context.Context) error {
	color.Green("Initializing AI agent project from template...")
	fmt.Println()

	a.azureContext = &azdext.AzureContext{
		Scope:     &azdext.AzureScope{},
		Resources: []string{},
	}

	// 1. Resolve the template URL
	repoUrl, branch := resolveTemplateUrl(a.flags.templateUrl)
	repoSlug := extractRepoSlug(repoUrl)

	// 2. Check if project already exists
	projectResponse, projErr := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if projErr != nil {
		// No project exists — scaffold from the template repo directly into cwd.
		// This uses the same scaffoldTemplate approach (GitHub API tree + collision handling)
		// so that the template's azure.yaml, infra/, and agent sources are placed here.
		if repoSlug == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidArgs,
				fmt.Sprintf("could not extract owner/repo from template URL: %s", repoUrl),
				"provide a valid GitHub URL, owner/repo, or repo name",
			)
		}

		if branch == "" {
			branch = "main"
		}

		if err := a.scaffoldFromTemplate(ctx, repoSlug, branch); err != nil {
			if exterrors.IsCancellation(err) {
				return exterrors.Cancelled("project initialization was cancelled")
			}
			return exterrors.Dependency(
				exterrors.CodeScaffoldTemplateFailed,
				fmt.Sprintf("failed to scaffold template: %s", err),
				"",
			)
		}

		projectResponse, projErr = a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if projErr != nil {
			return exterrors.Dependency(
				exterrors.CodeProjectNotFound,
				fmt.Sprintf("failed to get project after template initialization: %s", projErr),
				"",
			)
		}

		fmt.Println()
	} else if a.flags.infra {
		// Project already exists + --infra: copy only infra/ from the template
		fmt.Println("Project already exists. Downloading template to add infrastructure files...")

		templateDir, err := a.downloadTemplate(ctx, repoUrl, branch)
		if err != nil {
			return fmt.Errorf("downloading template: %w", err)
		}
		defer os.RemoveAll(templateDir)

		infraSrc := filepath.Join(templateDir, "infra")
		if _, statErr := os.Stat(infraSrc); os.IsNotExist(statErr) {
			return exterrors.Validation(
				exterrors.CodeInvalidArgs,
				"the template repository does not contain an infra/ directory",
				"choose a template that includes infrastructure-as-code, or remove the --infra flag",
			)
		}

		if err := copyDirectory(infraSrc, "infra"); err != nil {
			return fmt.Errorf("copying infra directory: %w", err)
		}

		color.Green("\nInfrastructure files added from template successfully!")
		fmt.Println("The infra/ directory has been added to your project.")
		fmt.Printf("Next steps: Run %s to provision and deploy.\n", color.HiBlueString("azd up"))
		return nil
	} else {
		// Project already exists — clone template to temp dir and copy agent files
		fmt.Println("Project already exists. Downloading template to add agent...")

		templateDir, err := a.downloadTemplate(ctx, repoUrl, branch)
		if err != nil {
			return fmt.Errorf("downloading template: %w", err)
		}
		defer os.RemoveAll(templateDir)

		// Find agent.yaml and copy its parent directory to the target
		agentYamlPath, err := findAgentYaml(templateDir)
		if err != nil {
			return err
		}

		content, err := os.ReadFile(agentYamlPath)
		if err != nil {
			return fmt.Errorf("reading agent.yaml: %w", err)
		}

		agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(content)
		if err != nil {
			return fmt.Errorf("validating agent.yaml: %w", err)
		}

		// Determine target directory
		agentName := agentManifest.Name
		if agentName == "" {
			agentName = "my-agent"
		}

		targetDir := a.flags.src
		if targetDir == "" {
			targetDir = filepath.Join("src", agentName)
		}

		agentYamlDir := filepath.Dir(agentYamlPath)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("creating target directory: %w", err)
		}
		if err := copyDirectory(agentYamlDir, targetDir); err != nil {
			return fmt.Errorf("copying template files: %w", err)
		}
	}

	if projectResponse.Project == nil {
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"project not found",
			"",
		)
	}
	a.projectConfig = projectResponse.Project

	// 3. Find agent.yaml in the project directory (either scaffolded or existing)
	projectPath := a.projectConfig.Path
	agentYamlPath, err := findAgentYaml(projectPath)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeAgentYamlNotFound,
			"no agent manifest found in the template repository",
			"the template must contain an agent.yaml or agent.manifest.yaml file (searched root, src/*/, and all subdirectories). "+
				"Add an agent manifest to your template, or use 'azd ai agent init' without -t to create one interactively",
		)
	}

	// 4. Read and parse the agent.yaml
	content, err := os.ReadFile(agentYamlPath)
	if err != nil {
		return fmt.Errorf("reading agent.yaml: %w", err)
	}

	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(content)
	if err != nil {
		return fmt.Errorf("validating agent.yaml: %w", err)
	}

	fmt.Println("✓ Found and validated agent.yaml from template")

	// 5. Prompt for agent name (default from manifest)
	defaultName := agentManifest.Name
	if defaultName == "" {
		defaultName = "my-agent"
	}

	promptResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter a name for your agent:",
			DefaultValue: defaultName,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("agent name prompt was cancelled")
		}
		return fmt.Errorf("failed to prompt for agent name: %w", err)
	}
	agentName := sanitizeAgentName(promptResp.Value)
	agentManifest.Name = agentName

	// 6. Create azd environment (deferred - like init-with-code)
	if a.environment == nil {
		if err := a.createEnvironment(ctx, agentName+"-dev"); err != nil {
			return fmt.Errorf("failed to create azd environment: %w", err)
		}
	}

	// 7. Determine target directory for the agent source
	targetDir := a.flags.src
	if targetDir == "" {
		relPath, relErr := filepath.Rel(projectPath, filepath.Dir(agentYamlPath))
		if relErr == nil && relPath != "." {
			targetDir = relPath
		} else {
			targetDir = filepath.Join("src", agentName)
		}
	}

	// If src path is absolute, convert it to relative path compared to the azd project path
	if filepath.IsAbs(targetDir) {
		relPath, relErr := filepath.Rel(projectPath, targetDir)
		if relErr != nil {
			return fmt.Errorf("failed to convert src path to relative path: %w", relErr)
		}
		targetDir = relPath
	}

	// 8. Process manifest parameters (use defaults where available)
	agentManifest, err = registry_api.ProcessManifestParameters(ctx, agentManifest, a.azdClient, a.flags.NoPrompt)
	if err != nil {
		return fmt.Errorf("failed to process manifest parameters: %w", err)
	}

	// 9. Process models using the init-with-code flow (Deploy new / Use existing / Skip)
	agentManifest, err = a.processModelsInteractive(ctx, agentManifest)
	if err != nil {
		return fmt.Errorf("failed to process models: %w", err)
	}

	// 10. Write the processed agent.yaml back
	templateContent, err := yaml.Marshal(agentManifest.Template)
	if err != nil {
		return fmt.Errorf("marshaling agent manifest to YAML: %w", err)
	}

	annotation := "# yaml-language-server: $schema=https://raw.githubusercontent.com/microsoft/AgentSchema/refs/heads/main/schemas/v1.0/ContainerAgent.yaml"
	agentFilePath := filepath.Join(projectPath, targetDir, "agent.yaml")
	agentFileContent := annotation + "\n\n" + string(templateContent)
	if err := os.WriteFile(agentFilePath, []byte(agentFileContent), 0644); err != nil {
		return fmt.Errorf("writing agent.yaml: %w", err)
	}

	// 11. Add to project with smart defaults
	if err := a.addToProject(ctx, targetDir, agentManifest); err != nil {
		return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
	}

	color.Green("\nAI agent initialized from template successfully!")
	fmt.Printf("Next steps:\n")
	if len(a.deploymentDetails) > 0 {
		fmt.Printf("  Run %s to provision infrastructure and deploy the model.\n", color.HiBlueString("azd provision"))
	}
	fmt.Printf("  Run %s to run your agent locally.\n", color.HiBlueString("azd ai agent run"))
	fmt.Printf("  Run %s to deploy your agent to Microsoft Foundry.\n", color.HiBlueString("azd ai agent deploy"))

	return nil
}

// scaffoldFromTemplate downloads a GitHub template repo into the current directory,
// checking for file collisions before writing. Unlike scaffoldTemplate in init_from_code.go,
// this downloads ALL files from the repo (not just infra/ and azure.yaml).
func (a *InitFromTemplateAction) scaffoldFromTemplate(ctx context.Context, repoSlug string, branch string) error {
	ghToken := gitHubToken()

	apiUrl := fmt.Sprintf("https://api.github.com/repos/%s/git/trees/%s?recursive=1", repoSlug, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
	if err != nil {
		return fmt.Errorf("creating tree request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	setGitHubAuthHeader(req, ghToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching repo tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf(
				"fetching repo tree: status %d (GitHub API rate limit may have been exceeded; "+
					"set GITHUB_TOKEN or GH_TOKEN environment variable to increase the limit)",
				resp.StatusCode,
			)
		}
		return fmt.Errorf("fetching repo tree: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading tree response: %w", err)
	}

	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &treeResp); err != nil {
		return fmt.Errorf("parsing tree response: %w", err)
	}

	// Collect all blob files from the repo
	var files []templateFileInfo
	for _, entry := range treeResp.Tree {
		if entry.Type != "blob" {
			continue
		}
		cleanPath := posixpath.Clean(entry.Path)
		if posixpath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
			return fmt.Errorf("invalid path in repository tree: %s", entry.Path)
		}
		downloadURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repoSlug, branch, cleanPath)
		collides := false
		if _, statErr := os.Stat(filepath.FromSlash(cleanPath)); statErr == nil {
			collides = true
		}
		files = append(files, templateFileInfo{
			Path:     cleanPath,
			URL:      downloadURL,
			Collides: collides,
		})
	}

	if len(files) == 0 {
		return fmt.Errorf("template repository %s has no files", repoSlug)
	}

	// Sort for consistent display
	slices.SortFunc(files, func(a, b templateFileInfo) int {
		return strings.Compare(a.Path, b.Path)
	})

	// Classify into new and colliding
	var newFiles, collidingFiles []templateFileInfo
	for _, f := range files {
		if f.Collides {
			collidingFiles = append(collidingFiles, f)
		} else {
			newFiles = append(newFiles, f)
		}
	}

	// Display the file list, collapsing infra/ files into a single summary line
	fmt.Print("\nThe following files will be created from the template:\n\n")
	infraCount := 0
	infraCollides := false
	for _, f := range files {
		if strings.HasPrefix(f.Path, "infra/") {
			infraCount++
			if f.Collides {
				infraCollides = true
			}
			continue
		}
		if f.Collides {
			fmt.Printf("  %s  %s\n", color.YellowString("!"), color.YellowString(f.Path))
		} else {
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString(f.Path))
		}
	}
	if infraCount > 0 {
		summary := fmt.Sprintf("infra/ (+%d files)", infraCount)
		if infraCollides {
			fmt.Printf("  %s  %s\n", color.YellowString("!"), color.YellowString(summary))
		} else {
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString(summary))
		}
	}
	fmt.Println()

	// Handle collisions
	overwriteCollisions := false
	if len(collidingFiles) > 0 {
		fmt.Printf("%s %d file(s) already exist and would be overwritten.\n\n",
			color.YellowString("Warning:"), len(collidingFiles))

		conflictChoices := []*azdext.SelectChoice{
			{Label: "Overwrite existing files", Value: "overwrite"},
			{Label: "Skip existing files (keep my versions)", Value: "skip"},
			{Label: "Cancel", Value: "cancel"},
		}

		conflictResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "How would you like to handle existing files?",
				Choices: conflictChoices,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for conflict resolution: %w", err)
		}

		selectedValue := conflictChoices[*conflictResp.Value].Value
		switch selectedValue {
		case "overwrite":
			overwriteCollisions = true
		case "skip":
			overwriteCollisions = false
		case "cancel":
			return fmt.Errorf("operation cancelled, no changes were made")
		}
	} else {
		// No collisions - confirm to proceed
		confirmResp, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "Initialize from the template?",
				DefaultValue: to.Ptr(true),
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for confirmation: %w", err)
		}
		if !*confirmResp.Value {
			return fmt.Errorf("operation cancelled, no changes were made")
		}
	}

	// Download and write files
	filesToWrite := newFiles
	if overwriteCollisions {
		filesToWrite = files
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        fmt.Sprintf("Downloading template (%d files)...", len(filesToWrite)),
		ClearOnStop: true,
	})
	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("starting spinner: %w", err)
	}

	for _, f := range filesToWrite {
		localPath := filepath.FromSlash(f.Path)

		dir := filepath.Dir(localPath)
		if dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				_ = spinner.Stop(ctx)
				return fmt.Errorf("creating directory %s: %w", dir, err)
			}
		}

		fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("creating request for %s: %w", f.Path, err)
		}
		setGitHubAuthHeader(fileReq, ghToken)

		fileResp, err := a.httpClient.Do(fileReq)
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("downloading %s: %w", f.Path, err)
		}
		if fileResp.StatusCode != http.StatusOK {
			fileResp.Body.Close()
			_ = spinner.Stop(ctx)
			return fmt.Errorf("downloading %s: status %d", f.Path, fileResp.StatusCode)
		}

		fileContent, err := io.ReadAll(fileResp.Body)
		fileResp.Body.Close()
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("reading %s: %w", f.Path, err)
		}

		if err := os.WriteFile(localPath, fileContent, 0644); err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("writing %s: %w", localPath, err)
		}
	}

	if err := spinner.Stop(ctx); err != nil {
		return fmt.Errorf("stopping spinner: %w", err)
	}

	skipped := len(files) - len(filesToWrite)
	if skipped > 0 {
		fmt.Printf("  Template initialized: %d file(s) written, %d file(s) skipped.\n", len(filesToWrite), skipped)
	} else {
		fmt.Printf("  Template initialized: %d file(s) written.\n", len(filesToWrite))
	}

	return nil
}

// resolveTemplateUrl resolves a template URL to a full GitHub URL and optional branch.
// Supports:
//   - Full GitHub URL: https://github.com/owner/repo → as-is
//   - Full URL with /tree/branch: https://github.com/owner/repo/tree/main → repo URL + branch
//   - owner/repo → https://github.com/owner/repo
//   - repo → https://github.com/Azure-Samples/repo
func resolveTemplateUrl(templateUrl string) (repoUrl string, branch string) {
	// Handle full URLs
	if strings.HasPrefix(templateUrl, "https://") || strings.HasPrefix(templateUrl, "http://") {
		// Check for /tree/branch pattern in the URL
		if idx := strings.Index(templateUrl, "/tree/"); idx != -1 {
			repoUrl = templateUrl[:idx]
			branch = templateUrl[idx+len("/tree/"):]
			// Strip trailing slash from branch if present
			branch = strings.TrimRight(branch, "/")
			return repoUrl, branch
		}
		return templateUrl, ""
	}

	// Handle owner/repo format (contains a slash)
	if strings.Contains(templateUrl, "/") {
		return "https://github.com/" + templateUrl, ""
	}

	// Handle bare repo name → Azure-Samples/{repo}
	return "https://github.com/Azure-Samples/" + templateUrl, ""
}

// downloadTemplate downloads a template repository using shallow clone, falling back to GitHub API.
// Returns a path to a temporary directory containing the template files.
func (a *InitFromTemplateAction) downloadTemplate(ctx context.Context, repoUrl string, branch string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "azd-agent-template-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Downloading template...",
		ClearOnStop: true,
	})
	if err := spinner.Start(ctx); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("starting spinner: %w", err)
	}

	// Try shallow clone first
	cloneErr := a.shallowClone(ctx, repoUrl, branch, tmpDir)
	if cloneErr != nil {
		// Fall back to GitHub API download
		fmt.Printf("Git clone failed (%v), falling back to GitHub API download...\n", cloneErr)

		// Clean up the temp dir and recreate
		os.RemoveAll(tmpDir)
		tmpDir, err = os.MkdirTemp("", "azd-agent-template-*")
		if err != nil {
			_ = spinner.Stop(ctx)
			return "", fmt.Errorf("creating temp directory: %w", err)
		}

		if apiErr := a.downloadViaGitHubAPI(ctx, repoUrl, branch, tmpDir); apiErr != nil {
			_ = spinner.Stop(ctx)
			os.RemoveAll(tmpDir)
			return "", exterrors.Dependency(
				exterrors.CodeTemplateDownloadFailed,
				fmt.Sprintf("failed to download template from %s: clone error: %v, API error: %v", repoUrl, cloneErr, apiErr),
				"verify the URL is correct and you have access to the repository",
			)
		}
	}

	if err := spinner.Stop(ctx); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("stopping spinner: %w", err)
	}

	return tmpDir, nil
}

// shallowClone performs a git clone --depth 1 into the target directory.
func (a *InitFromTemplateAction) shallowClone(ctx context.Context, repoUrl string, branch string, target string) error {
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, repoUrl, target)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	// Strip .git directory
	if err := os.RemoveAll(filepath.Join(target, ".git")); err != nil {
		return fmt.Errorf("removing .git: %w", err)
	}

	return nil
}

// downloadViaGitHubAPI downloads a repository's files using the GitHub API tree endpoint.
func (a *InitFromTemplateAction) downloadViaGitHubAPI(ctx context.Context, repoUrl string, branch string, target string) error {
	// Extract owner/repo from the URL
	repoSlug := extractRepoSlug(repoUrl)
	if repoSlug == "" {
		return fmt.Errorf("could not extract owner/repo from URL: %s", repoUrl)
	}

	if branch == "" {
		branch = "main"
	}

	ghToken := gitHubToken()
	apiUrl := fmt.Sprintf("https://api.github.com/repos/%s/git/trees/%s?recursive=1", repoSlug, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
	if err != nil {
		return fmt.Errorf("creating tree request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	setGitHubAuthHeader(req, ghToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching repo tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching repo tree: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading tree response: %w", err)
	}

	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &treeResp); err != nil {
		return fmt.Errorf("parsing tree response: %w", err)
	}

	// Download all blob files
	for _, entry := range treeResp.Tree {
		if entry.Type != "blob" {
			continue
		}

		cleanPath := posixpath.Clean(entry.Path)
		if posixpath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
			return fmt.Errorf("invalid path in repository tree: %s", entry.Path)
		}

		localPath := filepath.Join(target, filepath.FromSlash(cleanPath))
		dir := filepath.Dir(localPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}

		downloadURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repoSlug, branch, cleanPath)
		fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
		if err != nil {
			return fmt.Errorf("creating request for %s: %w", cleanPath, err)
		}
		setGitHubAuthHeader(fileReq, ghToken)

		fileResp, err := a.httpClient.Do(fileReq)
		if err != nil {
			return fmt.Errorf("downloading %s: %w", cleanPath, err)
		}
		if fileResp.StatusCode != http.StatusOK {
			fileResp.Body.Close()
			return fmt.Errorf("downloading %s: status %d", cleanPath, fileResp.StatusCode)
		}

		fileContent, err := io.ReadAll(fileResp.Body)
		fileResp.Body.Close()
		if err != nil {
			return fmt.Errorf("reading %s: %w", cleanPath, err)
		}

		if err := os.WriteFile(localPath, fileContent, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", localPath, err)
		}
	}

	return nil
}

// extractRepoSlug extracts "owner/repo" from a GitHub URL.
func extractRepoSlug(repoUrl string) string {
	// Strip protocol and check for github.com
	url := repoUrl
	for _, prefix := range []string{"https://github.com/", "http://github.com/"} {
		if strings.HasPrefix(url, prefix) {
			url = strings.TrimPrefix(url, prefix)
			// Split remaining path and take first two segments
			parts := strings.SplitN(strings.TrimRight(url, "/"), "/", 3)
			if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
				return parts[0] + "/" + parts[1]
			}
			return ""
		}
	}

	return ""
}

// agentYamlNames lists the recognized agent definition/manifest filenames in priority order.
var agentYamlNames = []string{"agent.yaml", "agent.yml", "agent.manifest.yaml", "agent.manifest.yml"}

// findAgentYaml searches for agent.yaml in the downloaded template directory.
// Search order: root → src/*/ → **/
func findAgentYaml(dir string) (string, error) {
	// 1. Check root
	for _, name := range agentYamlNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// 2. Check src/*/
	srcDir := filepath.Join(dir, "src")
	if entries, err := os.ReadDir(srcDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			for _, name := range agentYamlNames {
				p := filepath.Join(srcDir, entry.Name(), name)
				if _, err := os.Stat(p); err == nil {
					return p, nil
				}
			}
		}
	}

	// 3. Recursive search
	agentYamlNameSet := make(map[string]bool, len(agentYamlNames))
	for _, name := range agentYamlNames {
		agentYamlNameSet[name] = true
	}

	var found string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		base := filepath.Base(path)
		if agentYamlNameSet[base] {
			found = path
			return filepath.SkipAll
		}
		return nil
	})

	if found != "" {
		return found, nil
	}

	return "", exterrors.Dependency(
		exterrors.CodeAgentYamlNotFound,
		"no agent manifest found in the template repository",
		"ensure the template repo contains an agent.yaml or agent.manifest.yaml file at the root, in src/<name>/, or in a subdirectory",
	)
}

// processModelsInteractive uses the init-with-code model selection flow
// (Deploy new / Use existing / Skip) instead of the manifest flow's automatic model processing.
func (a *InitFromTemplateAction) processModelsInteractive(ctx context.Context, manifest *agent_yaml.AgentManifest) (*agent_yaml.AgentManifest, error) {
	// Check if the manifest has model resources
	hasModelResources := false
	for _, resource := range manifest.Resources {
		resourceBytes, err := yaml.Marshal(resource)
		if err != nil {
			continue
		}
		var resourceDef agent_yaml.Resource
		if err := yaml.Unmarshal(resourceBytes, &resourceDef); err != nil {
			continue
		}
		if resourceDef.Kind == agent_yaml.ResourceKindModel {
			hasModelResources = true
			break
		}
	}

	if !hasModelResources {
		return manifest, nil
	}

	// Ask user how they want to configure the model
	modelConfigChoices := []*azdext.SelectChoice{
		{Label: "Deploy a new model from the catalog", Value: "new"},
		{Label: "Select an existing model deployment from a Foundry project", Value: "existing"},
		{Label: "Skip model configuration", Value: "skip"},
	}

	modelConfigResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "How would you like to configure a model for your agent?",
			Choices: modelConfigChoices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("model configuration choice was cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for model configuration choice: %w", err)
	}
	modelConfigChoice := modelConfigChoices[*modelConfigResp.Value].Value

	switch modelConfigChoice {
	case "new":
		if err := a.ensureSubscriptionAndLocation(ctx); err != nil {
			return nil, err
		}

		selectedModel, err := a.selectNewModel(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to select new model: %w", err)
		}

		modelDetails, err := a.resolveModelDeploymentNoPrompt(ctx, selectedModel, a.azureContext.Scope.Location)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve model deployment: %w", err)
		}

		a.deploymentDetails = append(a.deploymentDetails, project.Deployment{
			Name: modelDetails.ModelName,
			Model: project.DeploymentModel{
				Name:    modelDetails.ModelName,
				Format:  modelDetails.Format,
				Version: modelDetails.Version,
			},
			Sku: project.DeploymentSku{
				Name:     modelDetails.Sku.Name,
				Capacity: int(modelDetails.Capacity),
			},
		})

	case "existing":
		if err := a.ensureSubscription(ctx); err != nil {
			return nil, err
		}

		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text:        "Searching for Foundry projects in your subscription...",
			ClearOnStop: true,
		})
		if err := spinner.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start spinner: %w", err)
		}

		projects, err := a.listFoundryProjects(ctx, a.azureContext.Scope.SubscriptionId)
		if stopErr := spinner.Stop(ctx); stopErr != nil {
			return nil, stopErr
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list Foundry projects: %w", err)
		}

		if len(projects) == 0 {
			fmt.Println("No Foundry projects found. Skipping model configuration.")
			return manifest, nil
		}

		// Let user pick a Foundry project
		projectChoices := make([]*azdext.SelectChoice, len(projects))
		for i, p := range projects {
			projectChoices[i] = &azdext.SelectChoice{
				Label: fmt.Sprintf("%s / %s (%s)", p.AccountName, p.ProjectName, p.Location),
				Value: fmt.Sprintf("%d", i),
			}
		}

		projectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select a Foundry project:",
				Choices: projectChoices,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to prompt for project selection: %w", err)
		}

		selectedProject := projects[*projectResp.Value]
		a.azureContext.Scope.Location = selectedProject.Location

		if err := a.processExistingFoundryProject(ctx, selectedProject); err != nil {
			return nil, fmt.Errorf("failed to process Foundry project: %w", err)
		}

		// List deployments
		depSpinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text:        "Searching for model deployments...",
			ClearOnStop: true,
		})
		if err := depSpinner.Start(ctx); err != nil {
			return nil, fmt.Errorf("starting spinner: %w", err)
		}

		deployments, err := a.listProjectDeployments(ctx, selectedProject.SubscriptionId, selectedProject.ResourceGroupName, selectedProject.AccountName)
		if stopErr := depSpinner.Stop(ctx); stopErr != nil {
			return nil, stopErr
		}
		if err != nil {
			return nil, fmt.Errorf("listing deployments: %w", err)
		}

		if len(deployments) == 0 {
			fmt.Println("No existing deployments found. Skipping model configuration.")
			return manifest, nil
		}

		deployChoices := make([]*azdext.SelectChoice, len(deployments))
		for i, d := range deployments {
			label := fmt.Sprintf("%s (%s v%s, %s)", d.Name, d.ModelName, d.Version, d.SkuName)
			deployChoices[i] = &azdext.SelectChoice{
				Label: label,
				Value: d.Name,
			}
		}

		deployResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select a model deployment:",
				Choices: deployChoices,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to prompt for deployment selection: %w", err)
		}

		d := deployments[*deployResp.Value]
		a.deploymentDetails = append(a.deploymentDetails, project.Deployment{
			Name: d.Name,
			Model: project.DeploymentModel{
				Name:    d.ModelName,
				Format:  d.ModelFormat,
				Version: d.Version,
			},
			Sku: project.DeploymentSku{
				Name:     d.SkuName,
				Capacity: d.SkuCapacity,
			},
		})

		if err := a.setEnvVar(ctx, "AZURE_AI_MODEL_DEPLOYMENT_NAME", d.Name); err != nil {
			return nil, fmt.Errorf("failed to set AZURE_AI_MODEL_DEPLOYMENT_NAME: %w", err)
		}

	case "skip":
		// Nothing to do
	}

	return manifest, nil
}

// addToProject adds the agent service to the azd project with smart defaults.
func (a *InitFromTemplateAction) addToProject(ctx context.Context, targetDir string, agentManifest *agent_yaml.AgentManifest) error {
	containerSettings, err := promptContainerPreset(ctx, a.azdClient)
	if err != nil {
		return fmt.Errorf("failed to populate container settings: %w", err)
	}

	agentConfig := project.ServiceTargetAgentConfig{
		Deployments: a.deploymentDetails,
		Container:   containerSettings,
	}

	// Handle tool resources that require connection names - use resource ID as default
	if agentManifest.Resources != nil {
		for _, resource := range agentManifest.Resources {
			if toolResource, ok := resource.(agent_yaml.ToolResource); ok {
				if toolResource.Id == "bing_grounding" || toolResource.Id == "azure_ai_search" {
					agentConfig.Resources = append(agentConfig.Resources, project.Resource{
						Resource:       toolResource.Id,
						ConnectionName: toolResource.Id,
					})
				}
			}
		}
	}

	var agentConfigStruct *structpb.Struct
	if agentConfigStruct, err = project.MarshalStruct(&agentConfig); err != nil {
		return fmt.Errorf("failed to marshal agent config: %w", err)
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:         strings.ReplaceAll(agentManifest.Name, " ", ""),
		RelativePath: targetDir,
		Host:         AiAgentHost,
		Language:     "docker",
		Config:       agentConfigStruct,
		Docker: &azdext.DockerProjectOptions{
			RemoteBuild: true,
		},
	}

	// Prompt for startup command
	absDir, dirErr := resolveProjectDir(ctx, a.azdClient, targetDir)
	if dirErr != nil {
		return fmt.Errorf("resolving project directory: %w", dirErr)
	}

	startupCmd, cmdErr := promptStartupCommand(ctx, a.azdClient, absDir, a.flags.startupCommand, a.flags.NoPrompt)
	if cmdErr != nil {
		return fmt.Errorf("prompting for startup command: %w", cmdErr)
	}
	if startupCmd != "" {
		serviceConfig.AdditionalProperties, err = structpb.NewStruct(map[string]interface{}{
			"startupCommand": startupCmd,
		})
		if err != nil {
			return fmt.Errorf("creating additional properties: %w", err)
		}
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}
	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	fmt.Printf("\nAdded your agent as a service entry named '%s' under the file azure.yaml.\n", agentManifest.Name)
	fmt.Printf("Run %s to run your agent locally, or %s to deploy.\n",
		color.HiBlueString("azd ai agent run"),
		color.HiBlueString("azd ai agent deploy"))
	return nil
}

// The following methods delegate to InitFromCodeAction's implementations for shared functionality.
// This avoids duplication while keeping the template action as a separate type.

func (a *InitFromTemplateAction) ensureProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	codeAction := &InitFromCodeAction{
		azdClient:  a.azdClient,
		flags:      a.flags,
		httpClient: a.httpClient,
	}
	return codeAction.ensureProject(ctx)
}

func (a *InitFromTemplateAction) createEnvironment(ctx context.Context, envName string) error {
	envName = sanitizeAgentName(envName)

	workflow := &azdext.Workflow{
		Name: "env new",
		Steps: []*azdext.WorkflowStep{
			{Command: &azdext.WorkflowCommand{Args: []string{"env", "new", envName}}},
		},
	}

	_, err := a.azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: workflow,
	})
	if err != nil {
		return fmt.Errorf("failed to create environment %s: %w", envName, err)
	}

	fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString(".azure/%s/.env", envName))

	a.flags.env = envName
	env := getExistingEnvironment(ctx, a.flags, a.azdClient)
	if env == nil {
		return fmt.Errorf("environment %s was created but could not be found", envName)
	}

	a.environment = env
	return nil
}

func (a *InitFromTemplateAction) setEnvVar(ctx context.Context, key, value string) error {
	_, err := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: a.environment.Name,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s=%s: %w", key, value, err)
	}
	return nil
}

func (a *InitFromTemplateAction) ensureSubscriptionAndLocation(ctx context.Context) error {
	if a.azureContext.Scope.SubscriptionId == "" {
		if err := a.ensureSubscription(ctx); err != nil {
			return err
		}
	}
	if a.azureContext.Scope.Location == "" {
		if err := a.ensureLocation(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (a *InitFromTemplateAction) ensureSubscription(ctx context.Context) error {
	codeAction := &InitFromCodeAction{
		azdClient:    a.azdClient,
		flags:        a.flags,
		azureContext: a.azureContext,
		environment:  a.environment,
		credential:   a.credential,
		httpClient:   a.httpClient,
	}
	err := codeAction.ensureSubscription(ctx)
	if err != nil {
		return err
	}
	// Copy back state
	a.azureContext = codeAction.azureContext
	a.credential = codeAction.credential
	return nil
}

func (a *InitFromTemplateAction) ensureLocation(ctx context.Context) error {
	codeAction := &InitFromCodeAction{
		azdClient:    a.azdClient,
		flags:        a.flags,
		azureContext: a.azureContext,
		environment:  a.environment,
		credential:   a.credential,
		httpClient:   a.httpClient,
	}
	err := codeAction.ensureLocation(ctx)
	if err != nil {
		return err
	}
	a.azureContext = codeAction.azureContext
	return nil
}

func (a *InitFromTemplateAction) selectNewModel(ctx context.Context) (*azdext.AiModel, error) {
	promptReq := &azdext.PromptAiModelRequest{
		AzureContext: a.azureContext,
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model",
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
		Filter: &azdext.AiModelFilterOptions{
			Locations: []string{a.azureContext.Scope.Location},
		},
		DefaultValue: "gpt-4.1-mini",
	}

	modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for model selection: %w", err)
	}

	return modelResp.Model, nil
}

func (a *InitFromTemplateAction) resolveModelDeploymentNoPrompt(ctx context.Context, model *azdext.AiModel, location string) (*azdext.AiModelDeployment, error) {
	codeAction := &InitFromCodeAction{
		azdClient:    a.azdClient,
		flags:        a.flags,
		azureContext: a.azureContext,
		environment:  a.environment,
		credential:   a.credential,
		httpClient:   a.httpClient,
	}
	return codeAction.resolveModelDeploymentNoPrompt(ctx, model, location)
}

func (a *InitFromTemplateAction) listFoundryProjects(ctx context.Context, subscriptionId string) ([]FoundryProjectInfo, error) {
	codeAction := &InitFromCodeAction{
		azdClient:    a.azdClient,
		flags:        a.flags,
		azureContext: a.azureContext,
		environment:  a.environment,
		credential:   a.credential,
		httpClient:   a.httpClient,
	}
	return codeAction.listFoundryProjects(ctx, subscriptionId)
}

func (a *InitFromTemplateAction) listProjectDeployments(ctx context.Context, subscriptionId, resourceGroup, accountName string) ([]FoundryDeploymentInfo, error) {
	codeAction := &InitFromCodeAction{
		azdClient:    a.azdClient,
		flags:        a.flags,
		azureContext: a.azureContext,
		environment:  a.environment,
		credential:   a.credential,
		httpClient:   a.httpClient,
	}
	return codeAction.listProjectDeployments(ctx, subscriptionId, resourceGroup, accountName)
}

func (a *InitFromTemplateAction) processExistingFoundryProject(ctx context.Context, foundryProject FoundryProjectInfo) error {
	codeAction := &InitFromCodeAction{
		azdClient:    a.azdClient,
		flags:        a.flags,
		azureContext: a.azureContext,
		environment:  a.environment,
		credential:   a.credential,
		httpClient:   a.httpClient,
	}
	err := codeAction.processExistingFoundryProject(ctx, foundryProject)
	if err != nil {
		return err
	}
	a.azureContext = codeAction.azureContext
	return nil
}
