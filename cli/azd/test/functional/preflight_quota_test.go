// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

// Test_CLI_PreflightQuota_RG_DefaultCapacity verifies that the ai_model_quota preflight
// check fires a quota warning for RG-scoped deployments when capacity is absurdly high.
func Test_CLI_PreflightQuota_RG_DefaultCapacity(t *testing.T) {
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

	err := copySample(dir, "ai-quota/rg-deployment")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Provision with default params (capacity=99999) — expect quota warning, answer No.
	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForRGProvisionWithPreflightNo(),
		"provision",
	)
	require.NoError(t, err)
	// The user declined the warning, so azd should abort (exit 0 or specific error).
	// Check that the output contains the quota warning.
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "insufficient quota",
		"expected quota exceeded warning in output")
}

// Test_CLI_PreflightQuota_RG_InvalidModelName verifies a warning when the model name
// doesn't exist in the Azure AI catalog.
func Test_CLI_PreflightQuota_RG_InvalidModelName(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")
	cli.Env = append(cli.Env, "GPT_MODEL_NAME=gpt-nonexistent-model")
	cli.Env = append(cli.Env, "GPT_DEPLOYMENT_CAPACITY=10")

	err := copySample(dir, "ai-quota/rg-deployment")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForRGProvisionWithPreflightNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "was not found in the AI model catalog",
		"expected model-not-found warning for invalid model name")
	require.Contains(t, output, "gpt-nonexistent-model")
}

// Test_CLI_PreflightQuota_RG_InvalidVersion verifies a warning when the model version
// is not available in the catalog.
func Test_CLI_PreflightQuota_RG_InvalidVersion(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")
	cli.Env = append(cli.Env, "GPT_MODEL_VERSION=9999-99-99")
	cli.Env = append(cli.Env, "GPT_DEPLOYMENT_CAPACITY=10")

	err := copySample(dir, "ai-quota/rg-deployment")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForRGProvisionWithPreflightNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "was not found in the AI model catalog",
		"expected model-not-found warning for invalid version")
}

// Test_CLI_PreflightQuota_Sub_DefaultCapacity verifies the quota check for
// subscription-scoped deployments.
func Test_CLI_PreflightQuota_Sub_DefaultCapacity(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	err := copySample(dir, "ai-quota/sub-deployment")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForProvisionWithPreflightNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "insufficient quota",
		"expected quota exceeded warning in output")
}

// Test_CLI_PreflightQuota_Sub_InvalidModelName verifies model-not-found for
// subscription-scoped deployments with a bad model name.
func Test_CLI_PreflightQuota_Sub_InvalidModelName(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")
	cli.Env = append(cli.Env, "GPT_MODEL_NAME=gpt-555-turbo")
	cli.Env = append(cli.Env, "GPT_DEPLOYMENT_CAPACITY=10")

	err := copySample(dir, "ai-quota/sub-deployment")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForProvisionWithPreflightNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "was not found in the AI model catalog",
		"expected model-not-found warning for invalid model name")
	require.Contains(t, output, "gpt-555-turbo")
}

// Test_CLI_PreflightQuota_Sub_DifferentLocation verifies quota checking against
// a different location than the primary deployment location.
func Test_CLI_PreflightQuota_Sub_DifferentLocation(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")
	// Deploy AI resources to a different location
	cli.Env = append(cli.Env, "AI_DEPLOYMENTS_LOCATION=swedencentral")

	err := copySample(dir, "ai-quota/sub-deployment")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForProvisionWithPreflightNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	// Should check quota in swedencentral, not eastus2
	require.Contains(t, output, "swedencentral",
		"expected quota check against the override location")
}

// stdinForProvisionWithPreflightNo provides stdin for subscription-scoped provision that:
// 1. Accepts default subscription
// 2. Accepts default location
// 3. Answers "No" to the preflight warning prompt
func stdinForProvisionWithPreflightNo() string {
	return strings.Join([]string{
		"",  // choose subscription (default)
		"",  // choose location (default)
		"n", // decline preflight warning
	}, "\n")
}

// stdinForRGProvisionWithPreflightNo provides stdin for resource-group-scoped provision:
// 1. Accepts default subscription
// 2. Accepts default resource group (create new)
// 3. Accepts default resource group name
// 4. Answers "No" to the preflight warning prompt
func stdinForRGProvisionWithPreflightNo() string {
	return strings.Join([]string{
		"",  // choose subscription (default)
		"",  // choose resource group (default = create new)
		"",  // accept default resource group name
		"n", // decline preflight warning
	}, "\n")
}
