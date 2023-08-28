package cli_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// Verifies login status functionality (azd login).
// This is important to ensure we do not break editor integrations that consume azd CLI.
func Test_CLI_LoginStatus(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	result, err := cli.RunCommand(ctx, "login", "--check-status", "--output", "json")
	require.NoError(t, err)

	loginResult := contracts.LoginResult{}
	err = json.Unmarshal([]byte(result.Stdout), &loginResult)
	require.NoError(t, err, "failed to deserialize login result")

	switch loginResult.Status {
	case contracts.LoginStatusUnauthenticated:
		require.Fail(t, "User isn't currently logged in. Rerun this test with a logged in user to pass the test.")
	case contracts.LoginStatusSuccess:
		require.NotNil(t, loginResult.ExpiresOn)
	default:
		require.Fail(t, "Unexpected login status: %s", loginResult.Status)
	}
}

// Verifies login status functionality (azd auth login).
// This is important to ensure we do not break editor integrations that consume azd CLI.
func Test_CLI_AuthLoginStatus(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	loginResult := loginStatus(t, ctx, cli)

	switch loginResult.Status {
	case contracts.LoginStatusUnauthenticated:
		require.Fail(t, "User isn't currently logged in. Rerun this test with a logged in user to pass the test.")
	case contracts.LoginStatusSuccess:
		require.NotNil(t, loginResult.ExpiresOn)
	default:
		require.Fail(t, "Unexpected login status: %s", loginResult.Status)
	}
}

func Test_CLI_LoginServicePrincipal(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()

	cli := azdcli.NewCLI(t)
	// Isolate login to a separate configuration directory
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+dir)
	if cfg.ClientID == "" || cfg.TenantID == "" || cfg.ClientSecret == "" {
		if cfg.CI {
			panic("Service principal is not configured. AZD_TEST_* variables are required to be set for live testing.")
		}

		t.Skip("Skipping test because service principal is not configured. " +
			"Set the relevant AZD_TEST_* variables to run this test.")
		return
	}

	loginState := loginStatus(t, ctx, cli)
	require.Equal(t, contracts.LoginStatusUnauthenticated, loginState.Status)

	_, err := cli.RunCommand(ctx,
		"auth", "login",
		"--client-id", cfg.ClientID,
		"--client-secret", cfg.ClientSecret,
		"--tenant-id", cfg.TenantID)
	require.NoError(t, err)

	loginState = loginStatus(t, ctx, cli)
	require.Equal(t, contracts.LoginStatusSuccess, loginState.Status)

	_, err = cli.RunCommand(ctx, "auth", "logout")
	require.NoError(t, err)

	loginState = loginStatus(t, ctx, cli)
	require.Equal(t, contracts.LoginStatusUnauthenticated, loginState.Status)
}

func loginStatus(t *testing.T, ctx context.Context, cli *azdcli.CLI) contracts.LoginResult {
	result, err := cli.RunCommand(ctx, "auth", "login", "--check-status", "--output", "json")
	require.NoError(t, err)

	loginResult := contracts.LoginResult{}
	err = json.Unmarshal([]byte(result.Stdout), &loginResult)
	require.NoError(t, err, "failed to deserialize login result")

	return loginResult
}
