// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package git

import (
	"errors"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestName(t *testing.T) {
	cli := NewCli(nil)
	require.Equal(t, "git CLI", cli.Name())
}

func TestInstallUrl(t *testing.T) {
	cli := NewCli(nil)
	require.Equal(
		t, "https://git-scm.com/downloads", cli.InstallUrl(),
	)
}

func TestGetRemoteUrl(t *testing.T) {
	tests := []struct {
		name       string
		stdout     string
		stderr     string
		err        error
		wantURL    string
		wantErr    error
		wantErrMsg string
	}{
		{
			name:    "Success",
			stdout:  "https://github.com/user/repo.git\n",
			wantURL: "https://github.com/user/repo.git",
		},
		{
			name:    "SuccessSSH",
			stdout:  "  git@github.com:user/repo.git  \n",
			wantURL: "git@github.com:user/repo.git",
		},
		{
			name:    "NoSuchRemote",
			stderr:  "fatal: No such remote 'upstream'",
			err:     errors.New("exit code: 2"),
			wantErr: ErrNoSuchRemote,
		},
		{
			name:    "ErrorNoSuchRemote",
			stderr:  "error: No such remote 'upstream'",
			err:     errors.New("exit code: 2"),
			wantErr: ErrNoSuchRemote,
		},
		{
			name:    "NotAGitRepo",
			stderr:  "fatal: not a git repository",
			err:     errors.New("exit code: 128"),
			wantErr: ErrNotRepository,
		},
		{
			name:       "OtherError",
			stderr:     "some other error",
			err:        errors.New("exit code: 1"),
			wantErrMsg: "failed to get remote url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return slices.Contains(args.Args, "remote")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.RunResult{
					Stdout: tt.stdout,
					Stderr: tt.stderr,
				}, tt.err
			})

			cli := NewCli(runner)
			url, err := cli.GetRemoteUrl(
				t.Context(), "/repo", "origin",
			)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			if tt.wantErrMsg != "" {
				require.ErrorContains(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantURL, url)
		})
	}
}

func TestGetCurrentBranch(t *testing.T) {
	tests := []struct {
		name       string
		stdout     string
		stderr     string
		err        error
		wantBranch string
		wantErr    error
	}{
		{
			name:       "Success",
			stdout:     "main\n",
			wantBranch: "main",
		},
		{
			name:       "FeatureBranch",
			stdout:     "  feature/my-branch  \n",
			wantBranch: "feature/my-branch",
		},
		{
			name:    "NotARepo",
			stderr:  "fatal: not a git repository",
			err:     errors.New("exit code: 128"),
			wantErr: ErrNotRepository,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return slices.Contains(args.Args, "branch")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.RunResult{
					Stdout: tt.stdout,
					Stderr: tt.stderr,
				}, tt.err
			})

			cli := NewCli(runner)
			branch, err := cli.GetCurrentBranch(
				t.Context(), "/repo",
			)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantBranch, branch)
		})
	}
}

func TestGetRepoRoot(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		stderr   string
		err      error
		wantRoot string
		wantErr  error
	}{
		{
			name:     "Success",
			stdout:   "/home/user/project\n",
			wantRoot: "/home/user/project",
		},
		{
			name:    "NotARepo",
			stderr:  "fatal: not a git repository (or any parent)",
			err:     errors.New("exit code: 128"),
			wantErr: ErrNotRepository,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return slices.Contains(args.Args, "rev-parse")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.RunResult{
					Stdout: tt.stdout,
					Stderr: tt.stderr,
				}, tt.err
			})

			cli := NewCli(runner)
			root, err := cli.GetRepoRoot(
				t.Context(), "/repo",
			)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantRoot, root)
		})
	}
}

func TestShallowClone(t *testing.T) {
	tests := []struct {
		name    string
		branch  string
		runErr  error
		wantErr bool
	}{
		{
			name:   "WithBranch",
			branch: "main",
		},
		{
			name:   "WithoutBranch",
			branch: "",
		},
		{
			name:    "Error",
			branch:  "main",
			runErr:  errors.New("clone failed"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var capturedArgs exec.RunArgs
			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return slices.Contains(args.Args, "clone")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				capturedArgs = args
				return exec.RunResult{}, tt.runErr
			})

			cli := NewCli(runner)
			err := cli.ShallowClone(
				t.Context(),
				"https://github.com/user/repo",
				tt.branch,
				"/target",
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Contains(t, capturedArgs.Args, "clone")
			require.Contains(t, capturedArgs.Args, "--depth")
			require.Contains(t, capturedArgs.Args, "1")
			require.Contains(t, capturedArgs.Args, "/target")

			if tt.branch != "" {
				require.Contains(
					t, capturedArgs.Args, "--branch",
				)
				require.Contains(
					t, capturedArgs.Args, tt.branch,
				)
			} else {
				require.NotContains(
					t, capturedArgs.Args, "--branch",
				)
			}
		})
	}
}

func TestInitRepo(t *testing.T) {
	tests := []struct {
		name      string
		initErr   error
		checkErr  error
		wantErr   bool
		wantErrMs string
	}{
		{
			name: "Success",
		},
		{
			name:    "InitFails",
			initErr: errors.New("init failed"),
			wantErr: true,
		},
		{
			name:     "CheckoutFails",
			checkErr: errors.New("checkout failed"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return slices.Contains(args.Args, "init")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.RunResult{}, tt.initErr
			})
			runner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return slices.Contains(args.Args, "checkout")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.RunResult{}, tt.checkErr
			})

			cli := NewCli(runner)
			err := cli.InitRepo(t.Context(), "/repo")

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestIsUntrackedFile(t *testing.T) {
	tests := []struct {
		name       string
		stdout     string
		err        error
		wantResult bool
		wantErr    bool
	}{
		{
			name:       "Untracked",
			stdout:     "?? newfile.txt\nuntracked files present",
			wantResult: true,
		},
		{
			name:       "NewFile",
			stdout:     "A  new file added",
			wantResult: true,
		},
		{
			name:       "Tracked",
			stdout:     " M modified.go",
			wantResult: false,
		},
		{
			name:       "Empty",
			stdout:     "",
			wantResult: false,
		},
		{
			name:    "Error",
			err:     errors.New("git error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return slices.Contains(args.Args, "status")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.RunResult{
					Stdout: tt.stdout,
				}, tt.err
			})

			cli := NewCli(runner)
			result, err := cli.IsUntrackedFile(
				t.Context(), "/repo", "file.txt",
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantResult, result)
		})
	}
}

func TestAddRemote(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var captured exec.RunArgs
		runner := mockexec.NewMockCommandRunner()
		runner.When(func(
			args exec.RunArgs, _ string,
		) bool {
			return slices.Contains(args.Args, "remote") &&
				slices.Contains(args.Args, "add")
		}).RespondFn(func(
			args exec.RunArgs,
		) (exec.RunResult, error) {
			captured = args
			return exec.RunResult{}, nil
		})

		cli := NewCli(runner)
		err := cli.AddRemote(
			t.Context(), "/repo", "origin",
			"https://github.com/user/repo",
		)
		require.NoError(t, err)
		require.Contains(t, captured.Args, "origin")
		require.Contains(
			t, captured.Args,
			"https://github.com/user/repo",
		)
	})

	t.Run("Error", func(t *testing.T) {
		runner := mockexec.NewMockCommandRunner()
		runner.When(func(
			args exec.RunArgs, _ string,
		) bool {
			return true
		}).RespondFn(func(
			args exec.RunArgs,
		) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("remote add failed")
		})

		cli := NewCli(runner)
		err := cli.AddRemote(
			t.Context(), "/repo", "origin", "url",
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to add remote")
	})
}

func TestUpdateRemote(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "set-url")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := NewCli(runner)
	err := cli.UpdateRemote(
		t.Context(), "/repo", "origin", "https://new.url",
	)
	require.NoError(t, err)
	require.Contains(t, captured.Args, "set-url")
	require.Contains(t, captured.Args, "https://new.url")
}

func TestCommit(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "commit")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := NewCli(runner)
	err := cli.Commit(t.Context(), "/repo", "test commit")
	require.NoError(t, err)
	require.Contains(t, captured.Args, "--allow-empty")
	require.Contains(t, captured.Args, "-m")
	require.Contains(t, captured.Args, "test commit")
}

func TestPushUpstream(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "push")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := NewCli(runner)
	err := cli.PushUpstream(
		t.Context(), "/repo", "origin", "main",
	)
	require.NoError(t, err)
	require.Contains(t, captured.Args, "--set-upstream")
	require.Contains(t, captured.Args, "--quiet")
	require.Contains(t, captured.Args, "origin")
	require.Contains(t, captured.Args, "main")
}

func TestListStagedFiles(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "ls-files")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout: "100644 abc file.go\n",
		}, nil
	})

	cli := NewCli(runner)
	out, err := cli.ListStagedFiles(t.Context(), "/repo")
	require.NoError(t, err)
	require.Contains(t, out, "file.go")
}

func TestAddFile(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "add")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := NewCli(runner)
	err := cli.AddFile(t.Context(), "/repo", ".")
	require.NoError(t, err)
	require.Contains(t, captured.Args, "add")
	require.Contains(t, captured.Args, ".")
}

func TestSetCredentialStore(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "config")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := NewCli(runner)
	err := cli.SetCredentialStore(t.Context(), "/repo")
	require.NoError(t, err)
	require.Contains(t, captured.Args, "credential.helper")
	require.Contains(t, captured.Args, "store")
}

func TestAddFileExecPermission(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "update-index")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := NewCli(runner)
	err := cli.AddFileExecPermission(
		t.Context(), "/repo", "script.sh",
	)
	require.NoError(t, err)
	require.Contains(t, captured.Args, "--chmod=+x")
	require.Contains(t, captured.Args, "script.sh")
}
