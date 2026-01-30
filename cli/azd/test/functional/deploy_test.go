// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/require"
)

// test for errors when running deploy in invalid working directories
func Test_CLI_Deploy_Err_WorkingDirectory(t *testing.T) {
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

	// Otherwise, deploy with 'infrastructure has not been provisioned. Run `azd provision`'
	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_SUBSCRIPTION_ID", cfg.SubscriptionID)
	require.NoError(t, err)

	// cd infra
	err = os.MkdirAll(filepath.Join(dir, "infra"), osutil.PermissionDirectory)
	require.NoError(t, err)
	cli.WorkingDirectory = filepath.Join(dir, "infra")

	result, err := cli.RunCommand(ctx, "deploy")
	require.Error(t, err, "deploy should fail in non-project and non-service directory")
	require.Contains(t, result.Stdout, "current working directory")
}

// test for azd deploy with invalid flag options
func Test_CLI_DeployInvalidFlags(t *testing.T) {
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

	// Otherwise, deploy with 'infrastructure has not been provisioned. Run `azd provision`'
	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_SUBSCRIPTION_ID", cfg.SubscriptionID)
	require.NoError(t, err)

	// invalid service name
	res, err := cli.RunCommand(ctx, "deploy", "badServiceName")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "badServiceName")

	// --service with --all
	res, err = cli.RunCommand(ctx, "deploy", "web", "--all")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--all")
	require.Contains(t, res.Stdout, "<service>")

	// --from-package with --all
	res, err = cli.RunCommand(ctx, "deploy", "--all", "--from-package", "output")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--all")
	require.Contains(t, res.Stdout, "--from-package")
}

// Test_CLI_Deploy_SlotDeployment tests the deployment slot feature where:
// - First deployment (via `azd up`) deploys to both main app and slot
// - Subsequent deployments (via `azd deploy`) deploy only to the slot when using env var
// - The main app retains the original version while slot gets the update
func Test_CLI_Deploy_SlotDeployment(t *testing.T) {
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

	// Defer cleanup to delete resource group regardless of test outcome
	// The resource group name follows the pattern: rg-{envName}
	t.Cleanup(func() {
		cleanupRg(context.Background(), t, cli, session, "rg-"+envName)
	})

	err := copySample(dir, "webapp-slots")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Run azd up - this will provision and deploy to both main app and slot
	t.Logf("Running azd up (provision + initial deploy)\n")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "up")
	require.NoError(t, err)

	// Get the deployed URLs from environment
	result, err := cli.RunCommand(ctx, "env", "get-values", "-o", "json", "--cwd", dir)
	require.NoError(t, err)

	var envValues map[string]interface{}
	err = json.Unmarshal([]byte(result.Stdout), &envValues)
	require.NoError(t, err)

	websiteURL := envValues["WEBSITE_URL"].(string)
	slotURL := envValues["SLOT_URL"].(string)
	t.Logf("Main app URL: %s", websiteURL)
	t.Logf("Slot URL: %s", slotURL)

	// Define expected responses
	originalResponse := `{
  "message": "Hello from Azure App Service!",
  "version": "1.0.0"
}
`
	updatedResponse := `{
  "message": "Updated deployment!",
  "version": "2.0.0"
}
`

	httpClient := http.DefaultClient
	if session != nil {
		httpClient = session.ProxyClient
	}

	// Create service health prober with session-aware retry delay
	prober := newServiceHealthProber(httpClient, session)

	// Verify both main app and slot return the original response after first deployment
	t.Logf("Verifying main app returns original response\n")
	err = prober.probe(t, ctx, websiteURL, originalResponse)
	require.NoError(t, err, "main app should return original response after azd up")

	t.Logf("Verifying slot returns original response\n")
	err = prober.probe(t, ctx, slotURL, originalResponse)
	require.NoError(t, err, "slot should return original response after azd up")

	// Update the data.json file with new content
	t.Logf("Updating data.json with new content\n")
	dataJSONPath := filepath.Join(dir, "src", "data.json")
	err = os.WriteFile(dataJSONPath, []byte(updatedResponse), osutil.PermissionFile)
	require.NoError(t, err, "failed to update data.json")

	// Run azd deploy with the slot environment variable set
	// This should deploy only to the staging slot
	t.Logf("Running azd deploy with AZD_DEPLOY_API_SLOT_NAME=staging\n")
	cli.Env = append(cli.Env, "AZD_DEPLOY_API_SLOT_NAME=staging")
	_, err = cli.RunCommand(ctx, "deploy", "--cwd", dir)
	require.NoError(t, err)

	// Verify main app still returns original response (unchanged)
	t.Logf("Verifying main app still returns original response after slot deploy\n")
	err = prober.probe(t, ctx, websiteURL, originalResponse)
	require.NoError(t, err, "main app should still return original response after slot deploy")

	// Verify slot returns updated response
	t.Logf("Verifying slot returns updated response after slot deploy\n")
	err = prober.probe(t, ctx, slotURL, updatedResponse)
	require.NoError(t, err, "slot should return updated response after slot deploy")

	t.Logf("Done\n")
}

// serviceHealthProber probes service endpoints for expected responses with configurable retry delays.
type serviceHealthProber struct {
	client     *http.Client
	retryDelay time.Duration
}

// newServiceHealthProber creates a new service health prober.
// The retry delay is set based on the session's playback mode:
// - 1ms in playback mode for fast test execution
// - 5 seconds in live/record modes for actual service health checks
func newServiceHealthProber(
	client *http.Client,
	session *recording.Session,
) *serviceHealthProber {
	retryDelay := 10 * time.Second
	if session != nil && session.Playback {
		retryDelay = 1 * time.Millisecond
	}

	return &serviceHealthProber{
		client:     client,
		retryDelay: retryDelay,
	}
}

// probe verifies that an endpoint returns the expected JSON response.
// It retries up to 60 times with the configured retry delay.
func (p *serviceHealthProber) probe(t *testing.T, ctx context.Context, url string, expectedBody string) error {
	return retry.Do(ctx, retry.WithMaxRetries(120, retry.NewConstant(p.retryDelay)), func(ctx context.Context) error {
		t.Logf("Attempting to GET URL: %s", url)

		/* #nosec G107 - Potential HTTP request made with variable url false positive */
		res, err := p.client.Get(url)
		if err != nil {
			return retry.RetryableError(err)
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return retry.RetryableError(
				fmt.Errorf("expected status 200 but got %d for request to %s", res.StatusCode, url),
			)
		}

		var buf bytes.Buffer
		_, err = buf.ReadFrom(res.Body)
		if err != nil {
			return retry.RetryableError(err)
		}

		bodyString := buf.String()
		if bodyString != expectedBody {
			return retry.RetryableError(
				fmt.Errorf("expected %q but got %q for request to %s", expectedBody, bodyString, url),
			)
		}

		return nil
	})
}
