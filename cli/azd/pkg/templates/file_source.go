package templates

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// NewFileTemplateSource creates a new template source from a file.
func NewFileTemplateSource(name string, path string) (Source, error) {
	absolutePath, err := getAbsolutePath(path)
	if err != nil {
		return nil, fmt.Errorf("failed converting path '%s' to absolute path, %w", path, err)
	}

	templateBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading file '%s', %w", path, err)
	}

	return NewJsonTemplateSource(name, string(templateBytes))
}

func getAbsolutePath(filePath string) (string, error) {
	// Check if the path is absolute
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}

	roots := []string{}

	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	azdConfigPath, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	roots = append(roots, cwd)
	roots = append(roots, azdConfigPath)

	for _, root := range roots {
		// Join the root directory with the relative path
		absolutePath := filepath.Join(root, filePath)

		// Normalize the path to handle any ".." or "." segments
		absolutePath, err = filepath.Abs(absolutePath)
		if err != nil {
			return "", err
		}

		if _, err := os.Stat(absolutePath); err == nil {
			return absolutePath, nil
		}
	}

	return "", fmt.Errorf("file '%s' was not found, %w", filePath, os.ErrNotExist)
}
