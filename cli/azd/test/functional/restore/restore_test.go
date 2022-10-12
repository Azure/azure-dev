// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package restore_test

import (
	"path"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/functional"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_CLI_RestoreCommand(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "eastus2")

	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	err := functional.CopySample(dir, "restoreapp")
	require.NoError(t, err, "failed expanding sample")

	_, initResp := functional.NewRandomNameEnvAndInitResponse(t)

	_, _, err = functional.RunCliCommandWithStdIn(t, ctx, initResp, "init")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "restore")
	require.NoError(t, err)

	require.DirExists(t, path.Join(dir, "nodeapp", "node_modules", "chalk"), "nodeapp not restored")
	require.DirExists(t, path.Join(dir, "containerapp", "node_modules", "chalk"), "containerapp not restored")
	require.DirExists(t, path.Join(dir, "pyapp", "pyapp_env"), "pyapp not restored")
	require.DirExists(t, path.Join(dir, "csharpapp", "obj"), "csharpapp not restored")
	require.DirExists(t, path.Join(dir, "funcapp", "funcapp_env"), "funcapp not restored")
}
