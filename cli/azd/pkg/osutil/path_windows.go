// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore NONALERT

package osutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func readContainedFile(rootPath, relativePath string) ([]byte, error) {
	root, err := openRootFile(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readContainedFileFromRoot(root, relativePath)
}

func openRootFile(rootPath string) (*os.File, error) {
	pathPointer, err := windows.UTF16PtrFromString(rootPath)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), rootPath), nil
}

func readContainedFileFromRoot(root *os.File, relativePath string) ([]byte, error) {
	rootFinalPath, err := finalPath(root)
	if err != nil {
		return nil, err
	}

	current := root
	var openedDirectories []*os.File
	defer func() {
		for _, directory := range openedDirectories {
			directory.Close()
		}
	}()

	components := strings.Split(relativePath, string(os.PathSeparator))
	for index, component := range components {
		isLeaf := index == len(components)-1
		target, err := openRelativeFile(current, component, !isLeaf)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				exists, checkErr := relativeEntryExists(current, component)
				if checkErr != nil {
					return nil, checkErr
				}
				if exists {
					return nil, errPathEscapesRoot
				}
			}
			return nil, err
		}
		targetPath, err := finalPath(target)
		if err != nil {
			target.Close()
			return nil, err
		}
		if !isCanonicalPathContained(rootFinalPath, targetPath) {
			target.Close()
			return nil, errPathEscapesRoot
		}
		if isLeaf {
			defer target.Close()
			return io.ReadAll(target)
		}
		openedDirectories = append(openedDirectories, target)
		current = target
	}

	return nil, os.ErrNotExist
}

func relativeEntryExists(root *os.File, relativePath string) (bool, error) {
	objectName, err := windows.NewNTUnicodeString(relativePath)
	if err != nil {
		return false, err
	}
	objectAttributes := &windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(root.Fd()),
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	//nolint:gosec // OBJECT_ATTRIBUTES is far smaller than uint32 on Windows.
	objectAttributes.Length = uint32(unsafe.Sizeof(*objectAttributes))

	var handle windows.Handle
	err = windows.NtCreateFile(
		&handle,
		windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES,
		objectAttributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT,
		0,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.STATUS_NO_SUCH_FILE) ||
			errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) ||
			errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND) {
			return false, nil
		}
		return false, err
	}
	windows.CloseHandle(handle)
	return true, nil
}

func openRelativeFile(root *os.File, relativePath string, directory bool) (*os.File, error) {
	objectName, err := windows.NewNTUnicodeString(relativePath)
	if err != nil {
		return nil, err
	}
	objectAttributes := &windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(root.Fd()),
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	//nolint:gosec // OBJECT_ATTRIBUTES is far smaller than uint32 on Windows.
	objectAttributes.Length = uint32(unsafe.Sizeof(*objectAttributes))

	var handle windows.Handle
	options := uint32(windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_FOR_BACKUP_INTENT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	err = windows.NtCreateFile(
		&handle,
		windows.SYNCHRONIZE|windows.FILE_GENERIC_READ,
		objectAttributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		options,
		0,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.STATUS_NO_SUCH_FILE) ||
			errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) ||
			errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND) {
			return nil, &os.PathError{Op: "openat", Path: relativePath, Err: os.ErrNotExist}
		}
		return nil, err
	}
	return os.NewFile(uintptr(handle), relativePath), nil
}

func finalPath(file *os.File) (string, error) {
	bufferSize := uint32(windows.MAX_PATH)
	buffer := make([]uint16, bufferSize)
	for {
		length, err := windows.GetFinalPathNameByHandle(
			windows.Handle(file.Fd()),
			&buffer[0],
			bufferSize,
			0,
		)
		if err != nil {
			return "", err
		}
		if length < bufferSize {
			resolved := windows.UTF16ToString(buffer[:length])
			if after, ok := strings.CutPrefix(resolved, `\\?\UNC\`); ok {
				return `\\` + after, nil
			}
			if after, ok := strings.CutPrefix(resolved, `\\?\`); ok {
				return after, nil
			}
			return resolved, nil
		}
		bufferSize = length + 1
		buffer = make([]uint16, bufferSize)
	}
}

func isCanonicalPathContained(rootPath, targetPath string) bool {
	rootPath = filepath.Clean(rootPath)
	targetPath = filepath.Clean(targetPath)
	if targetPath == rootPath {
		return true
	}
	if !strings.HasSuffix(rootPath, string(os.PathSeparator)) {
		rootPath += string(os.PathSeparator)
	}
	return strings.HasPrefix(targetPath, rootPath)
}
