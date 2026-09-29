// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A path decoded out of the configuration is rebased once, by core.
//
// `resolveEvalRefs` names `file` and `source` with WithPathKeys, so core
// anchors them to the root it is given. The deploy then has to join them from
// that same root. It used to join them against the service's own directory as
// well -- `baseDirUnder` -- which resolved `evals/datasets/rows.jsonl` under
// `<root>/evals`, and `azd up` reported every scaffolded dataset and every
// generated rubric as missing.
//
// Guarded at the source, because the wrong base still produces a plausible
// absolute path: nothing panics, nothing fails to compile, and the tests that
// resolve a declaration themselves pass whatever base they are handed. What
// gives it away is only that the file is not there.
//
// `baseDirUnder` keeps its own job -- it answers where a service lives, which
// is how AgentInstructionsFromProject finds the optimizer's baseline beside an
// agent's own file -- so this pins where it may be used, not that it exists.
func TestDeployResolvesDeclaredPathsFromTheProjectRoot(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service_target_eval.go", nil, parser.ParseComments)
	require.NoError(t, err)

	deploy := functionNamed(file, "Deploy")
	require.NotNil(t, deploy, "Deploy has to exist for this to be guarding anything")

	var offenders []string
	ast.Inspect(deploy, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "baseDirUnder" {
			return true
		}
		offenders = append(offenders, fset.Position(call.Pos()).String())
		return true
	})

	assert.Emptyf(t, offenders,
		"Deploy must resolve declared paths from projectRoot; core has already "+
			"rebased them onto it, so joining the service's directory as well "+
			"applies the rebase twice. Found baseDirUnder at: %s",
		strings.Join(offenders, ", "))
}

func functionNamed(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
