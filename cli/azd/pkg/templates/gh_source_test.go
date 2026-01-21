// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GhSourceRawFile(t *testing.T) {
	name := "test"
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	expectedResult, err := json.Marshal(testTemplates)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).Respond(exec.RunResult{
		Stdout: string(expectedResult),
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	source, err := newGhTemplateSource(
		*mockContext.Context, name, "https://raw.github.com/owner/repo/branch/path/to/the/folder/file.json", ghCli,
		mockContext.Console)
	require.Nil(t, err)
	require.Equal(t, name, source.Name())
}

func Test_GhSourceApiFile(t *testing.T) {
	name := "test"
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	expectedResult, err := json.Marshal(testTemplates)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).Respond(exec.RunResult{
		Stdout: string(expectedResult),
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	source, err := newGhTemplateSource(
		*mockContext.Context, name, "https://api.github.com/repos/owner/repo/contents/path/to/the/folder/file.json", ghCli,
		mockContext.Console)
	require.Nil(t, err)
	require.Equal(t, name, source.Name())
}

func Test_GhSourceUrl(t *testing.T) {
	name := "test"
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	expectedResult, err := json.Marshal(testTemplates)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).Respond(exec.RunResult{
		Stdout: string(expectedResult),
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	source, err := newGhTemplateSource(
		*mockContext.Context, name, "https://github.com/owner/repo/branch/path/to/the/folder/file.json", ghCli,
		mockContext.Console)
	require.Nil(t, err)
	require.Equal(t, name, source.Name())
}

// Test_GhSourceUrlWithBlobAndBranchSlashes tests GitHub URLs with "blob" segment and branch names containing slashes
func Test_GhSourceUrlWithBlobAndBranchSlashes(t *testing.T) {
	name := "test"
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	expectedResult, err := json.Marshal(testTemplates)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]

		// Branch API calls during ParseGitHubUrl
		if strings.Contains(apiURL, "/branches/") {
			// "agentserver" alone doesn't exist
			// Branch names with slashes are URL-encoded: agentserver/first-release -> agentserver%2Ffirst-release
			if strings.HasSuffix(apiURL, "/branches/agentserver%2Ffirst-release") {
				return exec.RunResult{Stdout: `{"name":"agentserver/first-release"}`}, nil
			}
			if strings.HasSuffix(apiURL, "/branches/agentserver") {
				return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
			}
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}

		// Content API call
		if strings.Contains(apiURL, "/contents/") {
			require.Contains(t, apiURL, "contents/path/to/file.json")
			require.Contains(t, apiURL, "ref=agentserver%2Ffirst-release")
			return exec.RunResult{Stdout: string(expectedResult)}, nil
		}

		return exec.RunResult{Stdout: "", Stderr: "Unexpected call", ExitCode: 1}, fmt.Errorf("unexpected call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// URL with branch name containing slash: agentserver/first-release
	source, err := newGhTemplateSource(
		*mockContext.Context,
		name,
		"https://github.com/owner/repo/blob/agentserver/first-release/path/to/file.json",
		ghCli,
		mockContext.Console,
	)
	require.NoError(t, err)
	require.Equal(t, name, source.Name())
}

// Test_GhSourceUrlWithTreeAndBranchSlashes tests GitHub URLs with "tree" segment and branch names containing slashes
func Test_GhSourceUrlWithTreeAndBranchSlashes(t *testing.T) {
	name := "test"
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	expectedResult, err := json.Marshal(testTemplates)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]

		// Branch API calls during ParseGitHubUrl
		if strings.Contains(apiURL, "/branches/") {
			// Only "feature/new-feature" exists. "feature" alone does NOT exist.
			// Git does not allow both "feature" and "feature/new-feature" to exist simultaneously.
			// Branch names with slashes are URL-encoded: feature/new-feature -> feature%2Fnew-feature
			if strings.HasSuffix(apiURL, "/branches/feature%2Fnew-feature") {
				return exec.RunResult{Stdout: `{"name":"feature/new-feature"}`}, nil
			}
			// "feature" alone does not exist
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}

		// Content API call
		if strings.Contains(apiURL, "/contents/") {
			require.Contains(t, apiURL, "contents/agent.yaml")
			require.Contains(t, apiURL, "ref=feature%2Fnew-feature")
			return exec.RunResult{Stdout: string(expectedResult)}, nil
		}

		return exec.RunResult{Stdout: "", Stderr: "Unexpected call", ExitCode: 1}, fmt.Errorf("unexpected call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// URL with tree and branch name containing slash
	source, err := newGhTemplateSource(
		*mockContext.Context,
		name,
		"https://github.com/owner/repo/tree/feature/new-feature/agent.yaml",
		ghCli,
		mockContext.Console,
	)
	require.NoError(t, err)
	require.Equal(t, name, source.Name())
}

// Test_GhSourceRawFileWithBranchSlashes tests raw GitHub URLs with branch names containing multiple slashes
func Test_GhSourceRawFileWithBranchSlashes(t *testing.T) {
	name := "test"
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	expectedResult, err := json.Marshal(testTemplates)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]

		// Branch API calls during ParseGitHubUrl - test multiple slashes
		if strings.Contains(apiURL, "/branches/") {
			// Only "release/v1.0/candidate" exists.
			// Git does not allow "release", "release/v1.0", and "release/v1.0/candidate" to coexist.
			// Branch names with slashes are URL-encoded: release/v1.0/candidate -> release%2Fv1.0%2Fcandidate
			if strings.HasSuffix(apiURL, "/branches/release%2Fv1.0%2Fcandidate") {
				return exec.RunResult{Stdout: `{"name":"release/v1.0/candidate"}`}, nil
			}
			// Shorter prefixes do not exist as branches
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}

		// Content API call
		if strings.Contains(apiURL, "/contents/") {
			require.Contains(t, apiURL, "ref=release%2Fv1.0%2Fcandidate")
			return exec.RunResult{Stdout: string(expectedResult)}, nil
		}

		return exec.RunResult{Stdout: "", Stderr: "Unexpected call", ExitCode: 1}, fmt.Errorf("unexpected call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// URL with branch containing multiple slashes
	source, err := newGhTemplateSource(
		*mockContext.Context,
		name,
		"https://raw.githubusercontent.com/owner/repo/release/v1.0/candidate/config/template.json",
		ghCli,
		mockContext.Console,
	)
	require.NoError(t, err)
	require.Equal(t, name, source.Name())
}

// Test_GhSourceApiFileWithRefParameter tests API URLs with ref query parameter containing slashes
func Test_GhSourceApiFileWithRefParameter(t *testing.T) {
	name := "test"
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	expectedResult, err := json.Marshal(testTemplates)
	require.NoError(t, err)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		// API URL should preserve the ref parameter with branch containing slashes
		require.Contains(t, apiURL, "ref=hotfix%2Furgent")
		return exec.RunResult{Stdout: string(expectedResult)}, nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// API URL with ref parameter containing branch with slash
	source, err := newGhTemplateSource(
		*mockContext.Context,
		name,
		"https://api.github.com/repos/owner/repo/contents/path/to/file.json?ref=hotfix/urgent",
		ghCli,
		mockContext.Console,
	)
	require.NoError(t, err)
	require.Equal(t, name, source.Name())
}

// Test_ParseGitHubUrl_RawUrl tests parsing raw.githubusercontent.com URLs
func Test_ParseGitHubUrl_RawUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		// Check for branch existence API call
		if strings.Contains(apiURL, "/branches/") {
			if strings.HasSuffix(apiURL, "/branches/main") {
				return exec.RunResult{Stdout: `{"name":"main"}`}, nil
			}
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}
		return exec.RunResult{Stdout: ""}, fmt.Errorf("unexpected API call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	urlInfo, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://raw.githubusercontent.com/owner/repo/main/path/to/file.yaml",
		ghCli,
	)
	require.NoError(t, err)
	require.Equal(t, "github.com", urlInfo.Hostname)
	require.Equal(t, "owner/repo", urlInfo.RepoSlug)
	require.Equal(t, "main", urlInfo.Branch)
	require.Equal(t, "path/to/file.yaml", urlInfo.FilePath)
}

// Test_ParseGitHubUrl_BlobUrl tests parsing github.com/blob URLs
func Test_ParseGitHubUrl_BlobUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		if strings.Contains(apiURL, "/branches/") {
			if strings.HasSuffix(apiURL, "/branches/develop") {
				return exec.RunResult{Stdout: `{"name":"develop"}`}, nil
			}
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}
		return exec.RunResult{Stdout: ""}, fmt.Errorf("unexpected API call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	urlInfo, err := ParseGitHubUrl(*mockContext.Context, "https://github.com/owner/repo/blob/develop/src/main.go", ghCli)
	require.NoError(t, err)
	require.Equal(t, "github.com", urlInfo.Hostname)
	require.Equal(t, "owner/repo", urlInfo.RepoSlug)
	require.Equal(t, "develop", urlInfo.Branch)
	require.Equal(t, "src/main.go", urlInfo.FilePath)
}

// Test_ParseGitHubUrl_TreeUrl tests parsing github.com/tree URLs
func Test_ParseGitHubUrl_TreeUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		if strings.Contains(apiURL, "/branches/") {
			if strings.HasSuffix(apiURL, "/branches/feature-branch") {
				return exec.RunResult{Stdout: `{"name":"feature-branch"}`}, nil
			}
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}
		return exec.RunResult{Stdout: ""}, fmt.Errorf("unexpected API call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	urlInfo, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.com/owner/repo/tree/feature-branch/docs/readme.md",
		ghCli,
	)
	require.NoError(t, err)
	require.Equal(t, "github.com", urlInfo.Hostname)
	require.Equal(t, "owner/repo", urlInfo.RepoSlug)
	require.Equal(t, "feature-branch", urlInfo.Branch)
	require.Equal(t, "docs/readme.md", urlInfo.FilePath)
}

// Test_ParseGitHubUrl_ApiUrl tests parsing api.github.com URLs
func Test_ParseGitHubUrl_ApiUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// API URLs don't need branch resolution, branch comes from query parameter
	urlInfo, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://api.github.com/repos/owner/repo/contents/config/app.json?ref=v1.0.0",
		ghCli,
	)
	require.NoError(t, err)
	require.Equal(t, "github.com", urlInfo.Hostname)
	require.Equal(t, "owner/repo", urlInfo.RepoSlug)
	require.Equal(t, "v1.0.0", urlInfo.Branch)
	require.Equal(t, "config/app.json", urlInfo.FilePath)
}

// Test_ParseGitHubUrl_BranchWithSlashes tests parsing URLs with branch names containing slashes
func Test_ParseGitHubUrl_BranchWithSlashes(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		if strings.Contains(apiURL, "/branches/") {
			// "agentserver" alone doesn't exist
			// Branch names with slashes are URL-encoded: agentserver/first-release -> agentserver%2Ffirst-release
			if strings.HasSuffix(apiURL, "/branches/agentserver%2Ffirst-release") {
				return exec.RunResult{Stdout: `{"name":"agentserver/first-release"}`}, nil
			}
			if strings.HasSuffix(apiURL, "/branches/agentserver") {
				return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
			}
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}
		return exec.RunResult{Stdout: ""}, fmt.Errorf("unexpected API call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// Now ParseGitHubUrl resolves the full branch name by checking GitHub API
	urlInfo, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.com/Azure/azure-sdk-for-net/blob/agentserver/first-release/sdk/agent.yaml",
		ghCli,
	)
	require.NoError(t, err)
	require.Equal(t, "github.com", urlInfo.Hostname)
	require.Equal(t, "Azure/azure-sdk-for-net", urlInfo.RepoSlug)
	require.Equal(t, "agentserver/first-release", urlInfo.Branch) // Full branch name resolved
	require.Equal(t, "sdk/agent.yaml", urlInfo.FilePath)
}

// Test_ParseGitHubUrl_EnterpriseUrl tests parsing GitHub Enterprise URLs
func Test_ParseGitHubUrl_EnterpriseUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout: "Logged in to",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		if strings.Contains(apiURL, "/branches/") {
			if strings.HasSuffix(apiURL, "/branches/main") {
				return exec.RunResult{Stdout: `{"name":"main"}`}, nil
			}
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}
		return exec.RunResult{Stdout: ""}, fmt.Errorf("unexpected API call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	urlInfo, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.company.com/team/project/blob/main/README.md",
		ghCli,
	)
	require.NoError(t, err)
	require.Equal(t, "github.company.com", urlInfo.Hostname)
	require.Equal(t, "team/project", urlInfo.RepoSlug)
	require.Equal(t, "main", urlInfo.Branch)
	require.Equal(t, "README.md", urlInfo.FilePath)
}

// Test_ParseGitHubUrl_InvalidUrl tests error handling for invalid URLs
func Test_ParseGitHubUrl_InvalidUrl(t *testing.T) {
	testCases := []struct {
		name string
		url  string
	}{
		{"empty url", ""},
		{"invalid protocol", "ftp://github.com/owner/repo/file.txt"},
		{"incomplete path", "https://github.com/owner"},
		{"invalid raw format", "https://raw.githubusercontent.com/owner/repo"},
		{"invalid api format", "https://api.github.com/repos/owner"},
	}

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseGitHubUrl(*mockContext.Context, tc.url, ghCli)
			require.Error(t, err)
		})
	}
}

// Test_ParseGitHubUrl_NotAuthenticated tests that ParseGitHubUrl properly handles unauthenticated scenarios
func Test_ParseGitHubUrl_NotAuthenticated(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	// Simulate not authenticated scenario
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{
		Stdout:   "",
		Stderr:   "To get started with GitHub CLI, please run:  gh auth login",
		ExitCode: 1,
	})

	// Mock the login call
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(
			command, string(filepath.Separator)+"gh") && args.Args[0] == "auth" && args.Args[1] == "login"
	}).Respond(exec.RunResult{
		Stdout: "✓ Logged in as user",
	})

	// After login, branch API calls should succeed
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		if strings.Contains(apiURL, "/branches/") {
			if strings.HasSuffix(apiURL, "/branches/main") {
				return exec.RunResult{Stdout: `{"name":"main"}`}, nil
			}
			return exec.RunResult{Stdout: "", Stderr: "Not Found", ExitCode: 404}, fmt.Errorf("not found")
		}
		return exec.RunResult{Stdout: ""}, fmt.Errorf("unexpected API call")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// This should trigger authentication before attempting to resolve the branch
	urlInfo, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.com/owner/repo/blob/main/path/to/file.yaml",
		ghCli,
	)
	require.NoError(t, err)
	require.Equal(t, "github.com", urlInfo.Hostname)
	require.Equal(t, "owner/repo", urlInfo.RepoSlug)
	require.Equal(t, "main", urlInfo.Branch)
	require.Equal(t, "path/to/file.yaml", urlInfo.FilePath)
}
