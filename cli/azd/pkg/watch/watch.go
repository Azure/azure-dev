// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"cmp"
	"container/list"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ignore"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

const (
	reconcileInterval       = 100 * time.Millisecond
	reconcileBatchSize      = 64
	reconciledPathLimit     = 4096
	reconciledPathRetention = time.Minute
)

type Watcher interface {
	// Deprecated: Use GetFileChanges().String() instead.
	PrintChangedFiles(ctx context.Context)
	GetFileChanges() FileChanges
}

type fileWatcher struct {
	fileChanges     *fileChanges
	watcher         *fsnotify.Watcher
	ignoredFolders  map[string]struct{}
	globIgnorePaths []string
	ignoreMatcher   *ignore.Matcher
	root            string
	pendingCreated  map[string]uint64
	reconciledPaths map[string]*list.Element
	reconciledOrder list.List
	createSequence  uint64
	mu              sync.Mutex // Protects fileChanges, pendingCreated, reconciledPaths, reconciledOrder, and createSequence.
}

type reconciledPath struct {
	name    string
	expires time.Time
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
		watcher.Close()
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	// Load ignore patterns from .azdxignore and .gitignore files.
	ignoreMatcher, err := ignore.NewMatcher(cwd)
	if err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to load ignore patterns: %w", err)
	}

	// Hardcoded folder ignores are kept as a fast-path default — they apply
	// even when no .azdxignore or .gitignore file exists.
	ignoredFolders := map[string]struct{}{
		".git": {},
	}

	globIgnorePaths := []string{}
	for folder := range ignoredFolders {
		globIgnorePaths = append(globIgnorePaths, folder)
		globIgnorePaths = append(globIgnorePaths, fmt.Sprintf("%s/**/*", folder))
	}

	fw := &fileWatcher{
		fileChanges:     fileChanges,
		watcher:         watcher,
		ignoredFolders:  ignoredFolders,
		globIgnorePaths: globIgnorePaths,
		ignoreMatcher:   ignoreMatcher,
		root:            cwd,
		pendingCreated:  make(map[string]uint64),
		reconciledPaths: make(map[string]*list.Element),
	}

	go func() {
		defer watcher.Close()

		// Some backends (notably fsnotify's Darwin kqueue backend) emit the
		// synthetic Create event for a new file in a watched directory before
		// the per-file watch is registered: dirChange -> sendCreateIfNew
		// sends Create, then calls internalWatch/addWatch to open the file's
		// own kevent. A file removed inside that window is never watched
		// individually, so no Remove event is ever generated for it, and it
		// would otherwise be stuck in Created forever. Recheck each creation
		// once, in bounded batches, rather than polling all accumulated changes.
		reconcileTicker := time.NewTicker(reconcileInterval)
		defer reconcileTicker.Stop()

		for {
			select {
			case <-reconcileTicker.C:
				fw.reconcileCreated(os.Lstat)
			case event := <-watcher.Events:
				// Fast path: ignore events matching hardcoded glob patterns.
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

				name := event.Name

				// Single os.Stat call — reused for both isDir and ignore matching.
				info, statErr := os.Stat(name)
				isDir := statErr == nil && info.IsDir()

				// Check user-defined ignore patterns (.azdxignore / .gitignore).
				if relPath, relErr := filepath.Rel(fw.root, name); relErr != nil {
					log.Printf("debug: failed to compute relative path for %s: %v", name, relErr)
				} else {
					if fw.ignoreMatcher.IsIgnored(relPath, isDir) {
						continue
					}
					// When the path no longer exists (e.g. Remove event), os.Stat fails
					// and isDir defaults to false. Re-check as a directory so that
					// directory-only patterns (trailing slash) still filter the event.
					if statErr != nil && fw.ignoreMatcher.IsIgnored(relPath, true) {
						continue
					}
				}

				if event.Has(fsnotify.Create) && isDir {
					if _, ignored := fw.ignoredFolders[filepath.Base(name)]; !ignored {
						if err := fw.watchRecursive(name, watcher); err != nil {
							log.Printf("failed to watch new directory %s: %v", name, err)
						}
					}
				}
				fw.recordEvent(event, isDir)
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

func (fw *fileWatcher) recordEvent(event fsnotify.Event, isDir bool) {
	if isDir {
		return
	}

	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.pruneReconciled(time.Now())

	name := event.Name
	switch {
	case event.Has(fsnotify.Create):
		fw.clearReconciled(name)
		fw.createSequence++
		fw.pendingCreated[name] = fw.createSequence
		fw.fileChanges.Created[name] = true
	case event.Has(fsnotify.Write) || event.Has(fsnotify.Rename):
		if !fw.fileChanges.Created[name] && !fw.fileChanges.Deleted[name] && fw.reconciledPaths[name] == nil {
			fw.fileChanges.Modified[name] = true
		}
	case event.Has(fsnotify.Remove):
		delete(fw.pendingCreated, name)
		if fw.fileChanges.Created[name] {
			delete(fw.fileChanges.Created, name)
		} else if fw.reconciledPaths[name] == nil {
			fw.fileChanges.Deleted[name] = true
			delete(fw.fileChanges.Modified, name)
		}
		fw.clearReconciled(name)
	}
}

func (fw *fileWatcher) watchRecursive(root string, watcher *fsnotify.Watcher) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Check if this directory should be ignored by hardcoded defaults.
			if _, ignored := fw.ignoredFolders[info.Name()]; ignored {
				return filepath.SkipDir
			}

			// Check user-defined ignore patterns (.azdxignore / .gitignore).
			if relPath, relErr := filepath.Rel(fw.root, path); relErr != nil {
				log.Printf("debug: failed to compute relative path for %s: %v", path, relErr)
			} else if relPath != "." {
				if fw.ignoreMatcher.IsIgnored(relPath, true) {
					return filepath.SkipDir
				}
			}

			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("failed to watch directory %s: %w", path, err)
			}
		}
		return nil
	})
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
}

// FileChangeType enumerates the types of file changes.
type FileChangeType int

const (
	// FileCreated indicates a new file was created.
	FileCreated FileChangeType = iota
	// FileModified indicates an existing file was modified.
	FileModified
	// FileDeleted indicates a file was deleted.
	FileDeleted
)

// FileChange describes a single file change with its path and type.
type FileChange struct {
	Path       string
	ChangeType FileChangeType
}

// String returns a formatted display string for a single file change.
func (fc FileChange) String() string {
	cwd, cwdErr := os.Getwd()
	path := fc.Path
	if cwdErr == nil {
		if rel, err := filepath.Rel(cwd, fc.Path); err == nil {
			path = rel
		}
	}

	switch fc.ChangeType {
	case FileCreated:
		return fmt.Sprintf("%s %s %s",
			output.WithGrayFormat("|"),
			color.GreenString("+ Created  "),
			path)
	case FileModified:
		return fmt.Sprintf("%s %s %s",
			output.WithGrayFormat("|"),
			color.YellowString("± Modified "),
			path)
	case FileDeleted:
		return fmt.Sprintf("%s %s %s",
			output.WithGrayFormat("|"),
			color.RedString("- Deleted  "),
			path)
	default:
		return fmt.Sprintf("%s   %s", output.WithGrayFormat("|"), path)
	}
}

// FileChanges is a collection of file changes with formatted output support.
type FileChanges []FileChange

// String returns a formatted display of all file changes.
func (fc FileChanges) String() string {
	if len(fc) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(output.WithGrayFormat("| Files changed:"))
	for _, change := range fc {
		b.WriteString("\n")
		b.WriteString(change.String())
	}
	return b.String()
}

// reconcileCreated checks a bounded batch of new paths once, outside the state
// lock. A missing creation disappears entirely; its marker suppresses queued
// events until the next Create or Remove, or bounded marker retirement. Sequence
// checks keep stale filesystem results from applying to a later creation.
func (fw *fileWatcher) reconcileCreated(lstat func(string) (os.FileInfo, error)) {
	fw.mu.Lock()
	fw.pruneReconciled(time.Now())
	batch := make(map[string]uint64, min(len(fw.pendingCreated), reconcileBatchSize))
	for name, sequence := range fw.pendingCreated {
		batch[name] = sequence
		if len(batch) == reconcileBatchSize {
			break
		}
	}
	fw.mu.Unlock()

	for name, sequence := range batch {
		_, err := lstat(name)
		missing := errors.Is(err, os.ErrNotExist)
		if err != nil && !missing {
			log.Printf("failed to recheck created file %s: %v", name, err)
		}

		fw.mu.Lock()
		if fw.pendingCreated[name] == sequence {
			delete(fw.pendingCreated, name)
			if missing && fw.fileChanges.Created[name] {
				delete(fw.fileChanges.Created, name)
				fw.markReconciled(name, time.Now())
			}
		}
		fw.mu.Unlock()
	}
}

// Marker bookkeeping runs only under mu. The ordered list bounds both storage
// and expiration work without scanning historical filenames on every tick.
func (fw *fileWatcher) clearReconciled(name string) {
	if element := fw.reconciledPaths[name]; element != nil {
		fw.reconciledOrder.Remove(element)
		delete(fw.reconciledPaths, name)
	}
}

func (fw *fileWatcher) pruneReconciled(now time.Time) {
	for element := fw.reconciledOrder.Front(); element != nil; element = fw.reconciledOrder.Front() {
		marker := element.Value.(reconciledPath)
		if now.Before(marker.expires) {
			break
		}
		fw.clearReconciled(marker.name)
	}
}

func (fw *fileWatcher) markReconciled(name string, now time.Time) {
	fw.pruneReconciled(now)
	fw.clearReconciled(name)
	if len(fw.reconciledPaths) == reconciledPathLimit {
		fw.clearReconciled(fw.reconciledOrder.Front().Value.(reconciledPath).name)
	}
	fw.reconciledPaths[name] = fw.reconciledOrder.PushBack(reconciledPath{
		name: name, expires: now.Add(reconciledPathRetention),
	})
}

// GetFileChanges returns all file changes tracked by the watcher, sorted by path.
func (fw *fileWatcher) GetFileChanges() FileChanges {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	changes := make(FileChanges, 0,
		len(fw.fileChanges.Created)+len(fw.fileChanges.Modified)+len(fw.fileChanges.Deleted))

	for file := range fw.fileChanges.Created {
		changes = append(changes, FileChange{Path: file, ChangeType: FileCreated})
	}
	for file := range fw.fileChanges.Modified {
		changes = append(changes, FileChange{Path: file, ChangeType: FileModified})
	}
	for file := range fw.fileChanges.Deleted {
		changes = append(changes, FileChange{Path: file, ChangeType: FileDeleted})
	}

	slices.SortFunc(changes, func(a, b FileChange) int {
		return cmp.Compare(a.Path, b.Path)
	})

	return changes
}
