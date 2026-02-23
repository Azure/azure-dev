// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestReadReportedExtensionError(t *testing.T) {
	t.Run("EmptyPath", func(t *testing.T) {
		err, readErr := readReportedExtensionError("")
		require.NoError(t, readErr)
		require.Nil(t, err)
	})

	t.Run("ValidErrorFile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ext-error.json")
		writeErr := azdext.WriteErrorFile(path, &azdext.LocalError{
			Message:  "invalid config",
			Code:     "invalid_config",
			Category: azdext.LocalErrorCategoryValidation,
		})
		require.NoError(t, writeErr)

		err, readErr := readReportedExtensionError(path)
		require.NoError(t, readErr)

		var localErr *azdext.LocalError
		require.True(t, errors.As(err, &localErr))
		require.Equal(t, "invalid_config", localErr.Code)
		require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	})

	t.Run("InvalidErrorFileContent", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ext-error-invalid.json")
		require.NoError(t, osWriteFile(path, []byte("{invalid-json")))

		err, readErr := readReportedExtensionError(path)
		require.Nil(t, err)
		require.Error(t, readErr)
		require.True(t, strings.Contains(readErr.Error(), "unmarshal extension error file"))
	})
}

func TestCreateExtensionErrorFileEnv(t *testing.T) {
	envVar, errorFilePath, cleanup, err := createExtensionErrorFileEnv()
	require.NoError(t, err)
	require.NotEmpty(t, envVar)
	require.True(t, strings.HasPrefix(envVar, azdext.ExtensionErrorFileEnv+"="))
	require.NotEmpty(t, errorFilePath)
	require.FileExists(t, errorFilePath)

	// Verify envVar and errorFilePath are consistent
	path := strings.TrimPrefix(envVar, azdext.ExtensionErrorFileEnv+"=")
	require.Equal(t, errorFilePath, path)

	cleanup()
	require.NoFileExists(t, path)
}

func osWriteFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o600)
}
