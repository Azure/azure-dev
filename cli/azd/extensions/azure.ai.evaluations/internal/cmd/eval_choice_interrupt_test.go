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

// Interrupting the command is not an explicit Cancel answer.
func TestSelectionOutcome_AnInterruptedCommandIsNotAClosedPicker(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := selectionOutcome(promptingIn(ctx), context.Canceled)

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
		nil,
		status.Error(codes.Canceled, "user cancelled"),
		status.Error(codes.Unavailable, "no server"),
		errors.New("something else"),
	} {
		_, err := selectionOutcome(promptingIn(ctx), promptErr)
		assert.ErrorIs(t, err, context.Canceled)
	}
}

// A host-side interrupt does not have to cancel the extension's context.
func TestSelectionOutcome_AHostInterruptIsNotAnAnswer(t *testing.T) {
	t.Parallel()

	got, err := selectionOutcome(
		promptingIn(t.Context()), status.Error(codes.Canceled, "user cancelled"))

	assert.Equal(t, codes.Canceled, status.Code(err))
	assert.NotErrorIs(t, err, errEvalSelectionCancelled)
	assert.Empty(t, got, "nothing was selected")
}

func TestSelectionOutcome_APromptFailureIsPreserved(t *testing.T) {
	t.Parallel()

	for _, promptErr := range []error{
		status.Error(codes.Unavailable, "no server"),
		errors.New("something else"),
	} {
		got, err := selectionOutcome(promptingIn(t.Context()), promptErr)
		require.ErrorIs(t, err, promptErr)
		assert.Empty(t, got)
	}
}

// A command cobra has not executed yet has no context, and gRPC panics on a
// nil one -- so reading it must go through the same guard the prompt does.
func TestSelectionOutcome_SurvivesACommandWithNoContext(t *testing.T) {
	t.Parallel()

	failure := errors.New("x")
	got, err := selectionOutcome(&cobra.Command{Use: "create"}, failure)

	require.ErrorIs(t, err, failure)
	assert.Empty(t, got)
}

// The picker must propagate its prompt failure, not reinterpret it as an answer.
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
