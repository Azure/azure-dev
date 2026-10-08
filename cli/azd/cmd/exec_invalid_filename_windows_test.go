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

func TestIsInvalidFilenameError(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		err := &scripting.ValidationError{
			Field: "scriptPath",
			Err: &os.PathError{
				Op:   "CreateFile",
				Path: "cat<deploy.sh",
				Err:  windows.ERROR_INVALID_NAME,
			},
		}

		assert.True(t, isInvalidFilenameError(err))
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

		assert.False(t, isInvalidFilenameError(err))
	})

	t.Run("script not found", func(t *testing.T) {
		assert.False(t, isInvalidFilenameError(&scripting.ScriptNotFoundError{Path: "deploy.cmd"}))
	})
}
