package azdignore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/denormal/go-gitignore"
)

// ReadIgnoreFiles reads all .azdignore files in the directory hierarchy, from the projectDir upwards,
// and returns a slice of gitignore.GitIgnore structures.
func ReadIgnoreFiles(projectDir string) ([]gitignore.GitIgnore, error) {
	var ignoreMatchers []gitignore.GitIgnore

	// Traverse upwards from the projectDir to the root directory
	currentDir := projectDir
	for {
		ignoreFilePath := filepath.Join(currentDir, ".azdignore")
		if _, err := os.Stat(ignoreFilePath); !os.IsNotExist(err) {
			ignoreMatcher, err := gitignore.NewFromFile(ignoreFilePath)
			if err != nil {
				return nil, fmt.Errorf("error reading .azdignore file at %s: %w", ignoreFilePath, err)
			}
			ignoreMatchers = append([]gitignore.GitIgnore{ignoreMatcher}, ignoreMatchers...)
		}

		// Stop if we've reached the root directory
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}
		currentDir = parentDir
	}

	return ignoreMatchers, nil
}

// ShouldIgnore checks if a file or directory should be ignored based on a slice of gitignore.GitIgnore structures.
func ShouldIgnore(path string, isDir bool, ignoreMatchers []gitignore.GitIgnore) bool {
	for _, matcher := range ignoreMatchers {
		match := matcher.Relative(path, isDir)
		if match != nil && match.Ignore() {
			return true
		}
	}
	return false
}

// RemoveIgnoredFiles removes files and directories based on .azdignore rules using a pre-collected list of paths.
func RemoveIgnoredFiles(staging string, ignoreMatchers []gitignore.GitIgnore) error {
	if len(ignoreMatchers) == 0 {
		return nil // No .azdignore files, no files to ignore
	}

	// Collect all file and directory paths
	paths, err := CollectFilePaths(staging)
	if err != nil {
		return fmt.Errorf("collecting file paths: %w", err)
	}

	// Map to store directories that should be ignored, preventing their children from being processed
	ignoredDirs := make(map[string]struct{})

	// Iterate through collected paths and determine which to remove
	for _, path := range paths {
		relativePath, err := filepath.Rel(staging, path)
		if err != nil {
			return err
		}

		// Skip processing if the path is within an ignored directory
		skip := false
		for ignoredDir := range ignoredDirs {
			if strings.HasPrefix(relativePath, ignoredDir) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		isDir := false
		info, err := os.Lstat(path)
		if err == nil {
			isDir = info.IsDir()
		}

		// Check if the file should be ignored
		if ShouldIgnore(relativePath, isDir, ignoreMatchers) {
			if isDir {
				ignoredDirs[relativePath] = struct{}{}
				if err := os.RemoveAll(path); err != nil {
					return fmt.Errorf("removing directory %s: %w", path, err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("removing file %s: %w", path, err)
				}
			}
		}
	}

	return nil
}

// CollectFilePaths collects all file and directory paths under the given root directory.
func CollectFilePaths(root string) ([]string, error) {
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}
