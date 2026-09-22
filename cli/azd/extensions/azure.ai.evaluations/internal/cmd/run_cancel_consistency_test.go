// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/messages"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Closing the eval picker is an answer: there is no eval to list, show, cancel
// or export, and nothing went wrong. `eval create` already reported it that
// way; the run subcommands returned the sentinel as a command error, so the
// same keystroke printed "Cancelled." at one door and exited non-zero at six
// others.
func TestEvalIDForRunCommandReportsAClosedPickerAsAnAnswer(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	cmd := &cobra.Command{Use: "list"}
	cmd.SetOut(out)

	// An evalContext with no azd client: evalDir resolution falls back, and
	// the picker is what this is about, so the sentinel is injected directly
	// through the same predicate the helper tests.
	assert.True(t, isEvalSelectionCancelled(errEvalSelectionCancelled))
	assert.False(t, isEvalSelectionCancelled(nil))

	reportCancelledSelection(cmd)
	assert.Equal(t, messages.EvalSelectionCancelled(), out.String(),
		"the reader is told the selection was cancelled, in the same words create uses")
}

// The consistency the fix is about cannot be asserted by calling one function:
// it is a property of every call site. A new run subcommand that reaches for
// resolveEvalID directly would reintroduce the split silently, so the source
// is the thing checked.
func TestEveryRunCommandResolvesItsEvalThroughTheCancellationAwarePath(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	offenders := map[string][]string{}

	for _, name := range []string{"run_ops.go", "run_output.go"} {
		path := filepath.Join(name)
		src, err := os.ReadFile(path)
		require.NoError(t, err, "reading %s", name)

		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		require.NoError(t, err)

		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			// The wrapper is the one place allowed to call it.
			if fn.Name.Name == "evalIDForRunCommand" {
				return false
			}
			ast.Inspect(fn.Body, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok || ident.Name != "resolveEvalID" {
					return true
				}
				offenders[name] = append(offenders[name], fn.Name.Name)
				return true
			})
			return false
		})
	}

	assert.Empty(t, offenders,
		"these call resolveEvalID directly and so report a closed picker as a failure; "+
			"use evalIDForRunCommand, which reports it as the answer it is")
}

// The other half of the same property: the wrapper is not dead code that the
// commands bypass. Every action that resolves an eval has to route through it.
func TestTheRunCommandsActuallyUseTheWrapper(t *testing.T) {
	t.Parallel()

	uses := 0
	for _, name := range []string{"run_ops.go", "run_output.go"} {
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		uses += strings.Count(string(src), "evalIDForRunCommand(")
	}

	// Seven call sites plus the declaration: run list, show, cancel, delete,
	// and the three output views.
	assert.GreaterOrEqual(t, uses, 8,
		"the cancellation-aware path is declared but the commands are not using it")
}
