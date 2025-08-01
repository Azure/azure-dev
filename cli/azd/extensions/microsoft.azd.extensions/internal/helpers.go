// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	PermissionDirectory      os.FileMode = 0755
	PermissionExecutableFile os.FileMode = 0755
	PermissionFile           os.FileMode = 0644

	PermissionDirectoryOwnerOnly os.FileMode = 0700
	PermissionFileOwnerOnly      os.FileMode = 0600

	PermissionMaskDirectoryExecute os.FileMode = 0100
)

func ToPtr[T any](value T) *T {
	return &value
}

func ToPascalCase(value string) string {
	parts := strings.Split(value, ".")

	for i, part := range parts {
		if len(part) > 0 {
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			parts[i] = string(runes)
		}
	}

	return strings.Join(parts, ".")
}

// ComputeChecksum computes the SHA256 checksum of a file
func ComputeChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to compute checksum: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// CopyFile copies a file from source to destination
func CopyFile(source, target string) error {
	srcFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	targetFile, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to create target file: %w", err)
	}
	defer targetFile.Close()

	_, err = io.Copy(targetFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	return nil
}

// InferOSArch infers OS/ARCH from a artifact filename
func InferOSArch(filename string) (string, error) {
	// Example filename: azd-ext-ai-windows-amd64.exe
	parts := strings.Split(filename, "-")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid artifact filename format: %s", filename)
	}

	// Extract OS and ARCH from the filename
	osPart := parts[len(parts)-2]   // Second-to-last part is the OS
	archPart := parts[len(parts)-1] // Last part is the ARCH (with optional extension)

	// Remove extension, handling both .tar.gz and single extensions
	if strings.HasSuffix(archPart, ".tar.gz") {
		archPart = strings.TrimSuffix(archPart, ".tar.gz")
	} else {
		archPart = strings.TrimSuffix(archPart, filepath.Ext(archPart))
	}

	return fmt.Sprintf("%s/%s", osPart, archPart), nil
}

// DownloadAssetToTemp downloads an asset (from URL or local path) to a temp file and returns the file path.
func DownloadAssetToTemp(assetUrl, assetName string) (string, error) {
	tempFile, err := os.CreateTemp("", "asset-*"+filepath.Ext(assetName))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}

	var reader io.Reader
	parsedUrl, err := url.Parse(assetUrl)
	if err == nil && (parsedUrl.Scheme == "https") {
		// #nosec G107: Potential HTTP request made with variable url
		resp, err := http.Get(assetUrl)
		if err != nil {
			os.Remove(tempFile.Name())
			return "", fmt.Errorf("failed to download asset: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			os.Remove(tempFile.Name())
			return "", fmt.Errorf("download returned status %d", resp.StatusCode)
		}
		reader = resp.Body
	} else {
		localFile, err := os.Open(assetUrl)
		if err != nil {
			os.Remove(tempFile.Name())
			return "", fmt.Errorf("failed to open local file: %w", err)
		}
		defer localFile.Close()
		reader = localFile
	}

	if _, err := io.Copy(tempFile, reader); err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}
	tempFile.Close()

	return tempFile.Name(), nil
}

func ZipSource(files []string, target string) error {
	outputFile, err := os.Create(target)
	if err != nil {
		return err
	}

	defer outputFile.Close()

	zipWriter := zip.NewWriter(outputFile)
	defer zipWriter.Close()

	for _, file := range files {
		fileInfo, err := os.Stat(file)
		if err != nil {
			return err
		}

		header := &zip.FileHeader{
			Name:     filepath.Base(file),
			Modified: fileInfo.ModTime(),
			Method:   zip.Deflate,
		}

		headerWriter, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(file)
		if err != nil {
			return err
		}

		_, err = io.Copy(headerWriter, file)
		if err != nil {
			return err
		}
	}

	return nil
}

func TarGzSource(files []string, target string) error {
	outputFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	gzipWriter := gzip.NewWriter(outputFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for _, file := range files {
		fileInfo, err := os.Stat(file)
		if err != nil {
			return err
		}

		header := &tar.Header{
			Name:    filepath.Base(file),
			Mode:    int64(fileInfo.Mode()),
			Size:    fileInfo.Size(),
			ModTime: fileInfo.ModTime(),
		}

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		file, err := os.Open(file)
		if err != nil {
			return err
		}

		_, err = io.Copy(tarWriter, file)
		file.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func LocalRegistryArtifactsPath() (string, error) {
	azdConfigDir, err := AzdConfigDir()
	if err != nil {
		return "", err
	}

	registryArtifactsPath := filepath.Join(azdConfigDir, "registry")
	if _, err := os.Stat(registryArtifactsPath); os.IsNotExist(err) {
		if err := os.MkdirAll(registryArtifactsPath, PermissionDirectory); err != nil {
			return "", fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	return registryArtifactsPath, nil
}

func AzdConfigDir() (string, error) {
	azdConfigDir := os.Getenv("AZD_CONFIG_DIR")
	if azdConfigDir == "" {
		userHomeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		azdConfigDir = filepath.Join(userHomeDir, ".azd")
	}

	return azdConfigDir, nil
}

// GlobArtifacts finds artifacts matching the given patterns
func GlobArtifacts(patterns []string) ([]string, error) {
	var allFiles []string

	for _, pattern := range patterns {
		// Check if the pattern is a concrete file path (no wildcards)
		if !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?") && !strings.Contains(pattern, "[") {
			// It's a concrete file path, check if it exists
			if _, err := os.Stat(pattern); err == nil {
				allFiles = append(allFiles, pattern)
			}
			// If the file doesn't exist, skip it (no error to match glob behavior)
			continue
		}

		// Use filepath.Glob for pattern matching
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		allFiles = append(allFiles, files...)
	}

	return allFiles, nil
}
