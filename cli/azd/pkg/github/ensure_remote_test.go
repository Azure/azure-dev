// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"errors"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestEnsureRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stdout     string
		stderr     string
		runErr     error
		wantSlug   string
		wantErrMsg string
	}{
		{
			name:     "SuccessHTTPS",
			stdout:   "https://github.com/Azure/azure-dev.git\n",
			wantSlug: "Azure/azure-dev",
		},
		{
			name:     "SuccessSSH",
			stdout:   "git@github.com:Azure/azure-dev.git\n",
			wantSlug: "Azure/azure-dev",
		},
		{
			name:     "SuccessHTTPSNoSuffix",
			stdout:   "https://github.com/Azure/azure-dev\n",
			wantSlug: "Azure/azure-dev",
		},
		{
			name:       "NotGitHubRemote",
			stdout:     "https://gitlab.com/Azure/azure-dev.git\n",
			wantErrMsg: "is not a GitHub repository",
		},
		{
			name:       "NoSuchRemote",
			stderr:     "fatal: No such remote 'origin'",
			runErr:     errors.New("exit 2"),
			wantErrMsg: "failed to get remote url",
		},
		{
			name:       "NotAGitRepo",
			stderr:     "fatal: not a git repository",
			runErr:     errors.New("exit 128"),
			wantErrMsg: "failed to get remote url",
		},
		{
			name:       "MalformedURLOtherHost",
			stdout:     "not-a-remote\n",
			wantErrMsg: "is not a GitHub repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			runner.When(func(args exec.RunArgs, command string) bool {
				return slices.Contains(args.Args, "remote") &&
					slices.Contains(args.Args, "get-url")
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{
					Stdout: tt.stdout,
					Stderr: tt.stderr,
				}, tt.runErr
			})

			gitCli := git.NewCli(runner)
			slug, err := EnsureRemote(t.Context(), "/some/repo", "origin", gitCli)

			if tt.wantErrMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				require.Empty(t, slug)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantSlug, slug)
		})
	}
}

func TestEnsureRemote_PassesRepoPathAndRemoteName(t *testing.T) {
	t.Parallel()

	var gotArgs []string
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		return slices.Contains(args.Args, "remote")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		gotArgs = append([]string{}, args.Args...)
		return exec.RunResult{
			Stdout: "git@github.com:o/r.git\n",
		}, nil
	})

	gitCli := git.NewCli(runner)
	slug, err := EnsureRemote(t.Context(), "/repo/path", "upstream", gitCli)
	require.NoError(t, err)
	require.Equal(t, "o/r", slug)

	// Sanity check the underlying git invocation targets the expected
	// repository path and remote name.
	require.Contains(t, gotArgs, "-C")
	require.Contains(t, gotArgs, "/repo/path")
	require.Contains(t, gotArgs, "get-url")
	require.Contains(t, gotArgs, "upstream")
}
