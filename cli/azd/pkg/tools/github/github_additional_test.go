// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGhOutputToList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "MultipleSecrets",
			input: "SECRET_A\tUpdated 2024-01-01\n" +
				"SECRET_B\tUpdated 2024-01-02\n",
			want: []string{"SECRET_A", "SECRET_B"},
		},
		{
			name:  "EmptyOutput",
			input: "",
			want:  []string{},
		},
		{
			name:  "SingleLine",
			input: "MY_SECRET\tUpdated 2024-01-01\n",
			want:  []string{"MY_SECRET"},
		},
		{
			name:  "NoTabs",
			input: "SECRET_ONLY\n",
			want:  []string{"SECRET_ONLY"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ghOutputToList(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGhOutputToMap(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "MultipleVariables",
			input: "VAR_A\tvalue_a\tUpdated\n" +
				"VAR_B\tvalue_b\tUpdated\n",
			want: map[string]string{
				"VAR_A": "value_a",
				"VAR_B": "value_b",
			},
		},
		{
			name:  "EmptyOutput",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "SingleVariable",
			input: "KEY\tVALUE\n",
			want:  map[string]string{"KEY": "VALUE"},
		},
		{
			name:    "BadFormat",
			input:   "no-tab-here\n",
			wantErr: true,
		},
		{
			name: "MixedValidInvalid",
			input: "VALID\tvalue\n" +
				"invalid_no_tab\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ghOutputToMap(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGhCliVersionRegexp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		matches bool
	}{
		{
			name: "StandardVersion",
			input: "gh version 2.86.0 (2024-01-15)\n" +
				"https://github.com/cli/cli/" +
				"releases/tag/v2.86.0",
			want:    "2.86.0",
			matches: true,
		},
		{
			name:    "OlderVersion",
			input:   "gh version 2.6.0 (2022-03-15)",
			want:    "2.6.0",
			matches: true,
		},
		{
			name:    "NoMatch",
			input:   "some random text",
			matches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := ghCliVersionRegexp.FindStringSubmatch(
				tt.input,
			)
			if !tt.matches {
				require.Len(t, matches, 0)
				return
			}
			require.Len(t, matches, 2)
			require.Equal(t, tt.want, matches[1])
		})
	}
}

func TestIsGhCliNotLoggedInMessageRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "AuthenticatePlease",
			input: "To authenticate, please run " +
				"`gh auth login`.",
			want: true,
		},
		{
			name: "TryAuthenticating",
			input: "Try authenticating with: " +
				" gh auth login",
			want: true,
		},
		{
			name: "ReAuthenticate",
			input: "To re-authenticate, run: " +
				"gh auth login",
			want: true,
		},
		{
			name: "GetStarted",
			input: "To get started with GitHub CLI, " +
				"please run:  gh auth login",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "everything is fine",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGhCliNotLoggedInMessageRegex.MatchString(
				tt.input,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsUserNotAuthorizedMessageRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "Matching",
			input: "HTTP 403: Resource not " +
				"accessible by integration",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "HTTP 200: OK",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUserNotAuthorizedMessageRegex.MatchString(
				tt.input,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNotLoggedIntoAnyGitHubHostsRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "Matching",
			input: "You are not logged into any " +
				"GitHub hosts.",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "Logged in to github.com",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := notLoggedIntoAnyGitHubHostsMessageRegex.
				MatchString(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRepositoryNameInUseRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name: "Matching",
			input: "GraphQL: Name already exists on " +
				"this account (createRepository)",
			want: true,
		},
		{
			name:  "NotMatching",
			input: "repository created successfully",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repositoryNameInUseRegex.MatchString(
				tt.input,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRunningOnCodespaces(t *testing.T) {
	t.Run("InCodespaces", func(t *testing.T) {
		t.Setenv("CODESPACES", "true")
		require.True(t, RunningOnCodespaces())
	})

	t.Run("NotInCodespaces", func(t *testing.T) {
		t.Setenv("CODESPACES", "false")
		require.False(t, RunningOnCodespaces())
	})

	t.Run("EnvNotSet", func(t *testing.T) {
		t.Setenv("CODESPACES", "")
		require.False(t, RunningOnCodespaces())
	})
}

func TestCliName(t *testing.T) {
	cli := &Cli{}
	require.Equal(t, "GitHub CLI", cli.Name())
}

func TestCliInstallUrl(t *testing.T) {
	cli := &Cli{}
	require.Equal(
		t,
		"https://aka.ms/azure-dev/github-cli-install",
		cli.InstallUrl(),
	)
}

func TestCliBinaryPath(t *testing.T) {
	cli := &Cli{path: "/usr/local/bin/gh"}
	require.Equal(
		t, "/usr/local/bin/gh", cli.BinaryPath(),
	)
}

func TestCliBinaryPathEmpty(t *testing.T) {
	cli := &Cli{}
	require.Equal(t, "", cli.BinaryPath())
}

func TestProtocolTypeConstants(t *testing.T) {
	require.Equal(t, "ssh", GitSshProtocolType)
	require.Equal(t, "https", GitHttpsProtocolType)
}

func TestGhCliName(t *testing.T) {
	name := ghCliName()
	require.NotEmpty(t, name)
	// On all platforms, it should either be "gh" or "gh.exe"
	require.Contains(t, name, "gh")
}

func TestGitHubHostName(t *testing.T) {
	require.Equal(t, "github.com", GitHubHostName)
}

func TestTokenEnvVars(t *testing.T) {
	require.Contains(t, TokenEnvVars, "GITHUB_TOKEN")
	require.Contains(t, TokenEnvVars, "GH_TOKEN")
	require.Len(t, TokenEnvVars, 2)
}
