// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func serviceRefingConfig(t *testing.T, ref string) *azdext.ServiceConfig {
	t.Helper()
	props, err := structpb.NewStruct(map[string]any{"$ref": ref})
	require.NoError(t, err)
	return &azdext.ServiceConfig{Name: "evals", Config: props}
}

// A `$ref` names a file by name, and a project is free to call it something
// other than azure.eval.yaml. Reducing the ref to its directory and looking for
// the conventional name beside it produced a scope for a file that is not the
// one being deployed -- so ids recorded at deploy were never found again, and
// the next deploy made a second eval.
func TestScopeOfAServiceIsTheConfigurationItNames(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	named := filepath.Join(dir, "nightly.yaml")
	require.NoError(t, os.WriteFile(named, []byte("evals: []\n"), 0o600))

	scope := EvalScopeOfService(serviceRefingConfig(t, "./config/nightly.yaml"), root)

	require.Equal(t, EvalScope(root, named), scope,
		"deploy must scope by the configuration the $ref names, not by the "+
			"conventional name beside it")
	require.NotContains(t, scope, EvalConfigBase,
		"the ref does not name %s; scoping by it invents a configuration", EvalConfigBase)
}

// The conventional layout has to keep working: a ref naming azure.eval.yaml
// scopes to that file, whether it is reached through the ref or the directory.
func TestScopeOfAConventionalServiceIsUnchanged(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "evals")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	conventional := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(conventional, []byte("evals: []\n"), 0o600))

	viaRef := EvalScopeOfService(serviceRefingConfig(t, "./evals/"+EvalConfigBase), root)
	viaDir := EvalScopeOfService(&azdext.ServiceConfig{Name: "evals", RelativePath: "evals"}, root)

	require.Equal(t, EvalScope(root, conventional), viaRef)
	require.Equal(t, viaRef, viaDir, "both spellings name one configuration")
}

// The scope deploy records under and the scope a lookup computes have to be the
// same string, or the id is written where nothing reads it. Pinned against the
// lookup side's own entry point rather than against a literal.
func TestDeployAndLookupAgreeOnTheScope(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	named := filepath.Join(dir, "nightly.yaml")
	require.NoError(t, os.WriteFile(named, []byte("evals: []\n"), 0o600))

	deployScope := EvalScopeOfService(serviceRefingConfig(t, "./config/nightly.yaml"), root)
	lookupScope := EvalScope(root, named)

	require.Equal(t, lookupScope, deployScope)
	require.NotEmpty(t, deployScope)
	require.Equal(t, EvalScopeTag(lookupScope), EvalScopeTag(deployScope),
		"the state key tag follows the scope, so a disagreement hides there too")
}
