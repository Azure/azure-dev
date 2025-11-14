// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/pkg/osutil"
	"github.com/azure/azure-dev/test/azdcli"
	"github.com/azure/azure-dev/test/recording"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

func Test_CLI_Publish_ContainerApp_RemoteBuild(t *testing.T) {
	t.Skip("taking 53 minutes. Needs to be re-designed - https://github.com/Azure/azure-dev/issues/6059")
	t.Parallel()

	tests := []struct {
		name              string
		publishArgs       []string
		expectedImageName string
		expectedImageTag  string
	}{
		{
			name:              "default publish",
			publishArgs:       []string{"publish"},
			expectedImageName: "foo/bar",
			expectedImageTag:  "latest",
		},
		{
			name:              "custom image and tag",
			publishArgs:       []string{"publish", "--to", "custom/image:prod"},
			expectedImageName: "custom/image",
			expectedImageTag:  "prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			err := copySample(dir, "containercustomdockerapp")
			require.NoError(t, err, "failed expanding sample")

			_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
			require.NoError(t, err)

			_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
			require.NoError(t, err)

			// Read the environment to get ACR endpoint
			env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
			require.NoError(t, err)

			acrEndpoint, has := env["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
			require.True(t, has, "AZURE_CONTAINER_REGISTRY_ENDPOINT should be in environment after provision")

			// Build publish command arguments with --cwd
			publishArgs := append(tt.publishArgs, "--cwd", filepath.Join(dir, "src", "app"))

			_, err = cli.RunCommand(ctx, publishArgs...)
			require.NoError(t, err)

			// Re-read environment to get updated SERVICE_APP_IMAGE_NAME
			env, err = godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
			require.NoError(t, err)

			image, has := env["SERVICE_APP_IMAGE_NAME"]
			require.True(t, has, "SERVICE_APP_IMAGE_NAME should be in environment after publish")

			expectedImage := acrEndpoint + "/" + tt.expectedImageName + ":" + tt.expectedImageTag
			require.Equal(t, expectedImage, image, "image name should match expected pattern")

			_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
			require.NoError(t, err)
		})
	}
}

// test for errors when running publish in invalid working directories
func Test_CLI_Publish_Err_WorkingDirectory(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit("testenv"), "init")
	require.NoError(t, err)

	// Otherwise, publish with 'infrastructure has not been provisioned. Run `azd provision`'
	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_SUBSCRIPTION_ID", cfg.SubscriptionID)
	require.NoError(t, err)

	// cd infra
	err = os.MkdirAll(filepath.Join(dir, "infra"), osutil.PermissionDirectory)
	require.NoError(t, err)
	cli.WorkingDirectory = filepath.Join(dir, "infra")

	result, err := cli.RunCommand(ctx, "publish")
	require.Error(t, err, "publish should fail in non-project and non-service directory")
	require.Contains(t, result.Stdout, "current working directory")
}

// test for azd publish with invalid flag options
func Test_CLI_PublishInvalidFlags(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Otherwise, publish with 'infrastructure has not been provisioned. Run `azd provision`'
	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_SUBSCRIPTION_ID", cfg.SubscriptionID)
	require.NoError(t, err)

	// invalid service name
	res, err := cli.RunCommand(ctx, "publish", "badServiceName")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "badServiceName")

	// --to with --all
	res, err = cli.RunCommand(ctx, "publish", "--all", "--to", "custom-image:tag")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--to")
	require.Contains(t, res.Stdout, "--all")

	// --from-package with --all
	res, err = cli.RunCommand(ctx, "publish", "--all", "--from-package", "output")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--all")
	require.Contains(t, res.Stdout, "--from-package")

	// --to without specific service (publishing all services)
	res, err = cli.RunCommand(ctx, "publish", "--to", "custom-image:tag")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--to")

	// --from-package without specific service (publishing all services)
	res, err = cli.RunCommand(ctx, "publish", "--from-package", "output")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--from-package")
}

// test for azd publish without provisioned infrastructure
func Test_CLI_Publish_Without_Provision(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Try to publish without setting subscription (no infrastructure provisioned)
	result, err := cli.RunCommand(ctx, "publish")
	require.Error(t, err)
	require.Contains(t, result.Stdout, "infrastructure has not been provisioned")
	require.Contains(t, result.Stdout, "azd provision")
}

// test for azd publish with non-container app services
func Test_CLI_Publish_Unsupported_Service(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	// Use a sample that might have non-container app services
	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_SUBSCRIPTION_ID", cfg.SubscriptionID)
	require.NoError(t, err)

	result, err := cli.RunCommand(ctx, "publish", "--all")
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "WARNING: 'publish' only supports")
}
