// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// azure.yaml's `$ref` names a file, not a directory.
//
// A project is free to declare `./config/nightly.yaml`. Reading only the
// directory out of that and then looking for `azure.eval.yaml` beside it
// reports the configuration missing while `azd up` deploys it from the very
// same `$ref` -- the two-answers-to-one-question shape this cascade exists to
// close.
func TestLocationMayNameTheFileTheRefDeclares(t *testing.T) {
	dir := t.TempDir()
	declared := filepath.Join(dir, "nightly.yaml")
	require.NoError(t, os.WriteFile(declared, []byte("evals:\n  - name: nightly\n"), 0o600))

	path, err := ResolveEvalConfigPath(declared)
	require.NoError(t, err)
	assert.Equal(t, declared, path, "the declared file is the configuration")

	cfg, err := OpenEvalConfig(declared)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Evals, 1)
	assert.Equal(t, "nightly", cfg.Evals[0].Name)

	assert.Equal(t, dir, EvalDirOf(declared),
		"artifacts sit beside the configuration, not inside it")
}

// A directory keeps the naming convention it always had.
func TestLocationMayStillBeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, EvalConfigBase), []byte("evals:\n  - name: nightly\n"), 0o600))

	path, err := ResolveEvalConfigPath(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, EvalConfigBase), path)
	assert.Equal(t, dir, EvalDirOf(dir))
}

// A location that does not exist yet is a directory: it is what `init` is given
// before it writes anything.
func TestAMissingLocationIsReadAsADirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created-yet")

	assert.Equal(t, filepath.Join(dir, EvalConfigBase), EvalConfigPath(dir))
	assert.Equal(t, dir, EvalDirOf(dir))
}

// The both-names guard is about a directory holding two candidates. A location
// that already names the file has nothing to disambiguate.
func TestADeclaredFileSkipsTheAmbiguityGuard(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, EvalConfigBase), []byte("evals: []\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, LegacyEvalConfigBase), []byte("evals: []\n"), 0o600))

	_, err := ResolveEvalConfigPath(dir)
	require.Error(t, err, "a directory holding both names is still refused")

	declared := filepath.Join(dir, EvalConfigBase)
	got, err := ResolveEvalConfigPath(declared)
	require.NoError(t, err, "naming the file is how a project says which one it means")
	assert.Equal(t, declared, got)
}
