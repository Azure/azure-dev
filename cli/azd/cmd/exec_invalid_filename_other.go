// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
//go:build !windows

package cmd

import (
	"errors"
	"syscall"
)

func isFileProbeFallbackError(err error) bool {
	return errors.Is(err, syscall.ENAMETOOLONG)
}
