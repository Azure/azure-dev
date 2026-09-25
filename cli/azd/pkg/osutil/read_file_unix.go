// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package osutil

import "os"

// ReadFile reads the named file.
func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
