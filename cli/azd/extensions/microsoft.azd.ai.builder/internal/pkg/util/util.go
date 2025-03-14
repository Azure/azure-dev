// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package util

import "os"

func IsDirEmpty(dirPath string) (bool, error) {
	dir, err := os.Open(dirPath)
	if err != nil {
		return false, err // Handle errors like "directory does not exist"
	}
	defer dir.Close()

	// Read at most 1 entry
	entries, err := dir.Readdirnames(1)
	if err != nil {
		return false, err
	}

	// If no entries found, directory is empty
	return len(entries) == 0, nil
}
