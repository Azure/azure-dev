// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/require"
	"path/filepath"
)

// Test_CLI_Up_Down_ContainerApp_RemoteBuild_MultiService regression-tests the cross-service image contamination
// bug that was fixed by giving ACR source uploads a per-request x-ms-correlation-request-id. See
// `cli/azd/pkg/containerregistry/remote_build.go` (`uniqueCorrelationPolicy`) and CHANGELOG entry for PR #7776.
//
// Scenario: a two-service Container Apps project where BOTH services use `docker.remoteBuild: true`. The
// feat/exegraph parallel deploy scheduler runs both package/publish steps concurrently, so both services call
// ACR's `GetBuildSourceUploadURL` under the same ambient OpenTelemetry trace. Before the fix, both requests
// shared a single `x-ms-correlation-request-id`, ACR derived the same blob path for both upload URLs
// (`tasks-source/<yyyymmdd>/<correlationId>.tar.gz`), and whichever upload finished second clobbered the first.
// The net result: both Container Apps ran whichever service happened to upload its tarball last.
//
// The test catches that regression by giving each service source code that self-identifies (`{"service":"web"}`
// and `{"service":"api"}`), then asserting each deployed Container App serves its OWN identity. Two identical
// responses from the two FQDNs fails the test, because it means the image contents collided.
//
// # Recording mode
//
// This test follows the same playback pattern as the other functional tests — it calls `recording.Start(t)` and
// uses the recorded cassette when one is present under
// `testdata/recordings/Test_CLI_Up_Down_ContainerApp_RemoteBuild_MultiService`. To record a cassette, run the
// test once against a real subscription with the recording flag:
//
//	cd cli/azd
//	go test ./test/functional/ -run Test_CLI_Up_Down_ContainerApp_RemoteBuild_MultiService -timeout 90m -v \
//	    -record
//
// The recording harness writes a `.yaml` cassette that captures every HTTP exchange, including the ACR source
// upload URLs — verifying the recorded correlation-id headers are unique per request is itself a useful
// sanity check. In CI without a cassette + without a live subscription, `recording.Start` returns nil and the
// test is skipped below; this is consistent with other live-ACR tests in this file that are effectively opt-in
// for engineers running against their own subscription.
func Test_CLI_Up_Down_ContainerApp_RemoteBuild_MultiService(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	// Live-only: this test exercises a real ACR build. We need either a recorded
	// cassette OR live Azure credentials. Skip early if neither is available so
	// `recording.Start(t)` does not t.Fatal on the missing cassette.
	cassettePath := filepath.Join("testdata", "recordings",
		"Test_CLI_Up_Down_ContainerApp_RemoteBuild_MultiService.yaml")
	if _, err := os.Stat(cassettePath); err != nil && os.Getenv("AZURE_TENANT_ID") == "" {
		t.Skip("skipping multi-service remote-build test: no cassette recorded and no AZURE_TENANT_ID set")
	}

	session := recording.Start(t)

	// Defensive: recording.Start can still return nil in passthrough/live mode.
	if session == nil && os.Getenv("AZURE_TENANT_ID") == "" {
		t.Skip("skipping multi-service remote-build test: no cassette recorded and no AZURE_TENANT_ID set")
	}

	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	err := copySample(dir, "containerremotebuildmultiapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// `azd up` on feat/exegraph schedules package/publish in parallel by default; this is the code path that
	// exposed the ACR correlation-id collision. We use `up` (not separate provision + deploy) deliberately so
	// this test exercises the same scheduler the bug surfaces in.
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "up")
	require.NoError(t, err)

	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	webURL, hasWeb := env["WEB_URL"]
	require.True(t, hasWeb, "WEB_URL should be in environment after up")
	apiURL, hasAPI := env["API_URL"]
	require.True(t, hasAPI, "API_URL should be in environment after up")
	require.NotEqual(t, webURL, apiURL, "web and api must have distinct FQDNs")

	var client httpClient = http.DefaultClient
	backoff := retry.NewConstant(5 * time.Second)
	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]
		client = session.ProxyClient
		backoff = retry.NewConstant(1 * time.Millisecond)
	}

	const expectedWeb = `{"service":"web"}`
	const expectedAPI = `{"service":"api"}`

	// Each service MUST serve its OWN identity. The regression would show up as identical bodies from both
	// URLs (whichever service's tarball won the ACR upload race), so we assert each URL individually AND that
	// the two bodies differ.
	require.NoError(t, probeServiceHealth(t, ctx, client, backoff, webURL, expectedWeb),
		"web service did not return its own identity — image contents may have been contaminated by api")
	require.NoError(t, probeServiceHealth(t, ctx, client, backoff, apiURL, expectedAPI),
		"api service did not return its own identity — image contents may have been contaminated by web")

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}
