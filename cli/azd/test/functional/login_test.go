package cli_test

import (
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
	result, err := cli.RunCommand(ctx, "auth", "login", "--check-status", "--output", "json")
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
