// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteReadErrorFile(t *testing.T) {
	t.Run("RoundTripLocalError", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ext-error.json")

		writeErr := WriteErrorFile(path, &LocalError{
			Message:    "invalid config",
			Code:       "invalid_config",
			Category:   LocalErrorCategoryValidation,
			Suggestion: "Add missing field in config",
		})
		require.NoError(t, writeErr)

		err, readErr := ReadErrorFile(path)
		require.NoError(t, readErr)
		require.NotNil(t, err)

		var localErr *LocalError
		require.ErrorAs(t, err, &localErr)
		require.Equal(t, "invalid_config", localErr.Code)
		require.Equal(t, LocalErrorCategoryValidation, localErr.Category)
		require.Equal(t, "Add missing field in config", localErr.Suggestion)
	})

	t.Run("MissingFile", func(t *testing.T) {
		err, readErr := ReadErrorFile(filepath.Join(t.TempDir(), "missing.json"))
		require.NoError(t, readErr)
		require.Nil(t, err)
	})
}
