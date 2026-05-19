// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// --- createAction.Run early-exit paths (no network) ---

func TestCreateAction_RejectsInvalidName(t *testing.T) {
	a := &createAction{flags: &createFlags{name: "_bad"}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillName, le.Code)
}

func TestCreateAction_NoPromptWithNoInput(t *testing.T) {
	a := &createAction{flags: &createFlags{name: "my-skill", noPrompt: true}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeMissingRequiredField, le.Code)
}

// TestCreateAction_ConflictingFlagsViRun exercises the full Run path (not just
// selectCreateMode) so that the validation is also reached via the command
// entry-point.
func TestCreateAction_ConflictingFlagsViaRun(t *testing.T) {
	a := &createAction{flags: &createFlags{
		name:           "my-skill",
		descriptionSet: true,
		description:    "desc",
		file:           "SKILL.md",
		noPrompt:       true,
	}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeConflictingArguments, le.Code)
}

// --- verifyPackageNameMatches ---

func TestVerifyPackageNameMatches_NameMatches(t *testing.T) {
	path := writeZipWithSkillMd(t, "my-skill")
	require.NoError(t, verifyPackageNameMatches(path, "my-skill"))
}

func TestVerifyPackageNameMatches_NameMismatch(t *testing.T) {
	path := writeZipWithSkillMd(t, "other-skill")
	err := verifyPackageNameMatches(path, "my-skill")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}

func TestVerifyPackageNameMatches_NoSkillMd(t *testing.T) {
	// Archive without SKILL.md: no name claim → --force allowed.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("README.md")
	require.NoError(t, err)
	_, _ = w.Write([]byte("hi"))
	require.NoError(t, zw.Close())
	p := writeTempFile(t, buf.Bytes(), "*.zip")
	require.NoError(t, verifyPackageNameMatches(p, "my-skill"))
}

func TestVerifyPackageNameMatches_MalformedSkillMd(t *testing.T) {
	// Malformed SKILL.md: PeekArchiveSkillName now propagates the error,
	// so --force must be blocked to prevent accidental skill deletion.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, _ = w.Write([]byte("not valid front matter"))
	require.NoError(t, zw.Close())
	p := writeTempFile(t, buf.Bytes(), "*.zip")
	err = verifyPackageNameMatches(p, "my-skill")
	require.Error(t, err, "malformed SKILL.md must block --force to prevent accidental deletion")
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}

// helpers

func writeZipWithSkillMd(t *testing.T, skillName string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, _ = w.Write([]byte("---\nname: " + skillName + "\n---\nbody\n"))
	require.NoError(t, zw.Close())
	return writeTempFile(t, buf.Bytes(), "*.zip")
}

func writeTempFile(t *testing.T, data []byte, pattern string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), pattern)
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return filepath.Clean(f.Name())
}
