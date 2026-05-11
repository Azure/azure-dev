// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"encoding/json"
	"errors"
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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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

	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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

// Test_ParseGitHubUrl_AccessErrorShortCircuits verifies that when the
// GitHub API returns a typed access failure (e.g., 403 SAML enforcement),
// resolveBranchAndPath returns the underlying *github.ApiError immediately
// instead of walking every candidate branch and emitting the misleading
// "could not find a valid branch in the URL path" message. The error
// surface is the typed error so the YAML error-suggestion pipeline can
// attach an actionable message.
func Test_ParseGitHubUrl_AccessErrorShortCircuits(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "--version"
	}).Respond(exec.RunResult{Stdout: github.Version.String()})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{Stdout: "Logged in to"})

	// Track number of branch lookups so we can assert short-circuit.
	callCount := 0
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		callCount++
		// Real gh CLI emits stderr in this format; parseApiError reads it.
		stderr := "gh: Resource protected by organization SAML enforcement. " +
			"You must grant your OAuth token access to this organization. (HTTP 403)"
		return exec.RunResult{Stdout: "", Stderr: stderr, ExitCode: 1},
			fmt.Errorf("exit code: 1, stdout: , stderr: %s", stderr)
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	_, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.com/org/repo/blob/feature/sub/path/file.yaml",
		ghCli,
	)
	require.Error(t, err)

	// Must surface as the typed *github.ApiError so withGitHubSuggestion
	// can map it to a SAML-specific suggestion inline.
	apiErr, ok := errors.AsType[*github.ApiError](err)
	require.True(t, ok, "expected *github.ApiError, got %T: %v", err, err)
	require.Equal(t, github.KindSAMLBlocked, apiErr.Kind)
	require.Equal(t, 403, apiErr.StatusCode)

	// The walk must short-circuit on the first auth failure rather than
	// trying every candidate branch (would be 4 calls for "feature/sub/path/file.yaml").
	require.Equal(t, 1, callCount, "expected branch walk to short-circuit on first access error")

	// And the misleading "could not find a valid branch" message must NOT appear.
	require.NotContains(t, err.Error(), "could not find a valid branch")
}

// Test_ParseGitHubUrl_RepoNotAccessibleFallback verifies the private/EMU
// codepath: when every branch lookup returns 404 (no positive access error),
// resolveBranchAndPath probes /repos/{slug}; if that also returns 404 the
// error surfaces as *RepoNotAccessibleError instead of the misleading
// "could not find a valid branch" message. This is the other half of the
// PR's value alongside Test_ParseGitHubUrl_AccessErrorShortCircuits.
func Test_ParseGitHubUrl_RepoNotAccessibleFallback(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "--version"
	}).Respond(exec.RunResult{Stdout: github.Version.String()})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{Stdout: "Logged in to"})

	// Every gh api call (branch lookups + repo probe) returns a 404 with the
	// real GitHub JSON error envelope so parseApiError classifies as KindNotFound.
	notFoundStdout := `{"message":"Not Found","documentation_url":"...","status":"404"}`
	notFoundStderr := "gh: Not Found (HTTP 404)"
	branchCallCount := 0
	repoProbeCount := 0
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		switch {
		case strings.Contains(apiURL, "/branches/"):
			branchCallCount++
		case strings.HasSuffix(apiURL, "/repos/owner/repo"):
			repoProbeCount++
		}
		return exec.RunResult{Stdout: notFoundStdout, Stderr: notFoundStderr, ExitCode: 1},
			fmt.Errorf("exit code: 1, stdout: %s, stderr: %s", notFoundStdout, notFoundStderr)
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)

	// 4-segment branch+path so branch walker exhausts all candidates before
	// falling back to the repo probe.
	_, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.com/owner/repo/blob/feature/sub/path/file.yaml",
		ghCli,
	)
	require.Error(t, err)

	// All 4 branch candidates were tried (no short-circuit, since 404 is
	// "not a branch", not an access failure)...
	require.Equal(t, 4, branchCallCount, "expected branch walk to try every candidate on 404")
	// ...followed by exactly one /repos/{slug} probe.
	require.Equal(t, 1, repoProbeCount, "expected exactly one /repos/{slug} probe after branch walk")

	// The error must surface as *RepoNotAccessibleError so the suggestion
	// pipeline (gh_errors.go) attaches EMU/private-repo guidance.
	repoErr, ok := errors.AsType[*RepoNotAccessibleError](err)
	require.True(t, ok, "expected *RepoNotAccessibleError, got %T: %v", err, err)
	require.Equal(t, "github.com", repoErr.Hostname)
	require.Equal(t, "owner/repo", repoErr.RepoSlug)

	// And the misleading "could not find a valid branch" message must NOT appear.
	require.NotContains(t, err.Error(), "could not find a valid branch")
}

// Test_ParseGitHubUrl_RepoAccessibleFallsThrough covers the "repo exists,
// branch genuinely doesn't" path: every branch candidate returns 404 but
// the /repos/{slug} probe returns 200, so checkRepoAccessible returns nil
// and the original "could not find a valid branch" message surfaces.
func Test_ParseGitHubUrl_RepoAccessibleFallsThrough(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "--version"
	}).Respond(exec.RunResult{Stdout: github.Version.String()})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{Stdout: "Logged in to"})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		// Repo probe succeeds; branch probes all 404.
		if strings.HasSuffix(apiURL, "/repos/owner/repo") {
			return exec.RunResult{Stdout: `{"name":"repo"}`}, nil
		}
		stdout := `{"message":"Not Found","documentation_url":"...","status":"404"}`
		stderr := "gh: Not Found (HTTP 404)"
		return exec.RunResult{Stdout: stdout, Stderr: stderr, ExitCode: 1},
			fmt.Errorf("exit code: 1, stdout: %s, stderr: %s", stdout, stderr)
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	_, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.com/owner/repo/blob/feature/sub/path/file.yaml",
		ghCli,
	)
	require.Error(t, err)

	// Repo IS accessible, so RepoNotAccessibleError must NOT appear.
	_, ok := errors.AsType[*RepoNotAccessibleError](err)
	require.False(t, ok, "repo is accessible — must not surface RepoNotAccessibleError")
	// Original "no valid branch" message is the right surface here.
	require.Contains(t, err.Error(), "could not find a valid branch")
}

// Test_ParseGitHubUrl_RepoProbeReturnsClassifiedError covers the case
// where the branch walk exhausts on 404 but the /repos/{slug} probe itself
// returns a classified access error (e.g., SAML on the org). The repo
// probe's typed error must propagate (not the misleading branch message
// and not RepoNotAccessibleError, since this is an auth failure rather
// than a not-found).
func Test_ParseGitHubUrl_RepoProbeReturnsClassifiedError(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "--version"
	}).Respond(exec.RunResult{Stdout: github.Version.String()})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "auth" && args.Args[1] == "status"
	}).Respond(exec.RunResult{Stdout: "Logged in to"})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") &&
			args.Args[0] == "api"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		apiURL := args.Args[1]
		if strings.HasSuffix(apiURL, "/repos/owner/repo") {
			// Repo probe returns SAML 403 (e.g., the org enforces SSO and
			// the user's token isn't authorized).
			samlStdout := `{"message":"Resource protected by organization SAML enforcement.",` +
				`"documentation_url":"...","status":"403"}`
			samlStderr := "gh: Resource protected by organization SAML enforcement. (HTTP 403)"
			return exec.RunResult{Stdout: samlStdout, Stderr: samlStderr, ExitCode: 1},
				fmt.Errorf("exit code: 1, stdout: %s, stderr: %s", samlStdout, samlStderr)
		}
		// Branch probes all 404.
		stdout := `{"message":"Not Found","documentation_url":"...","status":"404"}`
		stderr := "gh: Not Found (HTTP 404)"
		return exec.RunResult{Stdout: stdout, Stderr: stderr, ExitCode: 1},
			fmt.Errorf("exit code: 1, stdout: %s, stderr: %s", stdout, stderr)
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	_, err := ParseGitHubUrl(
		*mockContext.Context,
		"https://github.com/owner/repo/blob/feature/sub/path/file.yaml",
		ghCli,
	)
	require.Error(t, err)

	// Must surface the typed *github.ApiError from the repo probe — not
	// RepoNotAccessibleError (which is reserved for repo-probe 404s) and
	// not the misleading "no valid branch" text.
	apiErr, ok := errors.AsType[*github.ApiError](err)
	require.True(t, ok, "expected *github.ApiError from repo probe, got %T: %v", err, err)
	require.Equal(t, github.KindSAMLBlocked, apiErr.Kind)
	require.NotContains(t, err.Error(), "could not find a valid branch")
	_, isRepoErr := errors.AsType[*RepoNotAccessibleError](err)
	require.False(t, isRepoErr, "SAML on repo probe must not surface as RepoNotAccessibleError")
}
