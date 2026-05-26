// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestClassifyExtractError_UnsafeEntry(t *testing.T) {
	wrapped := fmt.Errorf("entry escapes: %w", skill_api.ErrUnsafeEntry)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeSkillArchiveUnsafe, le.Code)
}

func TestClassifyExtractError_LimitExceeded(t *testing.T) {
	wrapped := fmt.Errorf("too big: %w", skill_api.ErrLimitExceeded)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeSkillArchiveUnsafe, le.Code)
}

func TestClassifyExtractError_Collision(t *testing.T) {
	wrapped := fmt.Errorf("collision: %w", skill_api.ErrCollision)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeSkillOutputCollision, le.Code)
	require.Contains(t, le.Suggestion, "/tmp/out", "suggestion should mention output dir")
}

func TestClassifyExtractError_InvalidArchive(t *testing.T) {
	wrapped := fmt.Errorf("bad header: %w", skill_api.ErrInvalidArchive)
	err := classifyExtractError(wrapped, "/tmp/out")
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidParameter, le.Code)
}

func TestClassifyExtractError_UnknownPassthrough(t *testing.T) {
	original := errors.New("io: short read")
	err := classifyExtractError(original, "/tmp/out")
	require.Same(t, original, err, "unknown errors must propagate unwrapped")
}

func TestDownloadAction_RejectsInvalidName(t *testing.T) {
	a := &downloadAction{flags: &downloadFlags{name: "Bad Name"}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillName, le.Code)
}
