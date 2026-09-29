// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/messages"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two blob names that differ only in case are one file on Windows and macOS.
//
// The folder download wrote every entry with replace=true, so the second landed
// on the first and the count still reported both -- a dataset that quietly
// arrived short. Staging is created empty, so anything already at a path is the
// collision, whatever the filesystem's reason for folding them together.
func TestASecondEntryOnOnePathIsRefused(t *testing.T) {
	t.Parallel()

	staging := t.TempDir()
	taken := filepath.Join(staging, "rows.jsonl")
	require.NoError(t, os.WriteFile(taken, []byte("first"), 0o600))

	err := claimStagedPath(taken, "rows.jsonl")

	require.Error(t, err, "the path is taken by an entry already written")
	assert.Contains(t, err.Error(), "rows.jsonl")
	assert.Contains(t, err.Error(), "case sensitive", "and it says what to do about it")

	// #nosec G304 -- taken is a path this test just created under t.TempDir().
	body, readErr := os.ReadFile(taken)
	require.NoError(t, readErr)
	assert.Equal(t, "first", string(body), "the first entry is left as it was")
}

// The ordinary case: every entry has a path of its own, so nothing is refused.
func TestDistinctEntriesAreNotRefused(t *testing.T) {
	t.Parallel()

	staging := t.TempDir()
	for _, name := range []string{"train.jsonl", "test.jsonl", "nested/rows.jsonl"} {
		local, err := safeJoin(staging, name)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(local), 0o750))

		require.NoErrorf(t, claimStagedPath(local, name), "%s is its own path", name)
		require.NoError(t, os.WriteFile(local, []byte(name), 0o600))
	}
}

// Guarded at the source as well as in behaviour.
//
// The write loop needs a dataset client, so a test that drove it end to end
// would be a fake of the service rather than a test of this rule. What actually
// goes wrong is the call disappearing -- the loop still compiles, still writes,
// and still reports the full count. So this pins that the claim is made.
func TestTheDownloadLoopStillClaimsEachPath(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "dataset_download.go", nil, parser.ParseComments)
	require.NoError(t, err)

	var write *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "write" && fn.Recv != nil {
			write = fn
			break
		}
	}
	require.NotNil(t, write, "write has to exist for this to be guarding anything")

	claims := false
	ast.Inspect(write, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "claimStagedPath" {
			claims = true
		}
		return true
	})

	assert.True(t, claims,
		"the folder download must claim each staged path before writing it; "+
			"without it two entries that fold to one name leave a short dataset "+
			"and a count that says otherwise")
}

// The refusal names both what collided and what to do, because the reader
// cannot otherwise tell which of their files is missing.
func TestTheCollisionRefusalNamesWhatCollided(t *testing.T) {
	t.Parallel()

	err := messages.DownloadEntriesCollideLocally("a.jsonl", "A.jsonl")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a.jsonl", "the entry that arrived second")
	assert.Contains(t, err.Error(), "A.jsonl", "the path it landed on")
	assert.Contains(t, err.Error(), "case sensitive")
}
