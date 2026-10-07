// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/ignore"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

type Watcher interface {
	// Deprecated: Use GetFileChanges().String() instead.
	PrintChangedFiles(ctx context.Context)
	GetFileChanges() FileChanges
}

type fileWatcher struct {
	fileChanges     *fileChanges
	ignoredFolders  map[string]struct{}
	globIgnorePaths []string
	ignoreMatcher   *ignore.Matcher
	root            string
	// initialFiles is fixed at startup, not extended by transient paths.
	initialFiles map[string]struct{}
	// mu protects fileChanges. initialFiles is immutable once event consumption starts.
	mu    sync.Mutex
	done  chan struct{}
	flush chan chan struct{}
}

type watchBackend interface {
	Add(string) error
	Close() error
}

type fileChanges struct {
	Created  map[string]bool
	Modified map[string]bool
	Deleted  map[string]bool
}

// directoryQueue retains only pending discoveries, coalescing overlapping subtrees.
// It never blocks the event consumer on backend registration.
type directoryQueue struct {
	mu      sync.Mutex
	pending map[string]struct{}
	ready   chan struct{}
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (q *directoryQueue) add(path string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for pending := range q.pending {
		if pathWithin(pending, path) {
			return
		}
		if pathWithin(path, pending) {
			delete(q.pending, pending)
		}
	}
	q.pending[path] = struct{}{}
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *directoryQueue) take() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	paths := slices.Sorted(maps.Keys(q.pending))
	q.pending = make(map[string]struct{})
	return paths
}

func NewWatcher(ctx context.Context) (Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	fw, err := newFileWatcher(ctx, cwd, watcher, watcher.Events, watcher.Errors)
	if err != nil {
		return nil, err
	}
	return fw, nil
}

func newFileWatcher(
	ctx context.Context, cwd string, watcher watchBackend, events <-chan fsnotify.Event, watcherErrors <-chan error,
) (*fileWatcher, error) {
	fileChanges := &fileChanges{
		Created:  make(map[string]bool),
		Modified: make(map[string]bool),
		Deleted:  make(map[string]bool),
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
		ignoredFolders:  ignoredFolders,
		globIgnorePaths: globIgnorePaths,
		ignoreMatcher:   ignoreMatcher,
		root:            cwd,
		initialFiles:    make(map[string]struct{}),
	}

	if err := fw.start(ctx, watcher, events, watcherErrors); err != nil {
		return nil, err
	}
	return fw, nil
}

func (fw *fileWatcher) start(
	ctx context.Context, watcher watchBackend, events <-chan fsnotify.Event, watcherErrors <-chan error,
) error {
	// Build immutable provenance without registering watches. Add may need the
	// backend's event queue drained to complete on Windows.
	if err := fw.walkTracked(ctx, fw.root, func(path string, info os.FileInfo) error {
		if !info.IsDir() {
			fw.initialFiles[path] = struct{}{}
		}
		return nil
	}); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to inventory watched files: %w", err)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	// A single registration owner serializes Add and Close. The consumer keeps
	// draining until that owner finishes its pending Add and closes the backend:
	// Windows Close can otherwise abandon an Add reply or race another Close.
	directories := &directoryQueue{pending: make(map[string]struct{}), ready: make(chan struct{}, 1)}
	registered := make(chan error, 1)
	backendDone := make(chan struct{})
	consumerDone := make(chan struct{})
	fw.done = make(chan struct{})
	fw.flush = make(chan chan struct{})
	go func() {
		defer close(consumerDone)
		defer cancel()

		for events != nil || watcherErrors != nil {
			select {
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if watchCtx.Err() != nil {
					continue // Still drain backend events until pending registration completes.
				}
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
					// Register only the discovered subtree; unrelated established
					// directories must not be traversed again.
					if _, ignored := fw.ignoredFolders[filepath.Base(name)]; !ignored {
						directories.add(name)
					}
				} else if !isDir {
					fw.mu.Lock()
					fw.trackFileEventLocked(event)
					fw.mu.Unlock()
				}
			case err, ok := <-watcherErrors:
				if !ok {
					watcherErrors = nil
					continue
				}
				log.Printf("watcher error: %v", err)
			case <-backendDone:
				return
			}
		}
	}()

	go func() {
		defer func() {
			cancel()
			watcher.Close()
			close(backendDone)
			<-consumerDone
			close(fw.done)
		}()

		err := fw.watchRecursive(watchCtx, fw.root, watcher)
		if err == nil {
			err = watchCtx.Err()
		}
		registered <- err
		if err != nil {
			return
		}
		scanPending := func() {
			for _, path := range directories.take() {
				if watchCtx.Err() != nil {
					return
				}
				if err := fw.watchRecursive(watchCtx, path, watcher); err != nil && watchCtx.Err() == nil {
					if _, statErr := os.Lstat(path); errors.Is(err, os.ErrNotExist) &&
						errors.Is(statErr, os.ErrNotExist) {
						log.Printf("debug: directory disappeared before watch registration %s", path)
					} else {
						log.Printf("failed to update directory watches for %s: %v", path, err)
					}
				}
			}
		}
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-directories.ready:
				scanPending()
			case barrier := <-fw.flush:
				scanPending()
				close(barrier)
			}
		}
	}()

	if err := <-registered; err != nil {
		<-fw.done
		return fmt.Errorf("watcher failed: %w", err)
	}
	return nil
}

// trackFileEventLocked updates file change accounting. The caller must hold fw.mu.
func (fw *fileWatcher) trackFileEventLocked(event fsnotify.Event) {
	name := event.Name
	_, existed := fw.initialFiles[name]
	switch {
	case event.Has(fsnotify.Create):
		fw.fileChanges.Created[name] = true
		delete(fw.fileChanges.Modified, name)
		delete(fw.fileChanges.Deleted, name)
	case event.Has(fsnotify.Write) || event.Has(fsnotify.Rename):
		if existed && !fw.fileChanges.Created[name] && !fw.fileChanges.Deleted[name] {
			fw.fileChanges.Modified[name] = true
		}
	case event.Has(fsnotify.Remove):
		if fw.fileChanges.Created[name] {
			// A queued removal of an earlier generation must not clear a live
			// replacement at the same path. Snapshot reconciliation uses the
			// same current-path contract, not inode-level event attribution.
			if info, err := os.Lstat(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("debug: failed to verify removed file %s: %v", name, err)
				return
			} else if err == nil && !info.IsDir() {
				return
			}
			delete(fw.fileChanges.Created, name)
		} else if existed {
			fw.fileChanges.Deleted[name] = true
			delete(fw.fileChanges.Modified, name)
		}
	}
}

func (fw *fileWatcher) walkTracked(
	ctx context.Context, root string, visit func(string, os.FileInfo) error,
) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			// Walk captures sibling names before visiting them; a removed child
			// must not prevent discovery of later, unrelated directories.
			if path != root && errors.Is(err, os.ErrNotExist) {
				log.Printf("debug: path disappeared during watch traversal %s", path)
				return nil
			}
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
		}
		return visit(path, info)
	})
}

func (fw *fileWatcher) watchRecursive(ctx context.Context, root string, watcher watchBackend) error {
	return fw.walkTracked(ctx, root, func(path string, info os.FileInfo) error {
		if info.IsDir() {
			if err := watcher.Add(path); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return fmt.Errorf("failed to watch directory %s: %w; watch context ended: %w", path, err, ctxErr)
				}
				if path != root && errors.Is(err, os.ErrNotExist) {
					_, statErr := os.Lstat(path)
					if errors.Is(statErr, os.ErrNotExist) {
						log.Printf("debug: directory disappeared before watch registration %s", path)
						return filepath.SkipDir
					}
					if statErr != nil {
						return fmt.Errorf("failed to watch directory %s: %w; failed to verify path: %w", path, err, statErr)
					}
				}
				return fmt.Errorf("failed to watch directory %s: %w", path, err)
			}
		} else {
			// Children may predate registration of their newly created directory,
			// so the backend need not deliver individual Create events for them.
			if relPath, relErr := filepath.Rel(fw.root, path); relErr != nil {
				return fmt.Errorf("failed to compute relative path for %s: %w", path, relErr)
			} else if !fw.ignoreMatcher.IsIgnored(relPath, false) {
				if _, existed := fw.initialFiles[path]; !existed {
					fw.mu.Lock()
					fw.fileChanges.Created[path] = true
					fw.mu.Unlock()
				}
			}
		}
		return nil
	})
}

func (fw *fileWatcher) PrintChangedFiles(ctx context.Context) {
	fw.printChangedFiles(os.Stdout)
}

func (fw *fileWatcher) printChangedFiles(writer io.Writer) {
	fw.flushDirectories()
	fw.mu.Lock()
	defer fw.mu.Unlock()
	createdFiles := fw.existingCreatedFilesLocked()

	createdFileLength := len(createdFiles)
	modifiedFileLength := len(fw.fileChanges.Modified)
	deletedFileLength := len(fw.fileChanges.Deleted)

	if createdFileLength == 0 && modifiedFileLength == 0 && deletedFileLength == 0 {
		return
	}

	fmt.Fprintln(writer, output.WithGrayFormat("\n| Files changed:"))

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
		for _, file := range createdFiles {
			fmt.Fprintln(writer, output.WithGrayFormat("| "), color.GreenString("+ Created  "), getDisplayPath(file))
		}
	}

	if modifiedFileLength > 0 {
		for _, file := range slices.Sorted(maps.Keys(fw.fileChanges.Modified)) {
			fmt.Fprintln(writer, output.WithGrayFormat("| "), color.YellowString("± Modified "), getDisplayPath(file))
		}
	}

	if deletedFileLength > 0 {
		for _, file := range slices.Sorted(maps.Keys(fw.fileChanges.Deleted)) {
			fmt.Fprintln(writer, output.WithGrayFormat("| "), color.RedString("- Deleted  "), getDisplayPath(file))
		}
	}
}

// flushDirectories joins in-flight registration and discoveries already queued
// when the owner accepts the barrier. No accounting lock is held while waiting;
// the event consumer must remain able to drain backend Add replies.
func (fw *fileWatcher) flushDirectories() {
	if fw.flush == nil {
		return
	}
	barrier := make(chan struct{})
	select {
	case fw.flush <- barrier:
		select {
		case <-barrier:
		case <-fw.done:
		}
	case <-fw.done:
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

// existingCreatedFilesLocked filters missing created files from reported changes.
// The caller must hold fw.mu.
//
// Some backends (notably Darwin kqueue) can miss Remove events when a file is
// removed before its per-file watch is registered. Reconcile before reporting
// changes, even if the final snapshot immediately precedes watcher cancellation.
// The fixed startup inventory distinguishes pre-existing files from late events
// for reclaimed ephemeral paths, without retaining a tombstone for each path.
func (fw *fileWatcher) existingCreatedFilesLocked() []string {
	files := make([]string, 0, len(fw.fileChanges.Created))
	for name := range fw.fileChanges.Created {
		if info, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
			continue
		} else {
			files = append(files, name)
		}
	}
	if len(files) != len(fw.fileChanges.Created) {
		// Rebuild rather than just delete keys so peak transient map capacity
		// can also be reclaimed after a burst of ephemeral files.
		retained := make(map[string]bool, len(files))
		for _, name := range files {
			retained[name] = fw.fileChanges.Created[name]
		}
		fw.fileChanges.Created = retained
	}
	slices.Sort(files)
	return files
}

// GetFileChanges returns tracked file changes, excluding missing created files,
// sorted by path.
func (fw *fileWatcher) GetFileChanges() FileChanges {
	fw.flushDirectories()
	fw.mu.Lock()
	defer fw.mu.Unlock()
	createdFiles := fw.existingCreatedFilesLocked()

	changes := make(FileChanges, 0,
		len(createdFiles)+len(fw.fileChanges.Modified)+len(fw.fileChanges.Deleted))

	for _, file := range createdFiles {
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
