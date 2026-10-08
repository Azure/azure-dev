// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
//go:build !windows

package cmd

func isInvalidFilenameError(error) bool {
	return false
}
