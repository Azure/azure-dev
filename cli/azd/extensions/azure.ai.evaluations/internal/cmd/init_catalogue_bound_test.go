// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bound is the behavior. An unreachable endpoint returns before the bound
// whether or not there is one, so only the deadline itself can show it exists.
func TestCatalogueContext_CarriesTheBound(t *testing.T) {
	t.Parallel()

	start := time.Now()
	ctx, cancel := catalogueContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "without a deadline a hung transport holds init open forever")
	assert.WithinDuration(t, start.Add(builtinCatalogueTimeout), deadline, time.Second)
}

// Derived from the caller's context, not detached from it: a reader who
// interrupts has answered, and a detached lookup would go on waiting.
func TestCatalogueContext_StopsWithItsParent(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := catalogueContext(parent)
	defer cancel()

	cancelParent()

	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("the lookup outlived the command that asked for it")
	}
}

// An interrupt during the lookup used to be collapsed into "the catalogue said
// nothing", and init carried on until an unrelated step failed -- so a Ctrl-C
// was reported as "no azd project".
func TestInitStopsWhenTheReaderInterruptsDuringTheCatalogueLookup(t *testing.T) {
	t.Setenv("AZD_SERVER", "")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{Use: "init"}
	cmd.SetContext(ctx)

	// The catalogue answers nothing, which is exactly what an interrupted
	// lookup also does. What separates them is the command's own context.
	restore := knownBuiltinEvaluators
	knownBuiltinEvaluators = func(context.Context) []string {
		cancel()
		return nil
	}
	t.Cleanup(func() { knownBuiltinEvaluators = restore })

	action := &initAction{
		cmd:   cmd,
		flags: &initFlags{evaluators: []string{"builtin.coherence"}},
	}

	err := action.Run()

	require.Error(t, err, "an interrupt is an answer, not a step to carry on past")
	assert.ErrorIs(t, err, context.Canceled,
		"and it has to be reported as the interrupt it was")
}

// The catalogue lookup has to stay where init can still refuse on it. A caller
// that reads the answer and never asks the command's context reopens the bug
// above, so the orchestration is pinned rather than only its parts.
func TestInitRunChecksItsContextAfterTheCatalogueLookup(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "init.go", nil, 0)
	require.NoError(t, err)

	var body *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Run" && fn.Recv != nil {
			if receiverIsInitAction(fn) {
				body = fn.Body
			}
		}
		return body == nil
	})
	require.NotNil(t, body, "initAction.Run was renamed or moved")

	asks, checks := false, false
	ast.Inspect(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			switch id.Name {
			case "knownBuiltinEvaluators":
				asks = true
			case "Err":
				checks = true
			}
		}
		return true
	})

	assert.True(t, asks, "init no longer asks the catalogue")
	assert.True(t, checks,
		"init must ask its own context whether the reader interrupted, "+
			"or a Ctrl-C reads as whatever step fails next")
}

// receiverIsInitAction reports a method on *initAction.
func receiverIsInitAction(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "initAction"
}
