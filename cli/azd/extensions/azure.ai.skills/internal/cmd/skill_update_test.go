// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
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

// TestUpdateAction_ZipSuggestionMentionsDestructive verifies that the error
// message for ZIP files on `update` tells the user the operation is
// destructive (delete-then-create at the skill level).
func TestUpdateAction_ZipSuggestionMentionsDestructive(t *testing.T) {
	a := &updateAction{flags: &updateFlags{file: "skill.zip"}}
	err := a.validateFlags()
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillFile, le.Code)
	require.Contains(t, le.Suggestion, "deletes",
		"suggestion must warn that the operation is destructive")
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
