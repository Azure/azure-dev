// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
//go:build !windows

package cmd

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec/scripting"
	"github.com/stretchr/testify/assert"
)

func TestIsFileProbeFallbackError(t *testing.T) {
	t.Run("filename too long", func(t *testing.T) {
		err := &scripting.ValidationError{
			Field: "scriptPath",
			Err: &os.PathError{
				Op:   "stat",
				Path: "long.sh",
				Err:  syscall.ENAMETOOLONG,
			},
		}

		assert.True(t, isFileProbeFallbackError(err))
	})

	t.Run("other error", func(t *testing.T) {
		assert.False(t, isFileProbeFallbackError(errors.New("invalid filename")))
	})
}
