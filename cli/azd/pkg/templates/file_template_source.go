package templates

import (
	"fmt"
	"os"
	"path/filepath"
)

// NewFileTemplateSource creates a new template source from a file.
func NewFileTemplateSource(path string) (TemplateSource, error) {
	absolutePath, err := getAbsolutePath(path)
	if err != nil {
		return nil, fmt.Errorf("failed converting path '%s' to absolute path, %w", path, err)
	}

	templateBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading file '%s', %w", path, err)
	}

	return NewJsonTemplateSource(string(templateBytes))
}

func getAbsolutePath(filePath string) (string, error) {
	// Check if the path is absolute
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}

	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Join the current working directory with the relative path
	absolutePath := filepath.Join(cwd, filePath)

	// Normalize the path to handle any ".." or "." segments
	absolutePath, err = filepath.Abs(absolutePath)
	if err != nil {
		return "", err
	}

	return absolutePath, nil
}
