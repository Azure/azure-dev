// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore EXCED
//go:build windows

package cmd

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isFileProbeFallbackError(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_NAME) ||
		errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE) ||
		errors.Is(err, windows.ERROR_BUFFER_OVERFLOW)
}
