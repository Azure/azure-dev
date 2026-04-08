// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// newTestCli creates a Cli backed by a mock command runner for
// unit-testing individual methods without the full install flow.
func newTestCli(t *testing.T) (*Cli, *mocks.MockContext) {
	t.Helper()
	mockCtx := mocks.NewMockContext(t.Context())
	return &Cli{
		commandRunner: mockCtx.CommandRunner,
		path:          "gh",
	}, mockCtx
}

// respondOK is a shorthand for a mock that returns a fixed stdout.
func respondOK(
	mockCtx *mocks.MockContext,
	keyword string,
	stdout string,
) {
	mockCtx.CommandRunner.When(
		func(_ exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, keyword)
		},
	).Respond(exec.NewRunResult(0, stdout, ""))
}

// respondErr is a shorthand for a mock that returns an error.
func respondErr(
	mockCtx *mocks.MockContext,
	keyword string,
) {
	mockCtx.CommandRunner.When(
		func(_ exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, keyword)
		},
	).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "failure"),
			errors.New("exit 1")
	})
}

// --------------- NewGitHubCli / CheckInstalled ---------------

func TestNewGitHubCliCreation(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	cli := NewGitHubCli(mockCtx.Console, mockCtx.CommandRunner)
	require.NotNil(t, cli)
	require.Equal(t, "GitHub CLI", cli.Name())
}

func TestCheckInstalled_ViaOverride(t *testing.T) {
	t.Setenv("AZD_GH_TOOL_PATH", "/usr/bin/gh")

	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, _ string) bool {
			return len(args.Args) == 1 && args.Args[0] == "--version"
		},
	).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf(
			"gh version %s (2024-01-01)", Version.String(),
		),
		"",
	))

	cli := NewGitHubCli(mockCtx.Console, mockCtx.CommandRunner)
	err := cli.CheckInstalled(t.Context())
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/gh", cli.BinaryPath())
}

// --------------- GetAuthStatus ---------------

func TestGetAuthStatus_LoggedIn(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	respondOK(mockCtx, "auth status", "")

	status, err := cli.GetAuthStatus(t.Context(), "github.com")
	require.NoError(t, err)
	require.True(t, status.LoggedIn)
}

func TestGetAuthStatus_OtherError(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	mockCtx.CommandRunner.When(
		func(_ exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "auth status")
		},
	).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "unexpected error"),
			errors.New("exit 1")
	})

	_, err := cli.GetAuthStatus(t.Context(), "github.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed running gh auth status")
}

// --------------- Login ---------------

func TestLogin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		succeed bool
	}{
		{name: "Success", succeed: true},
		{name: "Error", succeed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cli, mockCtx := newTestCli(t)
			if tt.succeed {
				respondOK(mockCtx, "auth login", "")
			} else {
				respondErr(mockCtx, "auth login")
			}

			err := cli.Login(t.Context(), "github.com")
			if tt.succeed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(
					t, err.Error(), "failed running gh auth login",
				)
			}
		})
	}
}

// --------------- ApiCall ---------------

func TestApiCall(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "api", `{"ok":true}`)

		out, err := cli.ApiCall(
			t.Context(), "github.com", "/repos/o/r",
			ApiCallOptions{},
		)
		require.NoError(t, err)
		require.Equal(t, `{"ok":true}`, out)
	})

	t.Run("WithHeaders", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		// Verify the -H flag is present.
		mockCtx.CommandRunner.When(
			func(_ exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "api") &&
					strings.Contains(cmd, "-H")
			},
		).Respond(exec.NewRunResult(0, "raw", ""))

		out, err := cli.ApiCall(
			t.Context(), "github.com", "/repos/o/r/contents/f",
			ApiCallOptions{
				Headers: []string{
					"Accept: application/vnd.github.raw",
				},
			},
		)
		require.NoError(t, err)
		require.Equal(t, "raw", out)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "api")

		_, err := cli.ApiCall(
			t.Context(), "github.com", "/bad",
			ApiCallOptions{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed running gh api")
	})
}

// --------------- Secrets ---------------

func TestListSecrets(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(
			mockCtx, "secret list",
			"SECRET_A\tUpdated\nSECRET_B\tUpdated\n",
		)

		secrets, err := cli.ListSecrets(t.Context(), "o/r")
		require.NoError(t, err)
		require.Equal(t, []string{"SECRET_A", "SECRET_B"}, secrets)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "secret list")

		_, err := cli.ListSecrets(t.Context(), "o/r")
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "failed running gh secret list",
		)
	})
}

func TestSetSecret(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "secret set", "")

		err := cli.SetSecret(t.Context(), "o/r", "KEY", "val")
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "secret set")

		err := cli.SetSecret(t.Context(), "o/r", "KEY", "val")
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "failed running gh secret set",
		)
	})
}

func TestDeleteSecret(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "secret delete", "")

		err := cli.DeleteSecret(t.Context(), "o/r", "KEY")
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "secret delete")

		err := cli.DeleteSecret(t.Context(), "o/r", "KEY")
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "failed running gh secret delete",
		)
	})
}

// --------------- Variables ---------------

func TestListVariables(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(
			mockCtx, "variable list",
			"VAR_A\tval_a\tUpdated\nVAR_B\tval_b\tUpdated\n",
		)

		vars, err := cli.ListVariables(
			t.Context(), "o/r", nil,
		)
		require.NoError(t, err)
		require.Equal(t, map[string]string{
			"VAR_A": "val_a",
			"VAR_B": "val_b",
		}, vars)
	})

	t.Run("WithEnvironment", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		mockCtx.CommandRunner.When(
			func(_ exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "variable list") &&
					strings.Contains(cmd, "--env")
			},
		).Respond(exec.NewRunResult(
			0, "K\tV\n", "",
		))

		vars, err := cli.ListVariables(
			t.Context(), "o/r",
			&ListVariablesOptions{Environment: "prod"},
		)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"K": "V"}, vars)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "variable list")

		_, err := cli.ListVariables(
			t.Context(), "o/r", nil,
		)
		require.Error(t, err)
	})
}

func TestSetVariable(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "variable set", "")

		err := cli.SetVariable(
			t.Context(), "o/r", "K", "V", nil,
		)
		require.NoError(t, err)
	})

	t.Run("WithEnvironment", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		mockCtx.CommandRunner.When(
			func(_ exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "variable set") &&
					strings.Contains(cmd, "--env")
			},
		).Respond(exec.NewRunResult(0, "", ""))

		err := cli.SetVariable(
			t.Context(), "o/r", "K", "V",
			&SetVariableOptions{Environment: "prod"},
		)
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "variable set")

		err := cli.SetVariable(
			t.Context(), "o/r", "K", "V", nil,
		)
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "failed running gh variable set",
		)
	})
}

func TestDeleteVariable(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "variable delete", "")

		err := cli.DeleteVariable(t.Context(), "o/r", "K")
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "variable delete")

		err := cli.DeleteVariable(t.Context(), "o/r", "K")
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "failed running gh variable delete",
		)
	})
}

// --------------- Repositories ---------------

func TestListRepositories(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "repo list", `[{
			"nameWithOwner":"o/r",
			"url":"https://github.com/o/r",
			"sshUrl":"git@github.com:o/r.git"
		}]`)

		repos, err := cli.ListRepositories(t.Context())
		require.NoError(t, err)
		require.Len(t, repos, 1)
		require.Equal(t, "o/r", repos[0].NameWithOwner)
		require.Equal(t, "https://github.com/o/r", repos[0].HttpsUrl)
	})

	t.Run("BadJSON", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "repo list", "not json")

		_, err := cli.ListRepositories(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "could not unmarshal")
	})

	t.Run("RunError", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "repo list")

		_, err := cli.ListRepositories(t.Context())
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "failed running gh repo list",
		)
	})
}

func TestViewRepository(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "repo view", `{
			"nameWithOwner":"o/r",
			"url":"https://github.com/o/r",
			"sshUrl":"git@github.com:o/r.git"
		}`)

		repo, err := cli.ViewRepository(t.Context(), "o/r")
		require.NoError(t, err)
		require.Equal(t, "o/r", repo.NameWithOwner)
	})

	t.Run("BadJSON", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "repo view", "{bad}")

		_, err := cli.ViewRepository(t.Context(), "o/r")
		require.Error(t, err)
		require.Contains(t, err.Error(), "could not unmarshal")
	})

	t.Run("RunError", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "repo view")

		_, err := cli.ViewRepository(t.Context(), "o/r")
		require.Error(t, err)
	})
}

func TestCreatePrivateRepository(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "repo create", "")

		err := cli.CreatePrivateRepository(t.Context(), "o/r")
		require.NoError(t, err)
	})

	t.Run("NameInUse", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		mockCtx.CommandRunner.When(
			func(_ exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "repo create")
			},
		).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(
				1, "",
				"GraphQL: Name already exists on "+
					"this account (createRepository)",
			), errors.New("exit 1")
		})

		err := cli.CreatePrivateRepository(t.Context(), "o/r")
		require.ErrorIs(t, err, ErrRepositoryNameInUse)
	})

	t.Run("OtherError", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "repo create")

		err := cli.CreatePrivateRepository(t.Context(), "o/r")
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "failed running gh repo create",
		)
	})
}

// --------------- Config / Actions / Environments ---------------

func TestGetGitProtocolType(t *testing.T) {
	t.Parallel()
	t.Run("SSH", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "config get", "ssh\n")

		proto, err := cli.GetGitProtocolType(t.Context())
		require.NoError(t, err)
		require.Equal(t, "ssh", proto)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "config get")

		_, err := cli.GetGitProtocolType(t.Context())
		require.Error(t, err)
		require.Contains(
			t, err.Error(),
			"failed running gh config get git_protocol",
		)
	})
}

func TestGitHubActionsExists(t *testing.T) {
	t.Parallel()
	t.Run("Exists", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "actions/workflows", `{"total_count":3}`)

		exists, err := cli.GitHubActionsExists(t.Context(), "o/r")
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("NotExists", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "actions/workflows", `{"total_count":0}`)

		exists, err := cli.GitHubActionsExists(t.Context(), "o/r")
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("BadJSON", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "actions/workflows", "not json")

		_, err := cli.GitHubActionsExists(t.Context(), "o/r")
		require.Error(t, err)
		require.Contains(t, err.Error(), "could not unmarshal")
	})

	t.Run("RunError", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "actions/workflows")

		_, err := cli.GitHubActionsExists(t.Context(), "o/r")
		require.Error(t, err)
		require.Contains(
			t, err.Error(), "getting github actions",
		)
	})
}

func TestCreateEnvironmentIfNotExist(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "environments", "")

		err := cli.CreateEnvironmentIfNotExist(
			t.Context(), "o/r", "prod",
		)
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "environments")

		err := cli.CreateEnvironmentIfNotExist(
			t.Context(), "o/r", "prod",
		)
		require.Error(t, err)
	})
}

func TestDeleteEnvironment(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondOK(mockCtx, "environments", "")

		err := cli.DeleteEnvironment(
			t.Context(), "o/r", "prod",
		)
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		cli, mockCtx := newTestCli(t)
		respondErr(mockCtx, "environments")

		err := cli.DeleteEnvironment(
			t.Context(), "o/r", "prod",
		)
		require.Error(t, err)
	})
}

// --------------- run() interceptor ---------------

func TestRunInterceptor_NotLoggedIn(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	mockCtx.CommandRunner.When(
		func(_ exec.RunArgs, _ string) bool { return true },
	).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(
			1, "",
			"To authenticate, please run `gh auth login`.",
		), errors.New("exit 1")
	})

	// Use DeleteSecret as a simple method that goes through run().
	err := cli.DeleteSecret(t.Context(), "o/r", "K")
	require.ErrorIs(t, err, ErrGitHubCliNotLoggedIn)
}

func TestRunInterceptor_UserNotAuthorized(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	mockCtx.CommandRunner.When(
		func(_ exec.RunArgs, _ string) bool { return true },
	).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(
			1, "",
			"HTTP 403: Resource not accessible by integration",
		), errors.New("exit 1")
	})

	err := cli.DeleteSecret(t.Context(), "o/r", "K")
	require.ErrorIs(t, err, ErrUserNotAuthorized)
}

// --------------- newRunArgs Codespaces ---------------

func TestNewRunArgs_Codespaces(t *testing.T) {
	t.Setenv("CODESPACES", "true")
	cli := &Cli{path: "gh"}
	args := cli.newRunArgs("auth", "login")

	require.Contains(t, args.Env, "GITHUB_TOKEN=")
	require.Contains(t, args.Env, "GH_TOKEN=")
}

func TestNewRunArgs_NotCodespaces(t *testing.T) {
	t.Setenv("CODESPACES", "")
	cli := &Cli{path: "gh"}
	args := cli.newRunArgs("auth", "login")

	require.Empty(t, args.Env)
}

// --------------- expectedVersionInstalled ---------------

func TestExpectedVersionInstalled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stdout string
		runErr error
		want   bool
	}{
		{
			name: "Matches",
			stdout: fmt.Sprintf(
				"gh version %s (2024-01-01)", Version.String(),
			),
			want: true,
		},
		{
			name:   "OlderVersion",
			stdout: "gh version 2.20.0 (2024-01-01)",
			want:   false,
		},
		{
			name:   "CommandError",
			runErr: errors.New("not found"),
			want:   false,
		},
		{
			name:   "GarbageOutput",
			stdout: "no version info here",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockCtx := mocks.NewMockContext(t.Context())
			mockCtx.CommandRunner.When(
				func(args exec.RunArgs, _ string) bool {
					return len(args.Args) == 1 &&
						args.Args[0] == "--version"
				},
			).RespondFn(
				func(_ exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(0, tt.stdout, ""),
						tt.runErr
				},
			)

			got := expectedVersionInstalled(
				t.Context(), mockCtx.CommandRunner, "/bin/gh",
			)
			require.Equal(t, tt.want, got)
		})
	}
}

// --------------- extractVersion ---------------

func TestExtractVersion_RunError(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	mockCtx.CommandRunner.When(
		func(_ exec.RunArgs, _ string) bool { return true },
	).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", ""),
			errors.New("not found")
	})

	_, err := cli.extractVersion(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "error running gh --version")
}

func TestExtractVersion_NoMatch(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	respondOK(mockCtx, "--version", "no version here")

	_, err := cli.extractVersion(t.Context())
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "could not extract version from output",
	)
}

// --------------- logVersion ---------------

func TestLogVersion_Error(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	mockCtx.CommandRunner.When(
		func(_ exec.RunArgs, _ string) bool { return true },
	).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", ""),
			errors.New("not found")
	})

	// logVersion logs the error but doesn't panic or return it.
	cli.logVersion(t.Context())
}
