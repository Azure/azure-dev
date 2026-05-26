// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"testing"

	"azureaiskills/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestValidateSkillName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"a", false},
		{"my-skill", false},
		{"abc123", false},
		{"skill1-2-3", false},
		{"", true},
		{"   ", true},
		{"-leading-hyphen", true},
		{"trailing-hyphen-", true},
		{"under_score", true},
		{"contains space", true},
		// new spec: lowercase only — uppercase rejected.
		{"Skill1", true},
		{"UPPER", true},
		// 64 char limit (was 63 in old spec)
		{string(makeRune('a', 64)), false},
		{string(makeRune('a', 65)), true},
	}
	for _, c := range cases {
		err := validateSkillName(c.name)
		if c.wantErr {
			require.Errorf(t, err, "expected validation error for %q", c.name)
			var le *azdext.LocalError
			require.True(t, errors.As(err, &le), "expected LocalError for %q", c.name)
			require.Equal(t, exterrors.CodeInvalidSkillName, le.Code)
		} else {
			require.NoErrorf(t, err, "unexpected error for %q", c.name)
		}
	}
}

func makeRune(c rune, n int) []rune {
	out := make([]rune, n)
	for i := range out {
		out[i] = c
	}
	return out
}

func TestSelectCreateMode_ConflictingArgs(t *testing.T) {
	_, err := selectCreateMode(&createFlags{
		descriptionSet: true,
		file:           "./SKILL.md",
	})
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeConflictingArguments, le.Code)
}

func TestSelectCreateMode_FileMd(t *testing.T) {
	mode, err := selectCreateMode(&createFlags{file: "./SKILL.md"})
	require.NoError(t, err)
	require.Equal(t, modeFileMd, mode)
}

func TestSelectCreateMode_FilePackage(t *testing.T) {
	for _, f := range []string{"./pkg.zip", "./PKG.ZIP"} {
		mode, err := selectCreateMode(&createFlags{file: f})
		require.NoError(t, err, "file %q", f)
		require.Equal(t, modeFilePackage, mode, "file %q", f)
	}
}

func TestSelectCreateMode_UnknownExtension(t *testing.T) {
	_, err := selectCreateMode(&createFlags{file: "./SKILL.txt"})
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}

func TestSelectCreateMode_InlineOnly(t *testing.T) {
	mode, err := selectCreateMode(&createFlags{descriptionSet: true})
	require.NoError(t, err)
	require.Equal(t, modeInline, mode)
}

func TestSelectCreateMode_None(t *testing.T) {
	mode, err := selectCreateMode(&createFlags{})
	require.NoError(t, err)
	require.Equal(t, modeNone, mode)
}

func TestUpdateAction_RequiresInput(t *testing.T) {
	a := &updateAction{flags: &updateFlags{}}
	err := a.validateFlags()
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeMissingRequiredField, le.Code)
}

func TestUpdateAction_ConflictingArgs(t *testing.T) {
	a := &updateAction{flags: &updateFlags{descriptionSet: true, file: "./SKILL.md"}}
	err := a.validateFlags()
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeConflictingArguments, le.Code)
}

func TestUpdateAction_RejectsZipFile(t *testing.T) {
	for _, f := range []string{"./pkg.zip", "./PKG.ZIP"} {
		a := &updateAction{flags: &updateFlags{file: f}}
		err := a.validateFlags()
		require.Errorf(t, err, "file %q", f)
		var le *azdext.LocalError
		require.True(t, errors.As(err, &le), "file %q", f)
		require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code, "file %q", f)
	}
}

func TestUpdateAction_AcceptsMdFile(t *testing.T) {
	a := &updateAction{flags: &updateFlags{file: "./SKILL.md"}}
	require.NoError(t, a.validateFlags())
}

func TestUpdateAction_UnknownExtension(t *testing.T) {
	a := &updateAction{flags: &updateFlags{file: "./SKILL.txt"}}
	err := a.validateFlags()
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
}

func TestShouldSuppressWarning(t *testing.T) {
	require.True(t, shouldSuppressWarning(true, outputTable))
	require.True(t, shouldSuppressWarning(false, outputJSON))
	require.False(t, shouldSuppressWarning(false, outputTable))
}

func TestTruncate(t *testing.T) {
	require.Equal(t, "hello", truncate("hello", 10))
	require.Equal(t, "hi", truncate("hi", 2))
	require.Equal(t, "hel...", truncate("hello-world", 6))
	require.Equal(t, "hello world", truncate("hello\nworld", 60))
}

func TestIsNotFound(t *testing.T) {
	require.False(t, isNotFound(nil))
	require.False(t, isNotFound(errors.New("oops")))
	require.True(t, isNotFound(&azcore.ResponseError{StatusCode: 404}))
	require.False(t, isNotFound(&azcore.ResponseError{StatusCode: 500}))
	wrapped := fmt.Errorf("get skill: %w", &azcore.ResponseError{StatusCode: 404})
	require.True(t, isNotFound(wrapped), "wrapped 404 ResponseError must still match")
}
