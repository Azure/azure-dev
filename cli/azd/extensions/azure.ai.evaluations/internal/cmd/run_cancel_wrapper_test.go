// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"azureaieval/internal/messages"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answering is a command whose output a test can read back.
func answering(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "list"}
	cmd.SetOut(&bytes.Buffer{})
	return cmd
}

// said reads back what a command wrote.
func said(cmd *cobra.Command) string {
	return cmd.OutOrStdout().(*bytes.Buffer).String()
}

// Closing the picker is an answer: nothing failed, and there is nothing left to
// list, show, cancel or export. The run subcommands used to return the sentinel
// as a command error, so one keystroke printed "Cancelled." at `eval create`
// and exited non-zero at the other seven.
func TestAnsweredEvalID_ACancelledPickerIsNotAFailure(t *testing.T) {
	t.Parallel()

	cmd := answering(t)

	evalID, carryOn, err := answeredEvalID(cmd, "", errEvalSelectionCancelled)

	require.NoError(t, err, "a deliberate answer must not exit non-zero")
	assert.False(t, carryOn, "there is no eval to act on")
	assert.Empty(t, evalID)
	assert.Equal(t, messages.EvalSelectionCancelled(), said(cmd),
		"and the reader is told, in the same words create uses")
}

// The sentinel is recognized through the error chain, not by identity: it
// travels back up through the resolution that called the picker.
func TestAnsweredEvalID_RecognizesAWrappedSentinel(t *testing.T) {
	t.Parallel()

	wrapped := errors.Join(errors.New("reading the configuration"), errEvalSelectionCancelled)

	_, carryOn, err := answeredEvalID(answering(t), "", wrapped)

	require.NoError(t, err)
	assert.False(t, carryOn)
}

// Everything else is still a failure. Reporting a closed picker as an answer
// must not turn a project that cannot be read into a silent exit 0.
func TestAnsweredEvalID_LeavesEveryOtherErrorAlone(t *testing.T) {
	t.Parallel()

	failure := errors.New("no azd project")
	cmd := answering(t)

	_, carryOn, err := answeredEvalID(cmd, "", failure)

	require.ErrorIs(t, err, failure)
	assert.False(t, carryOn)
	assert.Empty(t, said(cmd), "a failure is not a cancellation, and must not read as one")
}

// A resolved eval carries on, so this is not swallowing the normal path.
func TestAnsweredEvalID_APickedEvalCarriesOn(t *testing.T) {
	t.Parallel()

	cmd := answering(t)

	evalID, carryOn, err := answeredEvalID(cmd, "eval-123", nil)

	require.NoError(t, err)
	assert.True(t, carryOn)
	assert.Equal(t, "eval-123", evalID)
	assert.Empty(t, said(cmd))
}

// The wrapper the run commands call has to keep handing its resolution to the
// function above. Resolving needs a project, a configuration and a terminal, so
// the wrapper cannot be driven from a test -- but it can be held to delegating,
// which is the only thing in it a change could get wrong.
func TestEvalIDForRunCommandDelegatesToAnsweredEvalID(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "run_output.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	delegates := false
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "evalIDForRunCommand" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			if id, ok := inner.(*ast.Ident); ok && id.Name == "answeredEvalID" {
				delegates = true
			}
			return true
		})
		return false
	})

	assert.True(t, delegates,
		"evalIDForRunCommand must route its resolution through answeredEvalID, "+
			"or a closed picker goes back to exiting non-zero at the run commands")
}
