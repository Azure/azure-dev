// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzCli(t *testing.T) {
	t.Run("DebugAndTelemetryEnabled", func(t *testing.T) {
		tempCLI := NewAzCli(NewAzCliArgs{
			EnableDebug:     true,
			EnableTelemetry: true,
		})

		cli := tempCLI.(*azCli)

		require.True(t, cli.enableDebug)
		require.True(t, cli.enableTelemetry)

		var env []string
		var commandArgs []string

		cli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			env = args.Env
			commandArgs = args.Args
			return executil.RunResult{}, nil
		}

		_, err := cli.runAzCommand(context.Background(), "hello")
		require.NoError(t, err)

		require.Equal(t, []string{
			fmt.Sprintf("AZURE_HTTP_USER_AGENT=%s", azdinternal.MakeUserAgentString("")),
		}, env)

		require.Equal(t, []string{"hello", "--debug"}, commandArgs)
	})

	t.Run("DebugAndTelemetryDisabled", func(t *testing.T) {
		tempCLI := NewAzCli(NewAzCliArgs{
			EnableDebug:     false,
			EnableTelemetry: false,
		})

		cli := tempCLI.(*azCli)

		require.False(t, cli.enableDebug)
		require.False(t, cli.enableTelemetry)

		var env []string
		var commandArgs []string
		cli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			env = args.Env
			commandArgs = args.Args
			return executil.RunResult{}, nil
		}

		_, err := cli.runAzCommand(context.Background(), "hello")
		require.NoError(t, err)

		require.Equal(t, []string{
			fmt.Sprintf("AZURE_HTTP_USER_AGENT=%s", azdinternal.MakeUserAgentString("")),
			"AZURE_CORE_COLLECT_TELEMETRY=no",
		}, env)

		require.Equal(t, []string{"hello"}, commandArgs)
	})
}

func TestAZCLIWithUserAgent(t *testing.T) {
	tempAZCLI := NewAzCli(NewAzCliArgs{
		EnableTelemetry: true,
		EnableDebug:     true,
	})

	tempAZCLI.SetUserAgent(internal.MakeUserAgentString("AZTesting=yes"))

	azcli := tempAZCLI.(*azCli)

	account := mustGetDefaultAccount(t, azcli)

	userAgent := runAndCaptureUserAgent(t, azcli, account.Id)
	require.Contains(t, userAgent, "AZTesting=yes")
	require.Contains(t, userAgent, "azdev")
}

func mustGetDefaultAccount(t *testing.T, azcli AzCli) AzCliSubscriptionInfo {
	accounts, err := azcli.ListAccounts(context.Background())
	require.NoError(t, err)

	for _, account := range accounts {
		if account.IsDefault {
			return account
		}
	}

	assert.Fail(t, "No default account set")
	return AzCliSubscriptionInfo{}
}

func runAndCaptureUserAgent(t *testing.T, azcli *azCli, subscriptionID string) string {
	stderrBuffer := &bytes.Buffer{}

	azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
		if args.Stderr != nil {
			args.Stderr = io.MultiWriter(stderrBuffer, args.Stderr)
		} else {
			args.Stderr = stderrBuffer
		}

		rr, err := executil.RunWithResult(ctx, args)
		return rr, err
	}

	// the result doesn't matter here since we just want to see what the User-Agent is that we sent, which will
	// happen regardless of whether the request succeeds or fails.
	_, _ = azcli.ListResourceGroupResources(context.Background(), subscriptionID, "ResourceGroupThatDoesNotExist")

	// The outputted line will look like this:
	// DEBUG: cli.azure.cli.core.sdk.policies:     'User-Agent': 'AZURECLI/2.35.0 (MSI) azsdk-python-azure-mgmt-resource/20.0.0 Python/3.10.3 (Windows-10-10.0.22621-SP0) azdev/0.0.0-dev.0 AZTesting=yes'
	re := regexp.MustCompile(`'User-Agent':\s+'([^']+)'`)

	matches := re.FindAllStringSubmatch(stderrBuffer.String(), -1)
	require.NotNil(t, matches)

	return matches[0][1]
}
