// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWrapUnwrapErrorRoundTrip(t *testing.T) {
	t.Run("LocalError", func(t *testing.T) {
		original := &LocalError{
			Message:    "invalid config",
			Code:       "invalid_config",
			Category:   LocalErrorCategoryValidation,
			Suggestion: "Add missing field in config",
		}

		proto := WrapError(original)
		require.NotNil(t, proto)

		unwrapped := UnwrapError(proto)
		require.NotNil(t, unwrapped)

		var localErr *LocalError
		require.ErrorAs(t, unwrapped, &localErr)
		require.Equal(t, "invalid_config", localErr.Code)
		require.Equal(t, LocalErrorCategoryValidation, localErr.Category)
		require.Equal(t, "Add missing field in config", localErr.Suggestion)
	})

	t.Run("NilError", func(t *testing.T) {
		proto := WrapError(nil)
		require.Nil(t, proto)
	})
}

func TestNewErrorReportContext_DetachesCancellation(t *testing.T) {
	type contextKey string
	const key contextKey = "key"

	parent, cancelParent := context.WithCancel(context.WithValue(t.Context(), key, "value"))
	cancelParent()

	reportCtx, cancelReport := newErrorReportContext(parent)
	defer cancelReport()

	require.NoError(t, reportCtx.Err())
	require.Equal(t, "value", reportCtx.Value(key))
	deadline, ok := reportCtx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(errorReportTimeout), deadline, 100*time.Millisecond)
}
