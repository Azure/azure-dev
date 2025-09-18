// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

func TestGithubCLIDeploymentEnvironments(t *testing.T) {
	t.Run("mock", func(t *testing.T) {
		commandRunner := mockexec.NewMockCommandRunner()

		const mockRepoSlug = "richardpark-msft/copilot-auth-test"
		const mockEnv = "copilot2"

		commandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(args.Cmd, "gh") && len(args.Args) == 1 && args.Args[0] == "--version"
		}).Respond(exec.NewRunResult(
			0,
			fmt.Sprintf("gh version %s (abcdef0123)", Version.String()),
			"",
		))

		commandRunner.When(func(args exec.RunArgs, command string) bool {
			env := ""
			name := ""

			for i := 0; i < len(args.Args); i++ {
				switch {
				case args.Args[i] == "variable" && args.Args[i+1] == "set":
					name = args.Args[i+2]
					require.Equal(t, name, "hello")
				case args.Args[i] == "--env":
					env = args.Args[i+1]
					require.Equal(t, mockEnv, env)
				}
			}

			return env != "" && name != ""
		}).Respond(exec.NewRunResult(0, "", ""))

		commandRunner.When(func(args exec.RunArgs, command string) bool {
			env := ""
			listing := false

			for i := 0; i < len(args.Args); i++ {
				switch {
				case args.Args[i] == "variable" && args.Args[i+1] == "list":
					listing = true
				case args.Args[i] == "--env":
					env = args.Args[i+1]
					require.Equal(t, mockEnv, env)
				}
			}

			return listing && env != ""
		}).Respond(exec.NewRunResult(0, "HELLO\tworld\n", ""))

		addAPIHandler := func(verbAndURL string) *mockexec.CommandExpression {
			called := false

			return commandRunner.When(func(args exec.RunArgs, command string) bool {
				defer func() { called = true }()

				// ex: 'api', 'X', verb, URL
				return !called && verbAndURL == args.Args[2]+" "+args.Args[3]
			})
		}

		testGithubCLIDeploymentEnvironments(t, commandRunner, "richardpark-msft/copilot-auth-tests", "copilot2", addAPIHandler)
	})

	// TODO: how do we handle live testing resources, like a GitHub repo?
	t.Run("live", func(t *testing.T) {
		commandRunner := exec.NewCommandRunner(nil)

		testGithubCLIDeploymentEnvironments(t, commandRunner, "richardpark-msft/copilot-auth-tests", "copilot2", func(verbAndURL string) *mockexec.CommandExpression {
			// (unused, but needed to compile)
			return &mockexec.CommandExpression{}
		})
	})
}

func testGithubCLIDeploymentEnvironments(t *testing.T, commandRunner exec.CommandRunner, repoSlug string, envName string, addAPIHandler func(verbAndURL string) *mockexec.CommandExpression) {
	mockContext := mocks.NewMockContext(context.Background())
	cli, err := NewGitHubCli(context.Background(), mockContext.Console, commandRunner)
	require.NoError(t, err)

	addAPIHandler("PUT /repos/richardpark-msft/copilot-auth-tests/environments/copilot2").Respond(exec.NewRunResult(0, "", ""))
	err = cli.CreateEnvironmentIfNotExist(context.Background(), repoSlug, envName)
	require.NoError(t, err)

	t.Cleanup(func() {
		addAPIHandler("DELETE /repos/richardpark-msft/copilot-auth-tests/environments/copilot2").Respond(exec.NewRunResult(0, "", ""))
		err = cli.DeleteEnvironment(context.Background(), repoSlug, envName)
		require.NoError(t, err)
	})

	addAPIHandler("PATCH /repos/richardpark-msft/copilot-auth-tests/environments/copilot2/variables/hello").SetError(errors.New("this fails"))
	err = cli.SetVariable(context.Background(), repoSlug, "hello", "world", &SetVariableOptions{
		Environment: envName,
	})
	require.NoError(t, err)

	values, err := cli.ListVariables(context.Background(), repoSlug, &ListVariablesOptions{
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

	mockContext := mocks.NewMockContext(context.Background())

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
		_ = osutil.Rename(context.Background(), src, exp)
		return src, nil
	}

	cli, err := newGitHubCliImplementation(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		downloadGh,
		mockExtract,
	)
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

	ver, err := cli.extractVersion(context.Background())
	require.NoError(t, err)
	require.Equal(t, Version.String(), ver)
}

func TestGetAuthStatus(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

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
		_ = osutil.Rename(context.Background(), src, exp)
		return src, nil
	}

	cli, err := newGitHubCliImplementation(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		downloadGh,
		mockExtract,
	)
	require.NoError(t, err)
	require.NotNil(t, cli)

	status, err := cli.GetAuthStatus(*mockContext.Context, "test")
	require.NoError(t, err)
	require.Equal(t, AuthStatus{}, status)
}

func TestNewGitHubCliUpdate(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockContext := mocks.NewMockContext(context.Background())

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
		_ = osutil.Rename(context.Background(), src, exp)
		return src, nil
	}

	cli, err := newGitHubCliImplementation(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		downloadGh,
		mockExtract,
	)
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
