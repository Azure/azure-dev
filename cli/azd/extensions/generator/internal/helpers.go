// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ComputeChecksum computes the SHA256 checksum of a file
func ComputeChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to compute checksum: %v", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// CopyFile copies a file from source to destination
func CopyFile(source, target string) error {
	srcFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source file: %v", err)
	}
	defer srcFile.Close()

	targetFile, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to create target file: %v", err)
	}
	defer targetFile.Close()

	_, err = io.Copy(targetFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %v", err)
	}

	return nil
}

// InferOSArch infers OS/ARCH from a artifact filename
func InferOSArch(filename string) (string, error) {
	parts := filepath.Base(filename)
	partsArr := filepath.SplitList(parts)
	if len(partsArr) < 3 {
		return "", fmt.Errorf("invalid artifact filename format: %s", filename)
	}
	osArch := fmt.Sprintf("%s/%s", partsArr[1], partsArr[2])
	return osArch, nil
}
