// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Closing the picker printed "(Cancelled)" and then an unrelated ambiguity
// error, which told a reader who had just declined to choose that they had
// failed to name something. ADO 5571322.
func TestCancelled_RecognisesAClosedPrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "grpc cancelled, which is what the prompt returns",
			err:  status.Error(codes.Canceled, "user cancelled"),
			want: true,
		},
		{
			name: "context cancelled",
			err:  context.Canceled,
			want: true,
		},
		{
			name: "a wrapped context cancellation",
			err:  errors.Join(errors.New("prompting"), context.Canceled),
			want: true,
		},
		{
			name: "a transport failure is not a cancellation",
			err:  status.Error(codes.Unavailable, "no server"),
			want: false,
		},
		{
			name: "an ordinary error is not a cancellation",
			err:  errors.New("something else"),
			want: false,
		},
		{
			name: "no error at all",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, cancelled(tt.err))
		})
	}
}

// A cancellation has to be distinguishable from every other reason the picker
// declines to answer, because those keep falling through to the caller's error
// and this one must not.
func TestErrEvalSelectionCancelled_IsDistinctFromAnEmptyChoice(t *testing.T) {
	t.Parallel()

	assert.True(t, errors.Is(errEvalSelectionCancelled, errEvalSelectionCancelled))
	assert.False(t, errors.Is(context.Canceled, errEvalSelectionCancelled),
		"a bare context cancellation is not this sentinel")
}

// Everything that resolves without asking must still resolve without an error,
// so the paths that always worked keep working.
func TestChooseEval_PathsThatNeverPromptReturnNoError(t *testing.T) {
	t.Parallel()

	t.Run("a name given", func(t *testing.T) {
		got, err := chooseEval(newEvalCreateCommand(), configWith("a", "b"), "b")
		require.NoError(t, err)
		assert.Equal(t, "b", got)
	})

	t.Run("a single declared eval", func(t *testing.T) {
		got, err := chooseEval(newEvalCreateCommand(), configWith("only"), "")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("no configuration", func(t *testing.T) {
		got, err := chooseEval(newEvalCreateCommand(), nil, "")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("--no-prompt still refuses rather than guesses", func(t *testing.T) {
		cmd := newEvalCreateCommand()
		cmd.Flags().Bool("no-prompt", true, "")

		got, err := chooseEval(cmd, configWith("a", "b"), "")

		require.NoError(t, err, "there is nobody to ask, which is not a cancellation")
		assert.Empty(t, got, "the caller's ambiguity error is still the right one here")
	})
}

// The detailed ambiguity error is what --no-prompt and every non-interactive
// path should keep getting. Only an interactive cancellation replaces it.
func TestSeveralEvalsErrorSurvivesForNonInteractivePaths(t *testing.T) {
	t.Parallel()

	cfg := configWith("obs-trace-eval", "obs-eval")
	cmd := newEvalCreateCommand()
	cmd.Flags().Bool("no-prompt", true, "")

	chosen, chooseErr := chooseEval(cmd, cfg, "")
	require.NoError(t, chooseErr)

	_, err := cfg.Eval(chosen)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "obs-trace-eval")
	assert.Contains(t, err.Error(), "obs-eval")
	assert.Contains(t, err.Error(), "--eval")
}
