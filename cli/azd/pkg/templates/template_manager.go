package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/httpUtil"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/azure/azure-dev/cli/azd/resources"
)

type TemplateManager struct {
}

// Get a set of templates where each key is the name of the template
func (tm *TemplateManager) ListTemplates() (map[string]Template, error) {
	result := make(map[string]Template)
	var templates []Template
	err := json.Unmarshal(resources.TemplatesJson, &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	for _, template := range templates {
		result[template.Name] = template
	}

	return result, nil
}

func (tm *TemplateManager) GetTemplate(templateName string) (Template, error) {
	templates, err := tm.ListTemplates()

	if err != nil {
		return Template{}, fmt.Errorf("unable to list templates: %w", err)
	}

	if matchingTemplate, ok := templates[templateName]; ok {
		return matchingTemplate, nil
	}

	return Template{}, fmt.Errorf("template with name '%s' was not found", templateName)
}

func NewTemplateManager() *TemplateManager {
	return &TemplateManager{}
}

type gitRepository struct {
	host   string
	slug   string
	branch string
}

func (repoInfo *gitRepository) DownloadZipUrl() string {
	return fmt.Sprintf(
		"https://%s/%s/archive/%s.zip",
		repoInfo.host,
		repoInfo.slug,
		repoInfo.branch,
	)
}

func parseAsGit(url string) (gitRepository, error) {
	hostAndSlug := strings.Split(strings.Split(url, "@")[1], ":")

	return gitRepository{
		host: hostAndSlug[0],
		slug: strings.Split(hostAndSlug[1], ".git")[0],
	}, nil
}

func parseAsHttp(url string) (gitRepository, error) {
	hostAndSlug := strings.Split(strings.Split(url, "://")[1], "/")

	return gitRepository{
		host: hostAndSlug[0],
		slug: strings.Split(strings.Join(hostAndSlug[1:], "/"), ".git")[0],
	}, nil
}

func parseRepoUrl(url string, branch string) (gitRepository, error) {
	var result gitRepository
	var err error

	if strings.HasPrefix(url, "git") {
		result, err = parseAsGit(url)
	} else if strings.HasPrefix(url, "http") {
		result, err = parseAsHttp(url)
	}

	if err != nil {
		return result, fmt.Errorf("parsing repo url: %w", err)
	}
	result.branch = resolveBranchName(branch)
	return result, nil
}

// moveFolderContentToParentFolder gets the name of a folder inside the
// parentFolder and moves its content to the parent folder.
func moveFolderContentToParentFolder(ctx context.Context, parentFolder string) error {
	parentDirectory, err := os.Open(parentFolder)
	if err != nil {
		return fmt.Errorf("failed renaming folder: %w", err)
	}
	parentDirectoryFiles, err := parentDirectory.ReadDir(0)
	if err != nil {
		return fmt.Errorf("failed renaming folder: %w", err)
	}

	var folders []string
	for _, file := range parentDirectoryFiles {
		if file.IsDir() {
			folders = append(folders, file.Name())
		}
	}

	for _, folderName := range folders {
		tmpFolder := parentFolder + "tmp"
		folderPath := filepath.Join(tmpFolder, folderName)

		if err := os.Rename(parentFolder, tmpFolder); err != nil {
			return fmt.Errorf("failed renaming folder: %w", err)
		}
		if err := os.Rename(folderPath, parentFolder); err != nil {
			return fmt.Errorf("failed renaming folder: %w", err)
		}
	}
	return nil
}

func resolveBranchName(branch string) string {
	defaultBranch := "main"
	if branch != "" {
		defaultBranch = branch
	}
	return defaultBranch
}

const gitPath string = "AZD_GIT_PAT"
const aboutPat string = "https://docs.github.com/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token"

func FetchTemplateFromUrl(ctx context.Context, repositoryPath string, branch string, target string) error {
	fetchUrl, err := parseRepoUrl(repositoryPath, branch)
	if err != nil {
		return err
	}

	zipFile := filepath.Join(target, fetchUrl.branch+".zip")
	authPat := osutil.GetenvOrDefault(gitPath, "")
	result, err := httpUtil.DownloadFile(ctx, fetchUrl.DownloadZipUrl(), authPat, zipFile)
	if err != nil {
		return fmt.Errorf("failed to fetch repository %s: %w", repositoryPath, err)
	}
	if !result.Success() {
		return fmt.Errorf(
			"Repository %s was not found.\n"+
				"If this is a private repo, set environment variable: %s with auth token.\n"+
				"You can use a personal access token for GitHub.\n"+
				"See how to create it here: %s",
			repositoryPath,
			gitPath,
			aboutPat)
	}

	// unzip
	if err := rzip.Extract(ctx, zipFile, target); err != nil {
		return fmt.Errorf("failed to unzip repository %s: %w", repositoryPath, err)
	}
	// remove zip
	os.Remove(zipFile)

	// move content one level up
	if err = moveFolderContentToParentFolder(ctx, target); err != nil {
		return err
	}

	if err := os.RemoveAll(filepath.Join(target, ".git")); err != nil {
		return fmt.Errorf("removing .git folder after clone: %w", err)
	}

	return nil
}
