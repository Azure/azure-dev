// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzCli(t *testing.T) {
	t.Run("DebugAndTelemetryEnabled", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		tempCli := NewAzCli(identity.GetCredentials(*mockContext.Context), NewAzCliArgs{
			EnableDebug:     true,
			EnableTelemetry: true,
			CommandRunner:   mockContext.CommandRunner,
		})

		*mockContext.Context = WithAzCli(*mockContext.Context, tempCli)
		tempCli = GetAzCli(*mockContext.Context)
		cli := tempCli.(*azCli)

		require.True(t, cli.enableDebug)
		require.True(t, cli.enableTelemetry)

		var env []string
		var commandArgs []string

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az hello")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			env = args.Env
			commandArgs = args.Args
			return exec.RunResult{}, nil
		})

		_, err := cli.runAzCommand(*mockContext.Context, "hello")
		require.NoError(t, err)

		require.Equal(t, []string{
			fmt.Sprintf("AZURE_HTTP_USER_AGENT=%s", internal.MakeUserAgentString("")),
		}, env)

		require.Equal(t, []string{"hello", "--debug"}, commandArgs)
	})

	t.Run("DebugAndTelemetryDisabled", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		tempCli := NewAzCli(identity.GetCredentials(*mockContext.Context), NewAzCliArgs{
			EnableDebug:     false,
			EnableTelemetry: false,
			CommandRunner:   mockContext.CommandRunner,
		})

		*mockContext.Context = WithAzCli(*mockContext.Context, tempCli)
		tempCli = GetAzCli(*mockContext.Context)
		cli := tempCli.(*azCli)

		require.False(t, cli.enableDebug)
		require.False(t, cli.enableTelemetry)

		var env []string
		var commandArgs []string

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az hello")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			env = args.Env
			commandArgs = args.Args
			return exec.RunResult{}, nil
		})

		_, err := cli.runAzCommand(*mockContext.Context, "hello")
		require.NoError(t, err)

		require.Equal(t, []string{
			fmt.Sprintf("AZURE_HTTP_USER_AGENT=%s", internal.MakeUserAgentString("")),
			"AZURE_CORE_COLLECT_TELEMETRY=no",
		}, env)

		require.Equal(t, []string{"hello"}, commandArgs)
	})
}

func TestAZCLIWithUserAgent(t *testing.T) {
	userAgent := runAndCaptureUserAgent(t)

	require.Contains(t, userAgent, "AZTesting=yes")
	require.Contains(t, userAgent, "azdev")
}

func Test_AzCli_Login_Appends_useDeviceCode(t *testing.T) {
	var commandArgs []string
	var writer io.Writer

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--use-device-code")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		commandArgs = args.Args
		return exec.NewRunResult(0, "", ""), nil
	})

	azCli := GetAzCli(*mockContext.Context)
	err := azCli.Login(*mockContext.Context, true, writer)
	require.NoError(t, err)
	require.Contains(t, commandArgs, "--use-device-code")
}

func Test_AzCli_Login_DoesNotAppend_useDeviceCode(t *testing.T) {
	var commandArgs []string
	var writer io.Writer

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return !strings.Contains(command, "--use-device-code")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		commandArgs = args.Args
		return exec.NewRunResult(0, "", ""), nil
	})

	azCli := GetAzCli(*mockContext.Context)
	err := azCli.Login(*mockContext.Context, false, writer)

	require.NoError(t, err)
	require.NotContains(t, commandArgs, "--use-device-code")
}

func runAndCaptureUserAgent(t *testing.T) string {
	// Get the default command runner implementation
	defaultRunner := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
	mockContext := mocks.NewMockContext(context.Background())

	cli := NewAzCli(identity.GetCredentials(*mockContext.Context), NewAzCliArgs{
		EnableDebug:     true,
		EnableTelemetry: true,
		CommandRunner:   mockContext.CommandRunner,
	})
	cli.SetUserAgent(internal.MakeUserAgentString("AZTesting=yes"))

	stderrBuffer := &bytes.Buffer{}

	// Mock the command runner
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		if args.Stderr != nil {
			args.Stderr = io.MultiWriter(stderrBuffer, args.Stderr)
		} else {
			args.Stderr = stderrBuffer
		}

		// Invoke the real command runner
		rr, err := defaultRunner.Run(*mockContext.Context, args)
		return rr, err
	})

	// Since most of the az CLI commands have been refactored to use Go SDKs at this point
	// We just want to verify that any left over commands still pass in the required user agent strings.
	// Here we will just execute a custom command against the concrete CLI helper method
	runArgs := exec.
		NewRunArgs("az", "group", "show", "-g", "RESOURCE_GROUP").
		WithDebug(true).
		WithEnrichError(true)

	// Cast to the concrete CLI so we can exec the common command
	concreteCli := cli.(*azCli)

	// the result doesn't matter here since we just want to see what the User-Agent is that we sent, which will
	// happen regardless of whether the request succeeds or fails.
	_, _ = concreteCli.runAzCommandWithArgs(context.Background(), runArgs)

	// The outputted line will look like this:
	// DEBUG: cli.azure.cli.core.sdk.policies:     'User-Agent': 'AZURECLI/2.35.0 (MSI)
	// azsdk-python-azure-mgmt-resource/20.0.0 Python/3.10.3 (Windows-10-10.0.22621-SP0) azdev/0.0.0-dev.0 AZTesting=yes'
	re := regexp.MustCompile(`'User-Agent':\s+'([^']+)'`)

	matches := re.FindAllStringSubmatch(stderrBuffer.String(), -1)
	require.NotNil(t, matches)

	return matches[0][1]
}

func Test_extractDeploymentError(t *testing.T) {
	type args struct {
		stderr string
	}
	//nolint:lll
	tests := []struct {
		name     string
		args     args
		expected string
	}{
		{
			name: "errorWithInner",
			args: args{
				`ERROR: {"code": "InvalidTemplateDeployment", "message": "See inner errors for details."}

Inner Errors:
{"code": "PreflightValidationCheckFailed", "message": "Preflight validation failed. Please refer to the details for the specific errors."}

Inner Errors:
{"code": "AccountNameInvalid", "target": "invalid-123", "message": "invalid-123 is not a valid storage account name. Storage account name must be between 3 and 24 characters in length and use numbers and lower-case letters only."}`},
			expected: `Deployment Error Details:
InvalidTemplateDeployment: See inner errors for details.

Inner Error:
PreflightValidationCheckFailed: Preflight validation failed. Please refer to the details for the specific errors.

Inner Error:
AccountNameInvalid: invalid-123 is not a valid storage account name. Storage account name must be between 3 and 24 characters in length and use numbers and lower-case letters only.
`,
		},
		{
			name: "errorWithInnerRaw",
			args: args{
				`ERROR: {"code": "InvalidTemplateDeployment", "message": "See inner errors for details."}

Line1: additional detail happened
Line2: additional detail happened`,
			},
			expected: `Deployment Error Details:
InvalidTemplateDeployment: See inner errors for details.

Line1: additional detail happened
Line2: additional detail happened`,
		},
		{
			name: "errorOnly",
			args: args{
				`ERROR: {"code": "InvalidTemplateDeployment", "message": "Invalid template deployment."}`,
			},
			expected: `Deployment Error Details:
InvalidTemplateDeployment: Invalid template deployment.
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := extractDeploymentError(tt.args.stderr)
			assert.Equal(t, tt.expected, err.Error())
		})
	}
}

func TestAZCliGetAccessTokenTranslatesErrors(t *testing.T) {
	//nolint:lll
	tests := []struct {
		name   string
		stderr string
		expect error
	}{
		{
			name:   "AADSTS70043",
			stderr: "AADSTS70043: The refresh token has expired or is invalid due to sign-in frequency checks by conditional access. The token was issued on {issueDate} and the maximum allowed lifetime for this request is {time}.",
			expect: ErrAzCliRefreshTokenExpired,
		},
		{
			name:   "AADSTS700082",
			stderr: "AADSTS700082: The refresh token has expired due to inactivity. The token was issued on {issueDate} and was inactive for {time}.",
			expect: ErrAzCliRefreshTokenExpired,
		},
		{
			name:   "GetAccessTokenDoubleQuotes",
			stderr: `Please run "az login" to setup account.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenSingleQuotes",
			stderr: `Please run 'az login' to setup account.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenDoubleQuotesAccessAccount",
			stderr: `Please run "az login" to access your accounts.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenSingleQuotesAccessAccount",
			stderr: `Please run 'az login' to access your accounts.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenErrorNoSubscriptionFound",
			stderr: `ERROR: No subscription found`,
			expect: ErrAzCliNotLoggedIn,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			mockCredential := mocks.MockCredentials{
				GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
					return azcore.AccessToken{}, errors.New(test.stderr)
				},
			}

			azCli := NewAzCli(&mockCredential, NewAzCliArgs{
				EnableDebug:     true,
				EnableTelemetry: true,
				CommandRunner:   mockContext.CommandRunner,
			})

			_, err := azCli.GetAccessToken(*mockContext.Context)
			assert.True(t, errors.Is(err, test.expect))
		})
	}
}

func Test_AzSdk_User_Agent_Policy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == "/RESOURCE_ID"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armresources.ClientGetByIDResponse{
			GenericResource: armresources.GenericResource{
				ID:       convert.RefOf("RESOURCE_ID"),
				Kind:     convert.RefOf("RESOURCE_KIND"),
				Name:     convert.RefOf("RESOURCE_NAME"),
				Type:     convert.RefOf("RESOURCE_TYPE"),
				Location: convert.RefOf("RESOURCE_LOCATION"),
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	var rawResponse *http.Response
	ctx := runtime.WithCaptureResponse(*mockContext.Context, &rawResponse)

	azCli := GetAzCli(ctx)
	// We don't care about the actual response or if an error occurred
	// Any API call that leverages the Go SDK is fine
	_, _ = azCli.GetResource(ctx, "SUBSCRIPTION_ID", "RESOURCE_ID")

	userAgent, ok := rawResponse.Request.Header["User-Agent"]
	if !ok {
		require.Fail(t, "missing User-Agent header")
	}

	require.Contains(t, userAgent[0], "azsdk-go")
	require.Contains(t, userAgent[0], "azdev")
}
