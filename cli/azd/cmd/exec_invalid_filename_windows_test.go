// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
//go:build windows

package cmd

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec/scripting"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/windows"
)

func TestIsFileProbeFallbackError(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		err := &scripting.ValidationError{
			Field: "scriptPath",
			Err: &os.PathError{
				Op:   "CreateFile",
				Path: "cat<deploy.sh",
				Err:  windows.ERROR_INVALID_NAME,
			},
		}

		assert.True(t, isFileProbeFallbackError(err))
	})

	t.Run("filename too long", func(t *testing.T) {
		err := &scripting.ValidationError{
			Field: "scriptPath",
			Err: &os.PathError{
				Op:   "CreateFile",
				Path: "long.cmd",
				Err:  windows.ERROR_FILENAME_EXCED_RANGE,
			},
		}

		assert.True(t, isFileProbeFallbackError(err))
	})

	t.Run("buffer overflow", func(t *testing.T) {
		err := &scripting.ValidationError{
			Field: "scriptPath",
			Err: &os.PathError{
				Op:   "GetFullPathName",
				Path: "long.cmd",
				Err:  windows.ERROR_BUFFER_OVERFLOW,
			},
		}

		assert.True(t, isFileProbeFallbackError(err))
	})

	t.Run("access denied", func(t *testing.T) {
		err := &scripting.ValidationError{
			Field: "scriptPath",
			Err: &os.PathError{
				Op:   "CreateFile",
				Path: "deploy.cmd",
				Err:  windows.ERROR_ACCESS_DENIED,
			},
		}

		assert.False(t, isFileProbeFallbackError(err))
	})

	t.Run("script not found", func(t *testing.T) {
		assert.False(t, isFileProbeFallbackError(&scripting.ScriptNotFoundError{Path: "deploy.cmd"}))
	})
}
