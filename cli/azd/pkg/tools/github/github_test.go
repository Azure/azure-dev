package github

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
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
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

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
		fmt.Sprintf("gh version %s (abcdef0123)", GitHubCliVersion.String()),
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

	ver, err := cli.(*ghCli).extractVersion(context.Background())
	require.NoError(t, err)
	require.Equal(t, GitHubCliVersion.String(), ver)
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
