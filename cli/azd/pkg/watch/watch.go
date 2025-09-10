// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

type FileChanges struct {
	Created  map[string]bool
	Modified map[string]bool
	Deleted  map[string]bool
}

func StartWatcher(ctx context.Context, mu *sync.Mutex) (*fsnotify.Watcher, chan bool, *FileChanges, error) {
	fileChanges := &FileChanges{
		Created:  make(map[string]bool),
		Modified: make(map[string]bool),
		Deleted:  make(map[string]bool),
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	done := make(chan bool)

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				mu.Lock()
				name := event.Name
				switch {
				case event.Has(fsnotify.Create):
					fileChanges.Created[name] = true
				case event.Has(fsnotify.Write) || event.Has(fsnotify.Rename):
					if !fileChanges.Created[name] && !fileChanges.Deleted[name] {
						fileChanges.Modified[name] = true
					}
				case event.Has(fsnotify.Remove):
					if fileChanges.Created[name] {
						delete(fileChanges.Created, name)
					} else {
						fileChanges.Deleted[name] = true
						delete(fileChanges.Modified, name)
					}
				}
				mu.Unlock()
			case err := <-watcher.Errors:
				log.Printf("watcher error: %v", err)
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	if err := watchRecursive(cwd, watcher); err != nil {
		return nil, nil, nil, fmt.Errorf("watcher failed: %w", err)
	}

	return watcher, done, fileChanges, nil
}

func watchRecursive(root string, watcher *fsnotify.Watcher) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("failed to watch directory %s: %w", path, err)
			}
		}

		return nil
	})
}

func PrintChangedFiles(ctx context.Context, fileChanges *FileChanges, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	createdFileLength := len(fileChanges.Created)
	modifiedFileLength := len(fileChanges.Modified)
	deletedFileLength := len(fileChanges.Deleted)

	if createdFileLength == 0 && modifiedFileLength == 0 && deletedFileLength == 0 {
		return
	}

	fmt.Println(output.WithGrayFormat("\n| Files changed:"))

	if createdFileLength > 0 {
		for file := range fileChanges.Created {
			fmt.Println(output.WithGrayFormat("| "), color.GreenString("+ Created "), file)
		}
	}

	if modifiedFileLength > 0 {
		for file := range fileChanges.Modified {
			fmt.Println(output.WithGrayFormat("| "), color.YellowString(output.WithUnderline("+")), color.YellowString("Modified "), file)
		}
	}

	if deletedFileLength > 0 {
		for file := range fileChanges.Deleted {
			fmt.Println(output.WithGrayFormat("| "), color.RedString("- Deleted "), file)
		}
	}

	fmt.Println("")
}
