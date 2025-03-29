// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package util

import (
	"os"
)

func IsDirEmpty(dirPath string) bool {
	dir, err := os.Open(dirPath)
	if err != nil {
		return false
	}
	defer dir.Close()

	// Read at most 1 entry
	entries, err := dir.Readdirnames(1)
	if err != nil {
		return true
	}

	// If no entries, the directory is empty
	return len(entries) == 0
}
