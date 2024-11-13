// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

// Validates that env refresh works even without bicep defined.
func Test_CLI_EnvRefresh_NoBicep(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)

	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	// Remove .azure and infra
	environment := filepath.Join(dir, azdcontext.EnvironmentDirectoryName)
	require.NoError(t, os.RemoveAll(environment))

	infraPath := filepath.Join(dir, "infra")
	infraPathHidden := filepath.Join(dir, "infra-delete")
	require.NoError(t, os.Rename(infraPath, infraPathHidden))

	// Reuse same environment name
	_, err = cli.RunCommandWithStdIn(ctx, envName+"\n", "env", "refresh")
	require.NoError(t, err)

	env, err := envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)

	// Env refresh should populate these values
	assertEnvValuesStored(t, env)

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
	}

	// restore infra path for deletion
	require.NoError(t, os.Rename(infraPathHidden, infraPath))

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}
