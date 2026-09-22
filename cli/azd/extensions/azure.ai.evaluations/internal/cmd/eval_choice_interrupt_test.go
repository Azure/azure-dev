// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// promptingIn is a command whose context a test controls.
func promptingIn(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{Use: "create"}
	cmd.SetContext(ctx)
	return cmd
}

// Interrupting the command is not closing the picker. Both cancel the prompt,
// so the picker's error looks the same either way -- but reporting an
// interrupt as an answer exits 0, and a script that was killed mid-run then
// reads as one that succeeded.
func TestSelectionOutcome_AnInterruptedCommandIsNotAClosedPicker(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := selectionOutcome(promptingIn(ctx), "declared", context.Canceled)

	require.Error(t, err, "an interrupt must not exit 0")
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, errEvalSelectionCancelled,
		"an interrupt is not the reader answering the picker")
	assert.Empty(t, got)
}

// The same holds however the prompt reported it: what decides is the command's
// own context, not the shape of the error coming back.
func TestSelectionOutcome_AnInterruptWinsOverThePromptsOwnError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, promptErr := range []error{
		status.Error(codes.Canceled, "user cancelled"),
		status.Error(codes.Unavailable, "no server"),
		errors.New("something else"),
	} {
		_, err := selectionOutcome(promptingIn(ctx), "declared", promptErr)
		assert.ErrorIs(t, err, context.Canceled)
	}
}

// With the command still running, a closed prompt is the answer it always was.
func TestSelectionOutcome_AClosedPickerIsStillAnAnswer(t *testing.T) {
	t.Parallel()

	got, err := selectionOutcome(
		promptingIn(t.Context()), "declared", status.Error(codes.Canceled, "user cancelled"))

	assert.ErrorIs(t, err, errEvalSelectionCancelled)
	assert.Empty(t, got, "nothing was selected")
}

// Every other reason the prompt could not run still leaves the name alone, so
// the caller's own error keeps describing what is missing.
func TestSelectionOutcome_APromptThatCouldNotRunChangesNothing(t *testing.T) {
	t.Parallel()

	for _, promptErr := range []error{
		status.Error(codes.Unavailable, "no server"),
		errors.New("something else"),
	} {
		got, err := selectionOutcome(promptingIn(t.Context()), "declared", promptErr)
		require.NoError(t, err)
		assert.Equal(t, "declared", got)
	}
}

// A command cobra has not executed yet has no context, and gRPC panics on a
// nil one -- so reading it must go through the same guard the prompt does.
func TestSelectionOutcome_SurvivesACommandWithNoContext(t *testing.T) {
	t.Parallel()

	got, err := selectionOutcome(&cobra.Command{Use: "create"}, "declared", errors.New("x"))

	require.NoError(t, err)
	assert.Equal(t, "declared", got)
}

// The picker has to keep reading its failure through the function above. An
// interrupt and a close arrive as the same error, so a call site that inspects
// the error alone cannot tell them apart.
func TestChooseEvalReadsItsPromptFailureThroughSelectionOutcome(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "eval_choice.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	delegates := false
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "chooseEval" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			if id, ok := inner.(*ast.Ident); ok && id.Name == "selectionOutcome" {
				delegates = true
			}
			return true
		})
		return false
	})

	assert.True(t, delegates,
		"chooseEval must read a failed prompt through selectionOutcome, "+
			"or an interrupted command goes back to exiting 0")
}
