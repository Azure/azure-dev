// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

func Test_WorkflowService_NestedInit_Offline(t *testing.T) {
	for _, tt := range []struct {
		name      string
		target    string
		parentCwd bool
	}{
		{name: "current directory", target: "."},
		{name: "child directory", target: "agent"},
		{name: "child directory with parent cwd", target: "agent", parentCwd: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testWorkflowNestedInit(t, tt.target, tt.parentCwd)
		})
	}
}

func testWorkflowNestedInit(t *testing.T, target string, parentCwd bool) {
	t.Helper()
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("NO_COLOR", "1")
	parent := t.TempDir()
	t.Chdir(parent)
	opts := &internal.GlobalCommandOptions{NoPrompt: true}
	originalArgs := os.Args
	os.Args = []string{"azd", "ai", "agent", "init"}
	t.Cleanup(func() { os.Args = originalArgs })
	if parentCwd {
		opts.Cwd = parent
		os.Args = append(os.Args, "--cwd", parent)
	}

	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, t.Context())
	ioc.RegisterInstance(container, opts)

	rootCmd := NewRootCmd(false, nil, container)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	scope, err := container.NewScope()
	require.NoError(t, err)
	ioc.RegisterInstance(scope, rootCmd)
	ioc.RegisterInstance(scope, scope)
	ioc.RegisterInstance(scope, []string{})

	var server *grpcserver.Server
	require.NoError(t, scope.Resolve(&server))

	serverInfo, err := server.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, server.Stop())
	})

	token, err := grpcserver.GenerateExtensionToken(&extensions.Extension{
		Id: "azd.internal.test", Namespace: "test",
	}, serverInfo)
	require.NoError(t, err)

	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	ctx := azdext.WithAccessToken(t.Context(), token)
	_, err = client.Project().Get(ctx, &azdext.EmptyRequest{})
	require.Error(t, err)

	_, err = client.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: &azdext.Workflow{
			Name: "init",
			Steps: []*azdext.WorkflowStep{{Command: &azdext.WorkflowCommand{
				Args: []string{
					"init", "-t", t.TempDir(), target, "--environment", "repro", "--no-prompt",
				}}}},
		},
	})

	require.NoError(t, err)
	projectDir := filepath.Join(parent, target)
	currentDir, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, projectDir, currentDir, "nested init must leave the host in the new project directory")
	_, err = os.Stat(filepath.Join(projectDir, ".azure", "repro", ".env"))
	require.NoError(t, err)
	response, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, projectDir, response.Project.Path)
	if target != "." {
		require.NoFileExists(t, filepath.Join(parent, "azure.yaml"))
	}
}
