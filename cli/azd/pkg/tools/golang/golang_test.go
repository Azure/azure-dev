// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package golang

import (
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestCheckInstalled_Success(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.CommandRunner.MockToolInPath("go", nil)
	mockCtx.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "go" && len(args.Args) > 0 && args.Args[0] == "version"
	}).Respond(exec.RunResult{
		Stdout: "go version go1.24.3 linux/amd64",
	})

	cli := NewCli(mockCtx.CommandRunner)
	err := cli.CheckInstalled(*mockCtx.Context)
	require.NoError(t, err)
}

func TestCheckInstalled_NotInPath(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.CommandRunner.MockToolInPath("go", fmt.Errorf("go not found in PATH"))

	cli := NewCli(mockCtx.CommandRunner)
	err := cli.CheckInstalled(*mockCtx.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestCheckInstalled_VersionTooLow(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.CommandRunner.MockToolInPath("go", nil)
	mockCtx.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "go" && len(args.Args) > 0 && args.Args[0] == "version"
	}).Respond(exec.RunResult{
		Stdout: "go version go1.20.0 linux/amd64",
	})

	cli := NewCli(mockCtx.CommandRunner)
	err := cli.CheckInstalled(*mockCtx.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "version")
}

func TestName(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	cli := NewCli(mockCtx.CommandRunner)
	require.Equal(t, "Go CLI", cli.Name())
}

func TestInstallUrl(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	cli := NewCli(mockCtx.CommandRunner)
	require.Equal(t, "https://go.dev/dl/", cli.InstallUrl())
}

func TestBuild(t *testing.T) {
	var capturedArgs exec.RunArgs

	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "go build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args
		return exec.NewRunResult(0, "", ""), nil
	})

	cli := NewCli(mockCtx.CommandRunner)
	env := []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"}
	err := cli.Build(*mockCtx.Context, "/project", "/out/app", env)

	require.NoError(t, err)
	require.Equal(t, "go", capturedArgs.Cmd)
	require.Equal(t, []string{"build", "-o", "/out/app", "."}, capturedArgs.Args)
	require.Equal(t, "/project", capturedArgs.Cwd)
	require.Equal(t, env, capturedArgs.Env)
}

func TestModDownload(t *testing.T) {
	var capturedArgs exec.RunArgs

	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "go mod download")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args
		return exec.NewRunResult(0, "", ""), nil
	})

	cli := NewCli(mockCtx.CommandRunner)
	env := []string{"GOPROXY=direct"}
	err := cli.ModDownload(*mockCtx.Context, "/project", env)

	require.NoError(t, err)
	require.Equal(t, "go", capturedArgs.Cmd)
	require.Equal(t, []string{"mod", "download"}, capturedArgs.Args)
	require.Equal(t, "/project", capturedArgs.Cwd)
	require.Equal(t, env, capturedArgs.Env)
}
