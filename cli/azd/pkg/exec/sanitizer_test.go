// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSensitiveArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		args               []string
		sensitiveDataMatch []string
		expected           []string
	}{
		{
			name:               "EmptySensitiveDataReturnsOriginalSlice",
			args:               []string{"--password", "secret123"},
			sensitiveDataMatch: []string{},
			expected:           []string{"--password", "secret123"},
		},
		{
			name:               "NilSensitiveDataReturnsOriginalSlice",
			args:               []string{"--password", "secret123"},
			sensitiveDataMatch: nil,
			expected:           []string{"--password", "secret123"},
		},
		{
			name:               "EmptyArgs",
			args:               []string{},
			sensitiveDataMatch: []string{"secret"},
			expected:           []string{},
		},
		{
			name:               "NoMatchingData",
			args:               []string{"git", "push", "origin"},
			sensitiveDataMatch: []string{"secret123"},
			expected:           []string{"git", "push", "origin"},
		},
		{
			name:               "SingleArgRedacted",
			args:               []string{"--token", "abc123"},
			sensitiveDataMatch: []string{"abc123"},
			expected:           []string{"--token", "<redacted>"},
		},
		{
			name:               "MultipleArgsRedacted",
			args:               []string{"--user", "admin", "--pass", "s3cret"},
			sensitiveDataMatch: []string{"admin", "s3cret"},
			expected:           []string{"--user", "<redacted>", "--pass", "<redacted>"},
		},
		{
			name:               "SamePatternAppearsMultipleTimesInOneArg",
			args:               []string{"user=admin&backup=admin"},
			sensitiveDataMatch: []string{"admin"},
			expected:           []string{"user=<redacted>&backup=<redacted>"},
		},
		{
			name:               "SensitiveDataAsSubstring",
			args:               []string{"Server=myhost;Password=secret123;Database=mydb"},
			sensitiveDataMatch: []string{"secret123"},
			expected:           []string{"Server=myhost;Password=<redacted>;Database=mydb"},
		},
		{
			name:               "MultipleSensitivePatternsInOneArg",
			args:               []string{"user:admin pass:secret123"},
			sensitiveDataMatch: []string{"admin", "secret123"},
			expected:           []string{"user:<redacted> pass:<redacted>"},
		},
		{
			name:               "EntireArgIsSensitive",
			args:               []string{"mytoken"},
			sensitiveDataMatch: []string{"mytoken"},
			expected:           []string{"<redacted>"},
		},
		{
			name:               "PreservesNonSensitiveArgs",
			args:               []string{"--verbose", "--token", "secret", "--output", "json"},
			sensitiveDataMatch: []string{"secret"},
			expected:           []string{"--verbose", "--token", "<redacted>", "--output", "json"},
		},
		{
			name:               "SingleArgSingleSensitive",
			args:               []string{"key=value"},
			sensitiveDataMatch: []string{"value"},
			expected:           []string{"key=<redacted>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			originalArgs := make([]string, len(tt.args))
			copy(originalArgs, tt.args)

			result := RedactSensitiveArgs(tt.args, tt.sensitiveDataMatch)
			require.Equal(t, tt.expected, result)

			// When sensitive patterns are provided, a new slice is allocated, so the original must be untouched.
			if len(tt.sensitiveDataMatch) > 0 {
				require.Equal(t, originalArgs, tt.args, "original args must not be modified")
			}
		})
	}
}

func TestRedactSensitiveData_TokenPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "TokenField",
			input:    `{"token": "eyJhbGciOiJSUzI1NiJ9"}`,
			expected: `{"token": "<redacted>"}`,
		},
		{
			name:     "KubectlFromLiteral",
			input:    `kubectl create secret generic my-secret --from-literal=DB_PASSWORD=super-s3cret`,
			expected: `kubectl create secret generic my-secret --from-literal=DB_PASSWORD=<redacted>`,
		},
		{
			name:     "CombinedArgKeyValue",
			input:    `--api-key=abc123xyz`,
			expected: `--api-key=<redacted>`,
		},
		{
			name:     "AccessTokenField",
			input:    `{"accessToken": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig"}`,
			expected: `{"accessToken": "<redacted>"}`,
		},
		{
			name:     "DeploymentToken",
			input:    `az staticwebapp deploy --deployment-token abc123secret`,
			expected: `az staticwebapp deploy --deployment-token <redacted>`,
		},
		{
			name:     "UsernameFlag",
			input:    `docker login --username myuser --password mypass`,
			expected: `docker login --username <redacted> --password <redacted>`,
		},
		{
			name:     "PasswordFlagAlone",
			input:    `mysql --password SuperSecret123`,
			expected: `mysql --password <redacted>`,
		},
		{
			name:     "NoSensitiveData",
			input:    `just a plain message with no patterns`,
			expected: `just a plain message with no patterns`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actual := RedactSensitiveData(tt.input)
			require.Equal(t, tt.expected, actual)
		})
	}
}
