// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

// GitHubUrlInfo holds parsed information from a GitHub URL.
type GitHubUrlInfo struct {
	RepoSlug string
	Branch   string
	FilePath string
	Hostname string
}

// scaffoldTrainingProject materializes a training project's job definition
// into workingDir based on the user's --template input.
//
//   - If template is empty: prompt for compute + environment (no defaults
//     for either since both are environment-specific) and write a minimal
//     hello-world job.yaml at <workingDir>/config/job.yaml. Skips writing
//     if the file already exists so re-running 'init' doesn't clobber
//     user edits.
//   - If template is a GitHub URL: download the parent directory of the
//     referenced file into workingDir using the gh CLI.
//   - If template is a local path: recursively copy that directory into
//     workingDir.
func scaffoldTrainingProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	template string,
	workingDir string,
) error {
	if workingDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %w", err)
		}
		workingDir = cwd
	}

	if template == "" {
		return scaffoldDefaultJobYaml(ctx, azdClient, workingDir)
	}

	if isGitHubUrl(template) {
		return scaffoldFromGitHub(ctx, azdClient, template, workingDir)
	}

	return scaffoldFromLocalPath(template, workingDir)
}

// scaffoldDefaultJobYaml writes a minimal hello-world commandJob to
// <workingDir>/config/job.yaml after prompting for compute + environment.
// No-op (with a notice) if the file already exists.
func scaffoldDefaultJobYaml(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	workingDir string,
) error {
	yamlPath := filepath.Join(workingDir, "config", "job.yaml")

	if _, err := os.Stat(yamlPath); err == nil {
		fmt.Printf("Skipping job.yaml scaffolding: %s already exists.\n", yamlPath)
		return nil
	}

	envResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Enter the container image (ACR or MCR URI) to use as the job environment",
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to prompt for environment: %w", err)
	}
	environment := strings.TrimSpace(envResp.Value)
	if environment == "" {
		return fmt.Errorf("environment is required")
	}

	computeResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Enter the compute target (cluster name or full ARM resource ID)",
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to prompt for compute: %w", err)
	}
	compute := strings.TrimSpace(computeResp.Value)
	if compute == "" {
		return fmt.Errorf("compute is required")
	}

	yamlContent := fmt.Sprintf(`$schema: https://azuremlschemas.azureedge.net/latest/commandJob.schema.json
type: command

display_name: cli-hello-world
description: Sample job created by 'azd ai training init'

command: echo "hello world"
environment: %s
compute: %s
`, environment, compute)

	//nolint:gosec // project config directory should be readable and traversable
	if err := os.MkdirAll(filepath.Dir(yamlPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	//nolint:gosec // generated config file should be readable by project tooling
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("failed to write job.yaml: %w", err)
	}

	fmt.Printf("Created training job template at: %s\n", yamlPath)
	return nil
}

// scaffoldFromGitHub downloads the parent directory of the referenced file
// from GitHub into workingDir using the gh CLI.
func scaffoldFromGitHub(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	template string,
	workingDir string,
) error {
	fmt.Println("Downloading training template from GitHub...")

	commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	console := input.NewConsole(
		false, true,
		input.Writers{Output: os.Stdout},
		input.ConsoleHandles{Stderr: os.Stderr, Stdin: os.Stdin, Stdout: os.Stdout},
		nil, nil,
	)

	ghCli := github.NewGitHubCli(console, commandRunner)
	if err := ghCli.EnsureInstalled(ctx); err != nil {
		return fmt.Errorf("ensuring gh is installed: %w", err)
	}

	parseResp, err := azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
		Url: template,
	})
	if err != nil {
		return fmt.Errorf("parsing GitHub URL: %w", err)
	}

	urlInfo := &GitHubUrlInfo{
		RepoSlug: parseResp.RepoSlug,
		Branch:   parseResp.Branch,
		FilePath: parseResp.FilePath,
		Hostname: parseResp.Hostname,
	}

	if urlInfo.Branch != "" {
		fmt.Printf("Downloading from branch: %s\n", urlInfo.Branch)
	}
	if err := downloadParentDirectory(ctx, urlInfo, workingDir, ghCli); err != nil {
		return fmt.Errorf("downloading parent directory: %w", err)
	}

	return nil
}

// scaffoldFromLocalPath recursively copies srcPath into workingDir.
func scaffoldFromLocalPath(srcPath, workingDir string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("failed to access template path %q: %w", srcPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("template path %q is not a directory", srcPath)
	}
	if err := copyDirectory(srcPath, workingDir); err != nil {
		return fmt.Errorf("failed to copy template directory: %w", err)
	}
	fmt.Printf("Copied training template from %s to %s\n", srcPath, workingDir)
	return nil
}

// isGitHubUrl returns true when the URL points at a GitHub host.
func isGitHubUrl(s string) bool {
	parsedURL, err := url.Parse(s)
	if err != nil {
		return false
	}
	hostname := parsedURL.Hostname()
	return strings.HasPrefix(hostname, "raw.githubusercontent") ||
		strings.HasPrefix(hostname, "api.github") ||
		strings.Contains(hostname, "github")
}

// downloadParentDirectory downloads the directory containing urlInfo.FilePath
// (i.e. the file's parent) from GitHub into targetDir.
func downloadParentDirectory(
	ctx context.Context,
	urlInfo *GitHubUrlInfo,
	targetDir string,
	ghCli *github.Cli,
) error {
	pathParts := strings.Split(urlInfo.FilePath, "/")
	if len(pathParts) <= 1 {
		fmt.Println("Template file is at repository root; nothing else to download.")
		return nil
	}
	parentDirPath := strings.Join(pathParts[:len(pathParts)-1], "/")

	fmt.Printf("Downloading directory '%s' from %s (branch: %q)\n",
		parentDirPath, urlInfo.RepoSlug, urlInfo.Branch)

	return downloadDirectoryContents(
		ctx, urlInfo.Hostname, urlInfo.RepoSlug, parentDirPath, urlInfo.Branch, targetDir, ghCli,
	)
}

func downloadDirectoryContents(
	ctx context.Context,
	hostname, repoSlug, dirPath, branch, localPath string,
	ghCli *github.Cli,
) error {
	apiPath := fmt.Sprintf("/repos/%s/contents/%s", repoSlug, dirPath)
	if branch != "" {
		apiPath += fmt.Sprintf("?ref=%s", branch)
	}

	dirContentsJSON, err := ghCli.ApiCall(ctx, hostname, apiPath, github.ApiCallOptions{})
	if err != nil {
		return fmt.Errorf("failed to list directory contents: %w", err)
	}

	var dirContents []map[string]any
	if err := json.Unmarshal([]byte(dirContentsJSON), &dirContents); err != nil {
		return fmt.Errorf("failed to parse directory contents JSON: %w", err)
	}

	for _, item := range dirContents {
		name, _ := item["name"].(string)
		itemType, _ := item["type"].(string)
		if name == "" || itemType == "" {
			continue
		}

		itemPath := fmt.Sprintf("%s/%s", dirPath, name)
		itemLocalPath := filepath.Join(localPath, name)

		switch itemType {
		case "file":
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
		case "dir":
			//nolint:gosec // scaffolded directories are intended to be readable/traversable
			if err := os.MkdirAll(itemLocalPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", itemLocalPath, err)
			}
			if err := downloadDirectoryContents(
				ctx, hostname, repoSlug, itemPath, branch, itemLocalPath, ghCli,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyDirectory recursively copies all files and directories from src to dst.
func copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if d.IsDir() {
			//nolint:gosec // copied project directories should remain readable/traversable
			return os.MkdirAll(dstPath, 0755)
		}
		return copyFile(path, dstPath)
	})
}

// copyFile copies a single file from src to dst, creating parent dirs as needed.
func copyFile(src, dst string) error {
	//nolint:gosec // copied project directories should remain readable/traversable
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	//nolint:gosec // src is a local project file path
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	//nolint:gosec // dst is a local project file path
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = srcFile.WriteTo(dstFile)
	return err
}
