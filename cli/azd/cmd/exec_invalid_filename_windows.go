// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
//go:build windows

package cmd

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isInvalidFilenameError(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_NAME)
}
