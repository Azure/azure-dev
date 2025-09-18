// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

func Test_CLI_ShowWorksWithoutEnvironment(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Remove information about the just created enviroment to simulate the case where
	// there's some issue with the environment and we can't load it.
	err = os.RemoveAll(filepath.Join(dir, ".azure", envName))
	require.NoError(t, err)

	result, err := cli.RunCommand(ctx, "show", "--output", "json")
	require.NoError(t, err)

	var showRes struct {
		Name     string `json:"name"`
		Services map[string]*struct {
			Project struct {
				Path     string `json:"path"`
				Language string `json:"language"`
			} `json:"project"`
			Target *struct {
				ResourceIds []string `json:"resourceIds"`
			} `json:"target"`
		} `json:"services"`
	}

	err = json.Unmarshal([]byte(result.Stdout), &showRes)
	require.NoError(t, err)

	require.Equal(t, "webapp", showRes.Name)
	require.Equal(t, 1, len(showRes.Services))
	require.NotNil(t, showRes.Services["web"])
	require.Nil(t, showRes.Services["web"].Target)

	// Repeat the process but passing an explicit environment name for an environment that doesn't exist and ensure
	// that we get an error, as the selected env does not exists.
	_, err = cli.RunCommand(ctx, "show", "-e", "does-not-exist-by-design", "--output", "json")
	require.Error(t, err)
}
