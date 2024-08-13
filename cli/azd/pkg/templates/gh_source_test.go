package templates

import (
	"context"
	"encoding/json"
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

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
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

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
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

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	source, err := newGhTemplateSource(
		*mockContext.Context, name, "https://github.com/owner/repo/branch/path/to/the/folder/file.json", ghCli,
		mockContext.Console)
	require.Nil(t, err)
	require.Equal(t, name, source.Name())
}
