// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunCommandWithShell(t *testing.T) {
	runner := NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)

	runArgs := NewRunArgs("az", "--version").
		WithShell(true)

	res, err := runner.Run(context.Background(), runArgs)

	if err != nil {
		t.Errorf("failed to launch process: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("command returned non zero exit code %d", res.ExitCode)
	}

	if len(res.Stdout) == 0 {
		t.Errorf("stdout was empty")
	}

	if !regexp.MustCompile(`azure-cli\s+\d+\.\d+\.\d+`).Match([]byte(res.Stdout)) {
		t.Errorf("stdout %s did not contain 'azure-cli' and a version number", res.Stdout)
	}
}

func TestRunCommand(t *testing.T) {
	runner := NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)

	args := RunArgs{
		Cmd:  "git",
		Args: []string{"--version"},
	}
	res, err := runner.Run(context.Background(), args)

	if err != nil {
		t.Errorf("failed to launch process: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("command returned non zero exit code %d", res.ExitCode)
	}

	if len(res.Stdout) == 0 {
		t.Errorf("stdout was empty")
	}

	if !regexp.MustCompile(`git version\s+\d+\.\d+\.\d+`).Match([]byte(res.Stdout)) {
		t.Errorf("stdout did not contain 'git version' and a version number")
	}
}

func TestKillCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := time.Now()

	runner := NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
	_, err := runner.Run(ctx, RunArgs{
		Cmd: "pwsh",
		Args: []string{
			"-c",
			"sleep",
			"10000",
		},
	})

	if runtime.GOOS == "windows" {
		// on Windows terminating the process doesn't register as an error
		require.NoError(t, err)
	} else {
		require.EqualValues(t, "signal: killed", err.Error())
	}
	// should be pretty much instant since our context was already cancelled
	// but we'll give a little wiggle room (as long as it's < 10000 seconds, which is
	// what we're sleeping on in the powershell)
	since := time.Since(s)
	require.LessOrEqual(t, since, 10*time.Second)
	require.GreaterOrEqual(t, since, 1*time.Second)
}

func TestAppendEnv(t *testing.T) {
	require.Nil(t, appendEnv([]string{}))
	require.Nil(t, appendEnv(nil))

	expectedEnv := os.Environ()
	expectedEnv = append(expectedEnv, "azd_random_var=world")
	sort.Strings(expectedEnv)

	actualEnv := appendEnv([]string{"azd_random_var=world"})
	sort.Strings(actualEnv)

	require.Equal(t, expectedEnv, actualEnv)
}

func TestRunCommandList(t *testing.T) {
	res, err := RunCommandList(context.Background(), []string{
		"git --version",
		"az --version",
	}, nil, "")

	if err != nil {
		t.Errorf("failed to run command list: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("command list returned non zero exit code %d", res.ExitCode)
	}

	if len(res.Stdout) == 0 {
		t.Errorf("stdout was empty")
	}

	if !regexp.MustCompile(`git version\s+\d+\.\d+\.\d+`).Match([]byte(res.Stdout)) {
		t.Errorf("stdout did not contain 'git version' output")
	}

	if !regexp.MustCompile(`azure\-cli\s+\d+\.\d+\.\d+`).Match([]byte(res.Stdout)) {
		t.Errorf("stdout did not contain 'az version' output")
	}
}

func TestRunCapturingStderr(t *testing.T) {
	myStderr := &bytes.Buffer{}

	runner := NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
	res, _ := runner.Run(context.Background(), RunArgs{
		Cmd:    "go",
		Args:   []string{"--help"},
		Stderr: myStderr,
	})

	require.NotEmpty(t, res.Stderr)
	require.Equal(t, res.Stderr, myStderr.String())
}

func TestRunEnrichError(t *testing.T) {
	runner := NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
	_, err := runner.Run(context.Background(), RunArgs{
		Cmd:  "go",
		Args: []string{"--help"},
	})

	// the non-enriched error is the standard error message that comes from (most likely)
	// an ExitError
	require.EqualError(t, err, "exit status 2")

	res, err := runner.Run(context.Background(), RunArgs{
		Cmd:         "go",
		Args:        []string{"--help"},
		EnrichError: true,
	})

	// 'enriched' errors contain the contents of the Res() as well. This makes it a bit
	// easier for callers since they can just check that 'err ! nil', and not involve
	// themselves in checking the ExitCode.
	require.EqualError(t, err, fmt.Sprintf("%s: exit status 2", res.String()))
}

func TestRedactSensitiveData(t *testing.T) {
	tests := []struct {
		scenario string
		input    string
		expected string
	}{
		{scenario: "Basic",
			input: `"accessToken": "eyJ0eX",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`,
			expected: `"accessToken": "<redacted>",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`},
		{scenario: "NoReplacement",
			input: `"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`,
			expected: `"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`},
		{scenario: "MultipleReplacement",
			input: `"accessToken": "eyJ0eX",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer",
"accessToken": "skJ02wsfK"
}`,
			expected: `"accessToken": "<redacted>",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer",
"accessToken": "<redacted>"
}`},

		{scenario: "SWADeploymentToken",
			// nolint:lll
			input: `npx -y @azure/static-web-apps-cli@1.0.0 deploy --tenant-id abc-123 --subscription-id abc-123 --resource-group r --app-name app-name --app-location / --output-location . --env default --no-use-keychain --deployment-token abc-123`,
			// nolint:lll
			expected: `npx -y @azure/static-web-apps-cli@1.0.0 deploy --tenant-id abc-123 --subscription-id abc-123 --resource-group r --app-name app-name --app-location / --output-location . --env default --no-use-keychain --deployment-token <redacted>`},

		{scenario: "DockerLoginUsernameAndPassword",
			input:    `docker login --username crusername123 --password abc123 some.azurecr.io`,
			expected: `docker login --username <redacted> --password <redacted> some.azurecr.io`},
	}

	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			actual := redactSensitiveData(test.input)
			require.Equal(t, test.expected, actual)
		})
	}
}
