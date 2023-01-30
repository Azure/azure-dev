package apphint

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type PythonDetector struct {
}

func (pd *PythonDetector) DetectProjects(root string) ([]Project, error) {
	getProject := func(path string, entries []fs.DirEntry) *Project {
		for _, entry := range entries {
			if entry.Name() == "pyproject.toml" || entry.Name() == "requirements.txt" {
				return &Project{
					Language:  string(Python),
					Path:      path,
					InferRule: "Inferred by presence of: " + entry.Name(),
				}
			}
		}

		return nil
	}

	projects := []Project{}
	err := WalkDirectories(root, func(path string, entries []fs.DirEntry) error {
		project := getProject(path, entries)
		if project != nil {
			projects = append(projects, *project)
			return filepath.SkipDir
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return projects, nil
}

func WalkDirectories(root string, fn func(path string, d []fs.DirEntry) error) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}

	return walkDirRecursive(root, fs.FileInfoToDirEntry(info), fn)
}

func walkDirRecursive(path string, d fs.DirEntry, fn func(path string, d []fs.DirEntry) error) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	err = fn(path, entries)
	if err != nil {
		if errors.Is(err, filepath.SkipDir) {
			return nil
		}

		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			newPath := filepath.Join(path, entry.Name())
			err = walkDirRecursive(newPath, entry, fn)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
