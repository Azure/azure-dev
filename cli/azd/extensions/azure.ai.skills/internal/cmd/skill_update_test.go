// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// --- updateAction.Run early-exit paths (no network) ---

func TestUpdateAction_RejectsInvalidName(t *testing.T) {
	a := &updateAction{flags: &updateFlags{
		name:           "_bad",
		descriptionSet: true,
		description:    "new desc",
	}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillName, le.Code)
}

func TestUpdateAction_ValidInputFailsAtEndpoint(t *testing.T) {
	// A fully valid inline update with no endpoint configured must fail at
	// endpoint resolution (not at flag validation or name validation).
	a := &updateAction{flags: &updateFlags{
		name:            "my-skill",
		descriptionSet:  true,
		description:     "new desc",
		instructionsSet: true,
		instructions:    "new instructions",
	}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeMissingProjectEndpoint, le.Code,
		"should fail at endpoint resolution with no project configured")
}

// --- selectUpdateMode routing (issue #8489) ---
//
// Verifies that `update --file` now accepts the same three input shapes
// as `create --file`: a single SKILL.md, a .zip archive, or a directory
// whose root contains SKILL.md. Previously update rejected .zip and
// directories with a pointer to `create --force` (destructive).

func TestSelectUpdateMode_FileMd(t *testing.T) {
	mode, err := selectUpdateMode(&updateFlags{file: "./SKILL.md"})
	require.NoError(t, err)
	require.Equal(t, updateModeFileMd, mode)
}

func TestSelectUpdateMode_FilePackage(t *testing.T) {
	for _, f := range []string{"./pkg.zip", "./PKG.ZIP"} {
		mode, err := selectUpdateMode(&updateFlags{file: f})
		require.NoError(t, err, "file %q", f)
		require.Equal(t, updateModeFilePackage, mode, "file %q", f)
	}
}

func TestSelectUpdateMode_FileDirectory(t *testing.T) {
	dir := writeSkillDir(t, "my-skill")
	mode, err := selectUpdateMode(&updateFlags{file: dir})
	require.NoError(t, err)
	require.Equal(t, updateModeFileDirectory, mode)
}

func TestSelectUpdateMode_InlineOnly(t *testing.T) {
	mode, err := selectUpdateMode(&updateFlags{descriptionSet: true})
	require.NoError(t, err)
	require.Equal(t, updateModeInline, mode)
}

func TestSelectUpdateMode_SetDefaultAlone(t *testing.T) {
	mode, err := selectUpdateMode(&updateFlags{setDefault: "2"})
	require.NoError(t, err)
	require.Equal(t, updateModeSetDefault, mode)
}

func TestSelectUpdateMode_SetDefaultConflictsWithContent(t *testing.T) {
	for _, f := range []*updateFlags{
		{setDefault: "2", descriptionSet: true},
		{setDefault: "2", instructionsSet: true},
		{setDefault: "2", file: "./SKILL.md"},
	} {
		mode, err := selectUpdateMode(f)
		require.Error(t, err, "%+v", f)
		require.Equal(t, updateModeNone, mode)
		var le *azdext.LocalError
		require.True(t, errors.As(err, &le))
		require.Equal(t, exterrors.CodeConflictingArguments, le.Code)
	}
}

func TestSelectUpdateMode_InlineAndFileConflict(t *testing.T) {
	mode, err := selectUpdateMode(&updateFlags{
		descriptionSet: true,
		file:           "./SKILL.md",
	})
	require.Error(t, err)
	require.Equal(t, updateModeNone, mode)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeConflictingArguments, le.Code)
}

func TestSelectUpdateMode_NoInputFails(t *testing.T) {
	mode, err := selectUpdateMode(&updateFlags{})
	require.Error(t, err)
	require.Equal(t, updateModeNone, mode)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeMissingRequiredField, le.Code)
}

func TestSelectUpdateMode_UnknownExtension(t *testing.T) {
	mode, err := selectUpdateMode(&updateFlags{file: "./SKILL.txt"})
	require.Error(t, err)
	require.Equal(t, updateModeNone, mode)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}

// TestSelectUpdateMode_MissingNoExtPathSurfacesStatError guards the same
// regression as the create-side TestSelectCreateMode_MissingNoExtPathSurfacesStatError:
// a --file value with no extension that does not exist on disk must
// surface the underlying stat failure rather than a misleading
// "unsupported --file extension \"\"" error.
func TestSelectUpdateMode_MissingNoExtPathSurfacesStatError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	mode, err := selectUpdateMode(&updateFlags{file: missing})
	require.Error(t, err)
	require.Equal(t, updateModeNone, mode)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
	require.NotContains(t, le.Message, `unsupported --file extension ""`)
	require.Contains(t, le.Message, "inspect --file")
}

func TestUpdateAction_SetDefaultVersion_AcceptsAlone(t *testing.T) {
	a := &updateAction{flags: &updateFlags{setDefault: "2"}}
	require.NoError(t, a.validateFlags())
}

func TestUpdateAction_SetDefaultVersion_ConflictsWithContent(t *testing.T) {
	a := &updateAction{flags: &updateFlags{setDefault: "2", descriptionSet: true}}
	err := a.validateFlags()
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeConflictingArguments, le.Code)
}

// --- updateAction run-path preflight (no network) ---

// TestUpdateAction_DirectoryWithoutSkillMdFails proves runFileDirectory
// rejects a directory missing SKILL.md before any network call, so it can
// be safely exercised with a nil client. Mirrors
// TestCreateAction_DirectoryWithoutSkillMdFails.
func TestUpdateAction_DirectoryWithoutSkillMdFails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0600))
	a := &updateAction{flags: &updateFlags{name: "my-skill", file: dir}}
	err := a.runFileDirectory(context.Background(), nil)
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}

// TestUpdateAction_PackageFileMissingFails proves runFilePackage's stat
// preflight rejects a non-existent .zip before any network call (safe to
// exercise with a nil client).
func TestUpdateAction_PackageFileMissingFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.zip")
	a := &updateAction{flags: &updateFlags{name: "my-skill", file: missing}}
	err := a.runFilePackage(context.Background(), nil)
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}

// TestUpdateAction_PackageFileIsDirectoryFails proves runFilePackage
// rejects a directory passed via the .zip-extension-only path (defense in
// depth — the routing layer would normally send directories through
// runFileDirectory, but the runFilePackage stat check exists for safety).
func TestUpdateAction_PackageFileIsDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	a := &updateAction{flags: &updateFlags{name: "my-skill", file: dir}}
	err := a.runFilePackage(context.Background(), nil)
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}
