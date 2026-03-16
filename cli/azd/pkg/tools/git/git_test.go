// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package git

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestGetStatus(t *testing.T) {
	tests := []struct {
		name       string
		stdout     string
		stderr     string
		exitCode   int
		err        error
		wantStatus string
		wantErr    error
	}{
		{
			name:       "CleanRepo",
			stdout:     "",
			exitCode:   0,
			wantStatus: "",
		},
		{
			name:       "DirtyRepo_ModifiedFile",
			stdout:     " M file.go\n",
			exitCode:   0,
			wantStatus: "M file.go",
		},
		{
			name:       "DirtyRepo_MultipleChanges",
			stdout:     " M file.go\n?? newfile.txt\nA  staged.go\n",
			exitCode:   0,
			wantStatus: "M file.go\n?? newfile.txt\nA  staged.go",
		},
		{
			name:    "NotAGitRepo",
			stderr:  "fatal: not a git repository (or any of the parent directories): .git",
			err:     errors.New("exit code: 128"),
			wantErr: ErrNotRepository,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmdRunner := mockexec.NewMockCommandRunner()
			cmdRunner.When(func(args exec.RunArgs, command string) bool {
				return slices.Contains(args.Args, "status") &&
					slices.Contains(args.Args, "--porcelain")
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{
					ExitCode: tt.exitCode,
					Stdout:   tt.stdout,
					Stderr:   tt.stderr,
				}, tt.err
			})

			cli := NewCli(cmdRunner)
			status, err := cli.GetStatus(t.Context(), ".")

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantStatus, status)
			}
		})
	}
}

func TestIsDirty(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		stderr    string
		exitCode  int
		err       error
		wantDirty bool
		wantErr   error
	}{
		{
			name:      "CleanRepo",
			stdout:    "",
			exitCode:  0,
			wantDirty: false,
		},
		{
			name:      "DirtyRepo",
			stdout:    " M file.go\n",
			exitCode:  0,
			wantDirty: true,
		},
		{
			name:    "NotAGitRepo",
			stderr:  "fatal: not a git repository (or any of the parent directories): .git",
			err:     errors.New("exit code: 128"),
			wantErr: ErrNotRepository,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmdRunner := mockexec.NewMockCommandRunner()
			cmdRunner.When(func(args exec.RunArgs, command string) bool {
				return slices.Contains(args.Args, "status") &&
					slices.Contains(args.Args, "--porcelain")
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{
					ExitCode: tt.exitCode,
					Stdout:   tt.stdout,
					Stderr:   tt.stderr,
				}, tt.err
			})

			cli := NewCli(cmdRunner)
			dirty, err := cli.IsDirty(context.Background(), ".")

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantDirty, dirty)
			}
		})
	}
}
