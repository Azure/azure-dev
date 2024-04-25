// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

func Test_CLI_EnvRefresh_NoBicep(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	infraPath := filepath.Join(dir, "infra")
	require.NoError(t, os.RemoveAll(infraPath))

	envName := randomEnvName() + t.Name()
	res, err := cli.RunCommandWithStdIn(ctx, envName+"\n", "env", "refresh")
	require.Error(t, err)
	// Verify that env refresh will attempt to fetch matching deployments.
	// The deployment isn't expected to be found because none was created.
	require.Contains(t, res.Stdout, provisioning.ErrDeploymentsNotFound.Error())
}
