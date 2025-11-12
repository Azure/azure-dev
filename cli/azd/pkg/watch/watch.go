// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/denormal/go-gitignore"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

type Watcher interface {
	PrintChangedFiles(ctx context.Context)
}

type fileWatcher struct {
	fileChanges     *fileChanges
	watcher         *fsnotify.Watcher
	ignoredFolders  map[string]struct{}
	globIgnorePaths []string
	ignorer         gitignore.GitIgnore
	rootDir         string
	mu              sync.Mutex
}

type fileChanges struct {
	Created  map[string]bool
	Modified map[string]bool
	Deleted  map[string]bool
}

func NewWatcher(ctx context.Context) (Watcher, error) {
	fileChanges := &fileChanges{
		Created:  make(map[string]bool),
		Modified: make(map[string]bool),
		Deleted:  make(map[string]bool),
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	// Set up ignore patterns
	ignoredFolders := map[string]struct{}{
		".git": {},
	}

	globIgnorePaths := []string{}
	for folder := range ignoredFolders {
		globIgnorePaths = append(globIgnorePaths, folder)
		globIgnorePaths = append(globIgnorePaths, fmt.Sprintf("%s/**/*", folder))
	}

	// Load .azdxignore file if it exists
	var ignorer gitignore.GitIgnore
	azdxIgnorePath := filepath.Join(cwd, ".azdxignore")
	if ig, err := gitignore.NewFromFile(azdxIgnorePath); err == nil {
		ignorer = ig
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to load .azdxignore file: %w", err)
	}

	fw := &fileWatcher{
		fileChanges:     fileChanges,
		watcher:         watcher,
		ignoredFolders:  ignoredFolders,
		globIgnorePaths: globIgnorePaths,
		ignorer:         ignorer,
		rootDir:         cwd,
	}

	go func() {
		defer watcher.Close()

		for {
			select {
			case event := <-watcher.Events:
				// Ignore events matching glob patterns
				shouldIgnore := false
				for _, pattern := range fw.globIgnorePaths {
					matched, _ := doublestar.PathMatch(pattern, event.Name)
					if matched {
						shouldIgnore = true
						break
					}
				}
				if shouldIgnore {
					continue
				}

				// Check if the file is ignored by .azdxignore
				if fw.ignorer != nil {
					// Check if this is a file or directory for gitignore matching
					info, statErr := os.Stat(event.Name)
					isDir := statErr == nil && info.IsDir()
					if fw.isIgnored(event.Name, isDir) {
						continue
					}
				}

				fw.mu.Lock()
				name := event.Name

				// Check if this is a file or directory
				info, err := os.Stat(name)
				isDir := err == nil && info.IsDir()

				switch {
				case event.Has(fsnotify.Create):
					if isDir {
						// New directory created - start watching it if not ignored
						if _, ignored := fw.ignoredFolders[filepath.Base(name)]; !ignored {
							if err := fw.watchRecursive(name, watcher); err != nil {
								log.Printf("failed to watch new directory %s: %v", name, err)
							}
						}
					} else {
						// Only track file creation, not directory creation
						fileChanges.Created[name] = true
					}
				case event.Has(fsnotify.Write) || event.Has(fsnotify.Rename):
					// Only track file changes, not directory changes
					if !isDir && !fileChanges.Created[name] && !fileChanges.Deleted[name] {
						fileChanges.Modified[name] = true
					}
				case event.Has(fsnotify.Remove):
					// Handle both file and directory removal, but only track files
					if !isDir {
						if fileChanges.Created[name] {
							delete(fileChanges.Created, name)
						} else {
							fileChanges.Deleted[name] = true
							delete(fileChanges.Modified, name)
						}
					}
				}
				fw.mu.Unlock()
			case err := <-watcher.Errors:
				log.Printf("watcher error: %v", err)
			case <-ctx.Done():
				return
			}
		}
	}()

	if err := fw.watchRecursive(cwd, watcher); err != nil {
		return nil, fmt.Errorf("watcher failed: %w", err)
	}

	return fw, nil
}

func (fw *fileWatcher) watchRecursive(root string, watcher *fsnotify.Watcher) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Check if this directory should be ignored
			if _, ignored := fw.ignoredFolders[info.Name()]; ignored {
				return filepath.SkipDir
			}

			// Check if the directory is ignored by .azdxignore
			if fw.isIgnored(path, true) {
				return filepath.SkipDir
			}

			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("failed to watch directory %s: %w", path, err)
			}
		}
		return nil
	})
}

// isIgnored checks if a path should be ignored based on the .azdxignore file
func (fw *fileWatcher) isIgnored(path string, isDir bool) bool {
	if fw.ignorer == nil {
		return false
	}

	// Get the relative path from the root directory
	relPath, err := filepath.Rel(fw.rootDir, path)
	if err != nil {
		// If we can't get relative path, use the path as-is
		relPath = path
	}

	// Check if the path is ignored
	match := fw.ignorer.Relative(relPath, isDir)
	return match != nil
}

func (fw *fileWatcher) PrintChangedFiles(ctx context.Context) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	createdFileLength := len(fw.fileChanges.Created)
	modifiedFileLength := len(fw.fileChanges.Modified)
	deletedFileLength := len(fw.fileChanges.Deleted)

	if createdFileLength == 0 && modifiedFileLength == 0 && deletedFileLength == 0 {
		return
	}

	fmt.Println(output.WithGrayFormat("\n| Files changed:"))

	cwd, err := os.Getwd()
	getDisplayPath := func(file string) string {
		if err != nil {
			return file // fallback to absolute path if cwd failed
		}
		if relPath, relErr := filepath.Rel(cwd, file); relErr == nil {
			return relPath
		}

		return file // fallback to absolute path if relative conversion failed
	}

	if createdFileLength > 0 {
		for file := range fw.fileChanges.Created {
			fmt.Println(output.WithGrayFormat("| "), color.GreenString("+ Created  "), getDisplayPath(file))
		}
	}

	if modifiedFileLength > 0 {
		for file := range fw.fileChanges.Modified {
			fmt.Println(output.WithGrayFormat("| "), color.YellowString("± Modified "), getDisplayPath(file))
		}
	}

	if deletedFileLength > 0 {
		for file := range fw.fileChanges.Deleted {
			fmt.Println(output.WithGrayFormat("| "), color.RedString("- Deleted  "), getDisplayPath(file))
		}
	}

	fmt.Println("")
}
