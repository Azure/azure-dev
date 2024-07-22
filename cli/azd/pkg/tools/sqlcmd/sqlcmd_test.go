// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package sqlcmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
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
	"github.com/dsnet/compress/bzip2"

	"github.com/stretchr/testify/require"
)

func TestNewSqlCmdHubCli(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "github.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("this is sqlcmd cli")),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "sqlcmd") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf("%s", sqlCmdCliVersion.String()),
		"",
	))

	mockExtract := func(src, dst string) (string, error) {
		exp, _ := azdSqlCmdCliPath()
		_ = osutil.Rename(context.Background(), src, exp)
		return src, nil
	}

	cli, err := newSqlCmdCliImplementation(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		downloadSqlCmd,
		mockExtract,
	)
	require.NoError(t, err)
	require.NotNil(t, cli)

	require.Equal(t, 2, len(mockContext.Console.SpinnerOps()))

	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpShow,
		Message: "setting up sqlCmd connection",
		Format:  input.Step,
	}, mockContext.Console.SpinnerOps()[0])

	sqlCmdCli, err := azdSqlCmdCliPath()
	require.NoError(t, err)

	contents, err := os.ReadFile(sqlCmdCli)
	require.NoError(t, err)

	require.Equal(t, []byte("this is sqlcmd cli"), contents)

	ver, err := cli.extractVersion(context.Background())
	require.NoError(t, err)
	require.Equal(t, sqlCmdCliVersion.String(), ver)
}

func TestZipExtractContents(t *testing.T) {
	testPath := t.TempDir()
	expectedPhrase := "this will be inside a zip file"
	zipFilePath, err := createSampleZip(testPath, expectedPhrase, "bin/"+sqlCmdCliName())
	require.NoError(t, err)
	ghCliPath, err := extractSqlCmdCli(zipFilePath, testPath)
	require.NoError(t, err)

	content, err := os.ReadFile(ghCliPath)
	require.NoError(t, err)
	require.EqualValues(t, []byte(expectedPhrase), content)
}

func TestTarExtractContents(t *testing.T) {
	testPath := t.TempDir()
	expectedPhrase := "this will be inside a tar file"
	tarFilePath, err := createSampleTarBz2(testPath, expectedPhrase, "sqlcmd")
	require.NoError(t, err)
	ghCliPath, err := extractSqlCmdCli(tarFilePath, testPath)
	require.NoError(t, err)

	content, err := os.ReadFile(ghCliPath)
	require.NoError(t, err)
	require.EqualValues(t, []byte(expectedPhrase), content)
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

func createSampleTarBz2(path, content, file string) (string, error) {
	filePath := filepath.Join(path, "zippedFile.tar.bz2")
	tarFile, err := os.Create(filePath)
	if err != nil {
		return "", err
	}
	defer tarFile.Close()

	gzWriter, err := bzip2.NewWriter(tarFile, nil)
	if err != nil {
		return "", err
	}
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
