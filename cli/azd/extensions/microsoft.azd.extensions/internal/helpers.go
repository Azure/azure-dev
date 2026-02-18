// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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

// CopyFile copies a file from source to destination.
// On Windows, if the target file is locked (e.g., by a running process),
// it attempts to rename the locked file out of the way before copying.
func CopyFile(source, target string) error {
	srcFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	targetFile, err := os.Create(target)
	if err != nil && runtime.GOOS == "windows" {
		// On Windows, the target may be locked by a running process.
		// Try to rename it out of the way, then create the new file.
		oldTarget := target + ".old"
		_ = os.Remove(oldTarget)
		if renameErr := os.Rename(target, oldTarget); renameErr == nil {
			targetFile, err = os.Create(target)
		}
	}
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

	// Remove extension
	if strings.HasSuffix(archPart, ".tar.gz") {
		// Special handling for .tar.gz since filepath.Ext only removes the last extension (.gz)
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

// DefaultArtifactPatterns returns the default glob patterns for finding extension artifacts
// in the local registry.
func DefaultArtifactPatterns(extensionId, version string) ([]string, error) {
	localRegistryArtifactsPath, err := LocalRegistryArtifactsPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get registry artifacts path: %w", err)
	}
	basePattern := filepath.Join(localRegistryArtifactsPath, extensionId, version)
	return []string{
		filepath.Join(basePattern, "*.zip"),
		filepath.Join(basePattern, "*.tar.gz"),
	}, nil
}

// FindArtifacts finds all artifact files for an extension using the provided patterns or defaults.
func FindArtifacts(patterns []string, extensionId, version string) ([]string, error) {
	if len(patterns) == 0 {
		var err error
		patterns, err = DefaultArtifactPatterns(extensionId, version)
		if err != nil {
			return nil, err
		}
	}
	var allFiles []string
	for _, pattern := range patterns {
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		allFiles = append(allFiles, files...)
	}
	return allFiles, nil
}

// GetFileNameWithoutExt extracts the filename without its extension
func GetFileNameWithoutExt(filePath string) string {
	// Get the base filename
	fileName := filepath.Base(filePath)

	// Special handling for .tar.gz since filepath.Ext only removes the last extension (.gz)
	if strings.HasSuffix(fileName, ".tar.gz") {
		return strings.TrimSuffix(fileName, ".tar.gz")
	}

	// Remove the extension
	return strings.TrimSuffix(fileName, filepath.Ext(fileName))
}

// HasLocalRegistry checks if a local extension source registry exists
func HasLocalRegistry() (bool, error) {
	cmdBytes, err := exec.Command("azd", "ext", "source", "list", "-o", "json").Output()
	if err != nil {
		return false, fmt.Errorf("failed to execute command: %w", err)
	}

	var extensionSources []any
	if err := json.Unmarshal(cmdBytes, &extensionSources); err != nil {
		return false, fmt.Errorf("failed to unmarshal command output: %w", err)
	}

	for _, source := range extensionSources {
		extensionSource, ok := source.(map[string]any)
		if ok {
			if extensionSource["name"] == "local" && extensionSource["type"] == "file" {
				return true, nil
			}
		}
	}

	return false, nil
}

// CreateLocalRegistry creates a local extension source registry
func CreateLocalRegistry() error {
	azdConfigDir := os.Getenv("AZD_CONFIG_DIR")
	if azdConfigDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		azdConfigDir = filepath.Join(homeDir, ".azd")
	}

	localRegistryPath := filepath.Join(azdConfigDir, "registry.json")
	emptyRegistry := map[string]any{
		"registry": []any{},
	}

	registryJson, err := json.MarshalIndent(emptyRegistry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal empty registry: %w", err)
	}

	if err := os.WriteFile(localRegistryPath, registryJson, PermissionFile); err != nil {
		return fmt.Errorf("failed to create local registry file: %w", err)
	}

	args := []string{
		"ext", "source", "add",
		"--name", "local",
		"--type", "file",
		"--location", localRegistryPath,
	}

	/* #nosec G204 - args are hardcoded above, not user-controlled */
	createExtSourceCmd := exec.Command("azd", args...)
	if _, err := createExtSourceCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create local extension source: %w", err)
	}

	return nil
}
