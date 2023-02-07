package github

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

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
		fmt.Sprintf("gh version %s (abcdef0123)", cGitHubCliVersion.String()),
		"",
	))

	mockExtract := func(src, dst string) (string, error) {
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
		Message: "Downloading Github cli",
		Format:  input.Step,
	}, mockContext.Console.SpinnerOps()[0])

	gitHubCli, err := azdGithubCliPath()
	require.NoError(t, err)

	contents, err := os.ReadFile(gitHubCli)
	require.NoError(t, err)

	require.Equal(t, []byte("this is github cli"), contents)
}
