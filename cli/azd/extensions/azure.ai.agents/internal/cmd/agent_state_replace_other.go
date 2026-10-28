// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package cmd

import "os"

func replaceAgentStateFile(from, to string) error {
	return os.Rename(from, to)
}
