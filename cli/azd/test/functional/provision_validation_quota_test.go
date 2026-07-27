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

// Test_CLI_ProvisionValidationQuota_RG_DefaultCapacity verifies that the ai_model_quota validation
// check fires a quota warning for RG-scoped deployments when capacity is absurdly high.
func Test_CLI_ProvisionValidationQuota_RG_DefaultCapacity(t *testing.T) {
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

	// Persist AZURE_LOCATION to the azd environment so the validation check
	// can resolve it for RG-scoped deployments where the RG doesn't exist yet.
	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_LOCATION", "eastus2")
	require.NoError(t, err)

	// Provision with default params (capacity=99999) — expect quota warning, answer No.
	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForRGProvisionWithValidationNo(),
		"provision",
	)
	require.NoError(t, err)
	// The user declined the warning, so azd should stop before provisioning.
	// In this flow, declining the validation warning is expected to return successfully,
	// and the output should contain the quota warning.
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "Insufficient quota",
		"expected quota exceeded warning in output")
	require.Contains(t, output, "Suggestion:",
		"expected actionable suggestion in output")
}

// Test_CLI_ProvisionValidationQuota_RG_InvalidModelName verifies a warning when the model name
// doesn't exist in the Azure AI catalog.
func Test_CLI_ProvisionValidationQuota_RG_InvalidModelName(t *testing.T) {
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

	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_LOCATION", "eastus2")
	require.NoError(t, err)

	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForRGProvisionWithValidationNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "not found in AI model catalog",
		"expected model-not-found warning for invalid model name")
	require.Contains(t, output, "gpt-nonexistent-model")
}

// Test_CLI_ProvisionValidationQuota_RG_InvalidVersion verifies a warning when the model version
// is not available in the catalog.
func Test_CLI_ProvisionValidationQuota_RG_InvalidVersion(t *testing.T) {
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

	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_LOCATION", "eastus2")
	require.NoError(t, err)

	result, err := cli.RunCommandWithStdIn(
		ctx,
		stdinForRGProvisionWithValidationNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "not found in AI model catalog",
		"expected model-not-found warning for invalid version")
}

// Test_CLI_ProvisionValidationQuota_Sub_DefaultCapacity verifies the quota check for
// subscription-scoped deployments.
func Test_CLI_ProvisionValidationQuota_Sub_DefaultCapacity(t *testing.T) {
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
		stdinForProvisionWithValidationNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "Insufficient quota",
		"expected quota exceeded warning in output")
}

// Test_CLI_ProvisionValidationQuota_Sub_InvalidModelName verifies model-not-found for
// subscription-scoped deployments with a bad model name.
func Test_CLI_ProvisionValidationQuota_Sub_InvalidModelName(t *testing.T) {
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
		stdinForProvisionWithValidationNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	require.Contains(t, output, "not found in AI model catalog",
		"expected model-not-found warning for invalid model name")
	require.Contains(t, output, "gpt-555-turbo")
}

// Test_CLI_ProvisionValidationQuota_Sub_DifferentLocation verifies quota checking against
// a different location than the primary deployment location.
func Test_CLI_ProvisionValidationQuota_Sub_DifferentLocation(t *testing.T) {
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
		stdinForProvisionWithValidationNo(),
		"provision",
	)
	require.NoError(t, err)
	output := result.Stdout + result.Stderr
	// Should check quota in swedencentral, not eastus2
	require.Contains(t, output, "swedencentral",
		"expected quota check against the override location")
}

// stdinForProvisionWithValidationNo provides stdin for subscription-scoped provision.
//
// Subscription and location are resolved from the environment (AZURE_SUBSCRIPTION_ID /
// AZURE_LOCATION), so no interactive subscription/location prompt fires. The only prompt
// is the validation warning confirm ("Proceed with deployment despite the warnings above?"),
// which we answer "No" to. A blank line would now be interpreted as the confirm's default
// (Yes) since azd honors the prompt default at EOF/blank input, so we must answer explicitly.
func stdinForProvisionWithValidationNo() string {
	return "n" // decline the validation warning
}

// stdinForRGProvisionWithValidationNo provides stdin for resource-group-scoped provision.
//
// For RG-scoped provision, azd prompts to pick a resource group and to name a new one
// before validating the deployment; both are answered with a blank line to accept the
// defaults ("Create a new resource group" and the default name). The final prompt is the
// validation warning confirm ("Proceed with deployment despite the warnings above?"), which
// we answer "No" to. The answer must be explicit ("n") rather than a blank line, because
// azd now honors the confirm's default (Yes) at EOF/blank input.
func stdinForRGProvisionWithValidationNo() string {
	return strings.Join([]string{
		"",  // pick resource group (default = create new)
		"",  // accept default new resource group name
		"n", // decline the validation warning
	}, "\n")
}
