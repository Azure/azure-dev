// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package azdcontext

import (
	"os"
	"path/filepath"
)

func isEnvironmentLink(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}

func canonicalDirectory(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
