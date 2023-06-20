// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunCommand(t *testing.T) {
	runner := NewCommandRunner(nil)

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
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	s := time.Now()

	runner := NewCommandRunner(nil)
	var args RunArgs
	if runtime.GOOS == "windows" {
		args = RunArgs{
			Cmd: "pwsh",
			Args: []string{
				"-c",
				"sleep",
				"10000",
			},
		}
	} else {
		args = RunArgs{
			Cmd: "sh",
			Args: []string{
				"-c",
				"sleep 10",
			},
		}
	}

	_, err := runner.Run(ctx, args)

	if runtime.GOOS == "windows" {
		// on Windows terminating the process doesn't register as an error
		require.NoError(t, err)
	} else {
		require.ErrorContains(t, err, "signal: killed")
	}
	// should be pretty much instant since our context was already cancelled
	// but we'll give a little wiggle room (as long as it's < 10000 seconds, which is
	// what we're sleeping on in the powershell)
	since := time.Since(s)
	require.LessOrEqual(t, since, 10*time.Second)
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

func TestRunList(t *testing.T) {
	runner := NewCommandRunner(nil)
	res, err := runner.RunList(context.Background(), []string{
		"git --version",
	}, RunArgs{})

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
}

func TestRunCapturingStderr(t *testing.T) {
	myStderr := &bytes.Buffer{}

	runner := NewCommandRunner(nil)
	res, _ := runner.Run(context.Background(), RunArgs{
		Cmd:    "go",
		Args:   []string{"--help"},
		Stderr: myStderr,
	})

	require.NotEmpty(t, res.Stderr)
	require.Equal(t, res.Stderr, myStderr.String())
}

func TestError(t *testing.T) {
	runner := NewCommandRunner(nil)
	_, err := runner.Run(context.Background(), RunArgs{
		Cmd:  "go",
		Args: []string{"--help"},
	})

	var exitErr *ExitError
	require.ErrorAs(t, err, &exitErr)
	require.ErrorContains(t, exitErr, "exit code: 2, stdout:")
}

func TestError_Interactive(t *testing.T) {
	runner := NewCommandRunner(nil)
	_, err := runner.Run(context.Background(), RunArgs{
		Cmd:         "go",
		Args:        []string{"--help"},
		Interactive: true,
	})

	var exitErr *ExitError
	require.ErrorAs(t, err, &exitErr)
	// when interactive, no output is captured in the error
	require.Equal(t, exitErr.Error(), "exit code: 2")
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
			input: `npx -y @azure/static-web-apps-cli@1.0.6 deploy --tenant-id abc-123 --subscription-id abc-123 --resource-group r --app-name app-name --app-location / --output-location . --env default --no-use-keychain --deployment-token abc-123`,
			// nolint:lll
			expected: `npx -y @azure/static-web-apps-cli@1.0.6 deploy --tenant-id abc-123 --subscription-id abc-123 --resource-group r --app-name app-name --app-location / --output-location . --env default --no-use-keychain --deployment-token <redacted>`},

		{scenario: "DockerLoginUsernameAndPassword",
			input:    `docker login --username crusername123 --password abc123 some.azurecr.io`,
			expected: `docker login --username <redacted> --password <redacted> some.azurecr.io`},
	}

	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			actual := RedactSensitiveData(test.input)
			require.Equal(t, test.expected, actual)
		})
	}
}
