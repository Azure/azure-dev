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

func TestDeleteAction_RejectsInvalidName(t *testing.T) {
	a := &deleteAction{flags: &deleteFlags{name: "_bad"}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillName, le.Code)
}

func TestDeleteAction_NoPromptRequiresForce(t *testing.T) {
	a := &deleteAction{flags: &deleteFlags{name: "my-skill", noPrompt: true, force: false}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeMissingForceFlag, le.Code)
}
