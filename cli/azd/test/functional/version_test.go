// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

// Returns the expected version number of `azd`.
//
//   - If AZD_TEST_CLI_VERSION is set, the version specified by the variable. This is used as an end-to-end
//     test that the version matches what is generated explicitly in CI.
//   - Otherwise, the version specified in source.
func getExpectedVersion(t *testing.T) string {
	expected := internal.VersionInfo().Version.String()

	versionVar, ok := os.LookupEnv("AZD_TEST_CLI_VERSION")
	if ok {
		expected = versionVar
	}
	require.NotEmpty(t, expected)

	return expected
}

func Test_CLI_Version_Text(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	result, err := cli.RunCommand(ctx, "version")
	require.NoError(t, err)

	expected := getExpectedVersion(t)
	require.Contains(t, result.Stdout, fmt.Sprintf("azd version %s", expected))
}

func Test_CLI_Version_Json(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	result, err := cli.RunCommand(ctx, "version", "--output", "json")
	require.NoError(t, err)

	versionJson := &contracts.VersionResult{}
	err = json.Unmarshal([]byte(result.Stdout), versionJson)
	require.NoError(t, err)

	_, err = semver.Parse(versionJson.Azd.Version)
	require.NoError(t, err)

	expected := getExpectedVersion(t)
	require.Equal(t, expected, versionJson.Azd.Version)
}

func Test_CLI_Version_NoExtraConsoleMessages(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	result, err := cli.RunCommand(ctx, "version", "--output", "json")
	require.NoError(t, err)

	require.Empty(t, result.Stderr)
}
