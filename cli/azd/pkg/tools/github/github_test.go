// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestGithubCLIDeploymentEnvironments(t *testing.T) {
	// TODO: how do we handle live testing resources, like a GitHub repo?
	t.Skip("GitHub environment live test disabled. Can be run manually.")

	commandRunner := exec.NewCommandRunner(nil)

	// (you can use any repo here, these were just the ones I used last)
	repoSlug := "richardpark-msft/copilot-auth-tests"
	envName := "copilot2"

	mockContext := mocks.NewMockContext(t.Context())
	cli := NewGitHubCli(mockContext.Console, commandRunner)
	err := cli.EnsureInstalled(t.Context())
	require.NoError(t, err)

	err = cli.CreateEnvironmentIfNotExist(t.Context(), repoSlug, envName)
	require.NoError(t, err)

	t.Cleanup(func() {
		err = cli.DeleteEnvironment(t.Context(), repoSlug, envName)
		require.NoError(t, err)
	})

	err = cli.SetVariable(t.Context(), repoSlug, "hello", "world", &SetVariableOptions{
		Environment: envName,
	})
	require.NoError(t, err)

	values, err := cli.ListVariables(t.Context(), repoSlug, &ListVariablesOptions{
		Environment: envName,
	})
	require.NoError(t, err)
	require.Equal(t, "world", values["HELLO"])
}

func TestZipExtractContents(t *testing.T) {
	testPath := t.TempDir()
	expectedPhrase := "this will be inside a zip file"
	zipFilePath, err := createSampleZip(testPath, expectedPhrase, "bin/"+ghCliName())
	require.NoError(t, err)
	ghCliPath, err := extractGhCli(zipFilePath, testPath)
	require.NoError(t, err)

	content, err := os.ReadFile(ghCliPath)
	require.NoError(t, err)
	require.EqualValues(t, []byte(expectedPhrase), content)
}

func TestRepositoryNameInUse(t *testing.T) {
	require.True(t, repositoryNameInUseRegex.MatchString("GraphQL: Name already exists on this account (createRepository)"))
}

func TestZipGhNotFound(t *testing.T) {
	testPath := t.TempDir()
	expectedPhrase := "this will be inside a zip file"
	zipFilePath, err := createSampleZip(testPath, expectedPhrase, "bin/foo")
	require.NoError(t, err)
	ghCliPath, err := extractGhCli(zipFilePath, testPath)
	require.Error(t, err)
	require.EqualValues(t, "github cli binary was not found within the zip file", err.Error())
	require.EqualValues(t, "", ghCliPath)
}

func TestTarExtractContents(t *testing.T) {
	testPath := t.TempDir()
	expectedPhrase := "this will be inside a tar file"
	tarFilePath, err := createSampleTarGz(testPath, expectedPhrase, "gh")
	require.NoError(t, err)
	ghCliPath, err := extractGhCli(tarFilePath, testPath)
	require.NoError(t, err)

	content, err := os.ReadFile(ghCliPath)
	require.NoError(t, err)
	require.EqualValues(t, []byte(expectedPhrase), content)
}

func TestTarGhNotFound(t *testing.T) {
	testPath := t.TempDir()
	expectedPhrase := "this will be inside a zip file"
	tarFilePath, err := createSampleTarGz(testPath, expectedPhrase, "foo")
	require.NoError(t, err)
	ghCliPath, err := extractGhCli(tarFilePath, testPath)
	require.Error(t, err)
	require.EqualValues(t, "did not find gh cli within tar file", err.Error())
	require.EqualValues(t, "", ghCliPath)
}

func TestUnsupportedFormat(t *testing.T) {
	filePath := "someFile.xyz"
	githubCliPath, err := extractGhCli(filePath, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Unknown format while trying to extract")
	require.EqualValues(t, "", githubCliPath)
}

func TestNewGitHubCli(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockContext := mocks.NewMockContext(t.Context())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "github.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("this is github cli")),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "gh") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf("gh version %s (abcdef0123)", Version.String()),
		"",
	))

	mockExtract := func(src, dst string) (string, error) {
		exp, _ := azdGithubCliPath()
		_ = osutil.Rename(t.Context(), src, exp)
		return src, nil
	}

	cli := newGitHubCliImplementation(
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		downloadGh,
		mockExtract,
	)
	err := cli.EnsureInstalled(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, cli)

	require.Equal(t, 2, len(mockContext.Console.SpinnerOps()))

	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpShow,
		Message: "setting up github connection",
		Format:  input.Step,
	}, mockContext.Console.SpinnerOps()[0])

	gitHubCli, err := azdGithubCliPath()
	require.NoError(t, err)

	contents, err := os.ReadFile(gitHubCli)
	require.NoError(t, err)

	require.Equal(t, []byte("this is github cli"), contents)

	ver, err := cli.extractVersion(t.Context())
	require.NoError(t, err)
	require.Equal(t, Version.String(), ver)
}

func TestGetAuthStatus(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "github.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("this is github cli")),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "gh") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf("gh version %s (abcdef0123)", Version.String()),
		"",
	))

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "gh") && args.Args[0] == "auth" && args.Args[1] == "status"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", notLoggedIntoAnyGitHubHostsMessageRegex.String()), fmt.Errorf("error")
	})

	mockExtract := func(src, dst string) (string, error) {
		exp, _ := azdGithubCliPath()
		_ = osutil.Rename(t.Context(), src, exp)
		return src, nil
	}

	cli := newGitHubCliImplementation(
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		downloadGh,
		mockExtract,
	)
	err := cli.EnsureInstalled(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, cli)

	status, err := cli.GetAuthStatus(*mockContext.Context, "test")
	require.NoError(t, err)
	require.Equal(t, AuthStatus{}, status)
}

func TestNewGitHubCliUpdate(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockContext := mocks.NewMockContext(t.Context())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "github.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("this is github cli")),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "gh") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf("gh version %s (abcdef0123)", semver.MustParse("2.20.0").String()),
		"",
	))

	mockExtract := func(src, dst string) (string, error) {
		exp, _ := azdGithubCliPath()
		_ = osutil.Rename(t.Context(), src, exp)
		return src, nil
	}

	cli := newGitHubCliImplementation(
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		downloadGh,
		mockExtract,
	)
	err := cli.EnsureInstalled(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, cli)

	require.Equal(t, 2, len(mockContext.Console.SpinnerOps()))

	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpShow,
		Message: "setting up github connection",
		Format:  input.Step,
	}, mockContext.Console.SpinnerOps()[0])

	gitHubCli, err := azdGithubCliPath()
	require.NoError(t, err)

	contents, err := os.ReadFile(gitHubCli)
	require.NoError(t, err)

	require.Equal(t, []byte("this is github cli"), contents)
}

func createSampleZip(path, content, file string) (string, error) {
	filePath := filepath.Join(path, "zippedFile.zip")
	zipFile, err := os.Create(filePath)
	if err != nil {
		return "", err
	}
	defer zipFile.Close()

	contentReader := strings.NewReader(content)
	zipWriter := zip.NewWriter(zipFile)

	zipContent, err := zipWriter.Create(file)
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(zipContent, contentReader); err != nil {
		return "", err
	}

	zipWriter.Close()

	return filePath, nil
}

func createSampleTarGz(path, content, file string) (string, error) {
	filePath := filepath.Join(path, "zippedFile.tar.gz")
	tarFile, err := os.Create(filePath)
	if err != nil {
		return "", err
	}
	defer tarFile.Close()

	gzWriter := gzip.NewWriter(tarFile)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// not sure how tar from memory. Let's create an extra file with content
	fileContentPath := filepath.Join(path, file)
	fileContent, err := os.Create(fileContentPath)
	if err != nil {
		return "", err
	}
	if _, err := fileContent.WriteString(content); err != nil {
		return "", err
	}
	fileContent.Close()

	// tar the file
	fileInfo, err := os.Stat(fileContentPath)
	if err != nil {
		return "", err
	}
	tarHeader, err := tar.FileInfoHeader(fileInfo, fileInfo.Name())
	if err != nil {
		return "", err
	}
	if err := tarWriter.WriteHeader(tarHeader); err != nil {
		return "", nil
	}
	fileContent, err = os.Open(fileContentPath)
	defer func() {
		_ = fileContent.Close()
	}()
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tarWriter, fileContent); err != nil {
		return "", err
	}

	return filePath, nil
}

func TestGhOutputToList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "MultipleSecrets",
			input: "SECRET_A\tUpdated 2024-01-01\n" +
				"SECRET_B\tUpdated 2024-01-02\n",
			want: []string{"SECRET_A", "SECRET_B"},
		},
		{
			name:  "EmptyOutput",
			input: "",
			want:  []string{},
		},
		{
			name:  "SingleLine",
			input: "MY_SECRET\tUpdated 2024-01-01\n",
			want:  []string{"MY_SECRET"},
		},
		{
			name:  "NoTabs",
			input: "SECRET_ONLY\n",
			want:  []string{"SECRET_ONLY"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ghOutputToList(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGhOutputToMap(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "MultipleVariables",
			input: "VAR_A\tvalue_a\tUpdated\n" +
				"VAR_B\tvalue_b\tUpdated\n",
			want: map[string]string{
				"VAR_A": "value_a",
				"VAR_B": "value_b",
			},
		},
		{
			name:  "EmptyOutput",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "SingleVariable",
			input: "KEY\tVALUE\n",
			want:  map[string]string{"KEY": "VALUE"},
		},
		{
			name:    "BadFormat",
			input:   "no-tab-here\n",
			wantErr: true,
		},
		{
			name: "MixedValidInvalid",
			input: "VALID\tvalue\n" +
				"invalid_no_tab\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ghOutputToMap(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGhCliVersionRegexp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		matches bool
	}{
		{
			name: "StandardVersion",
			input: "gh version 2.86.0 (2024-01-15)\n" +
				"https://github.com/cli/cli/" +
				"releases/tag/v2.86.0",
			want:    "2.86.0",
			matches: true,
		},
		{
			name:    "OlderVersion",
			input:   "gh version 2.6.0 (2022-03-15)",
			want:    "2.6.0",
			matches: true,
		},
		{
			name:    "NoMatch",
			input:   "some random text",
			matches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := ghCliVersionRegexp.FindStringSubmatch(
				tt.input,
			)
			if !tt.matches {
				require.Len(t, matches, 0)
				return
			}
			require.Len(t, matches, 2)
			require.Equal(t, tt.want, matches[1])
		})
	}
}

func TestIsGhCliNotLoggedInMessageRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "AuthenticatePlease",
			input: "To authenticate, please run " +
				"`gh auth login`.",
			want: true,
		},
		{
			name: "TryAuthenticating",
			input: "Try authenticating with: " +
				" gh auth login",
			want: true,
		},
		{
			name: "ReAuthenticate",
			input: "To re-authenticate, run: " +
				"gh auth login",
			want: true,
		},
		{
			name: "GetStarted",
			input: "To get started with GitHub CLI, " +
				"please run:  gh auth login",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "everything is fine",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGhCliNotLoggedInMessageRegex.MatchString(
				tt.input,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsUserNotAuthorizedMessageRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "Matching",
			input: "HTTP 403: Resource not " +
				"accessible by integration",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "HTTP 200: OK",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUserNotAuthorizedMessageRegex.MatchString(
				tt.input,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNotLoggedIntoAnyGitHubHostsRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "Matching",
			input: "You are not logged into any " +
				"GitHub hosts.",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "Logged in to github.com",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := notLoggedIntoAnyGitHubHostsMessageRegex.
				MatchString(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRepositoryNameInUseRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "Matching",
			input: "GraphQL: Name already exists on " +
				"this account (createRepository)",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "repository created successfully",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repositoryNameInUseRegex.MatchString(
				tt.input,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRunningOnCodespaces(t *testing.T) {
	t.Run("InCodespaces", func(t *testing.T) {
		t.Setenv("CODESPACES", "true")
		require.True(t, RunningOnCodespaces())
	})

	t.Run("NotInCodespaces", func(t *testing.T) {
		t.Setenv("CODESPACES", "false")
		require.False(t, RunningOnCodespaces())
	})

	t.Run("EnvNotSet", func(t *testing.T) {
		t.Setenv("CODESPACES", "")
		require.False(t, RunningOnCodespaces())
	})
}

func TestCliName(t *testing.T) {
	cli := &Cli{}
	require.Equal(t, "GitHub CLI", cli.Name())
}

func TestCliInstallUrl(t *testing.T) {
	cli := &Cli{}
	require.Equal(
		t,
		"https://aka.ms/azure-dev/github-cli-install",
		cli.InstallUrl(),
	)
}

func TestCliBinaryPath(t *testing.T) {
	cli := &Cli{path: "/usr/local/bin/gh"}
	require.Equal(
		t, "/usr/local/bin/gh", cli.BinaryPath(),
	)
}

func TestCliBinaryPathEmpty(t *testing.T) {
	cli := &Cli{}
	require.Equal(t, "", cli.BinaryPath())
}

func TestProtocolTypeConstants(t *testing.T) {
	require.Equal(t, "ssh", GitSshProtocolType)
	require.Equal(t, "https", GitHttpsProtocolType)
}

func TestGhCliName(t *testing.T) {
	name := ghCliName()
	require.NotEmpty(t, name)
	// On all platforms, it should either be "gh" or "gh.exe"
	require.Contains(t, name, "gh")
}

func TestGitHubHostName(t *testing.T) {
	require.Equal(t, "github.com", GitHubHostName)
}

func TestTokenEnvVars(t *testing.T) {
	require.Contains(t, TokenEnvVars, "GITHUB_TOKEN")
	require.Contains(t, TokenEnvVars, "GH_TOKEN")
	require.Len(t, TokenEnvVars, 2)
}
