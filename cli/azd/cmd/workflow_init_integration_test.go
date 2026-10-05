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
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("NO_COLOR", "1")
	t.Chdir(t.TempDir())

	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, t.Context())
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{NoPrompt: true})

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
					"init", "-t", t.TempDir(), ".", "--environment", "repro", "--no-prompt",
				}}}},
		},
	})

	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(".azure", "repro", ".env"))
	require.NoError(t, err)
}
