// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func isEnvironmentLink(info os.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 ||
		(ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0)
}

func canonicalDirectory(path string) (string, error) {
	// EvalSymlinks does not follow junctions on Windows. Resolve the directory
	// through a handle first so linked project ancestors have a canonical base.
	dir, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer dir.Close()

	size := uint32(256)
	for {
		buffer := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(dir.Fd()), &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if n >= size {
			size = n + 1
			continue
		}
		path = windows.UTF16ToString(buffer[:n])
		if after, ok := strings.CutPrefix(path, `\\?\UNC\`); ok {
			path = `\\` + after
		} else {
			path = strings.TrimPrefix(path, `\\?\`)
		}
		return filepath.EvalSymlinks(path)
	}
}
