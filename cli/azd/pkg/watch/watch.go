// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
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
	watcher         watchBackend
	ignoredFolders  map[string]struct{}
	globIgnorePaths []string
	ignoreMatcher   *ignore.Matcher
	root            string
	// initialFiles is fixed at startup, not extended by transient paths.
	initialFiles map[string]struct{}
	// startupRevisions exists only while initial-file reconciliation runs.
	startupRevisions map[string]startupRevision
	mu               sync.Mutex
	revision         uint64
	done             chan struct{}
	flush            chan chan struct{}
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

type startupRevision struct {
	revision uint64
	removed  bool
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
		watcher:         watcher,
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
	rescan := make(chan struct{}, 1)
	var discoveryMu sync.Mutex
	pendingDirectories := make(map[string]struct{})
	queueDirectory := func(path string) {
		path = filepath.Clean(path)
		discoveryMu.Lock()
		for pending := range pendingDirectories {
			if path == pending || strings.HasPrefix(path, pending+string(os.PathSeparator)) {
				discoveryMu.Unlock()
				return
			}
			if strings.HasPrefix(pending, path+string(os.PathSeparator)) {
				delete(pendingDirectories, pending)
			}
		}
		pendingDirectories[path] = struct{}{}
		discoveryMu.Unlock()
		select {
		case rescan <- struct{}{}:
		default:
		}
	}
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
					// Queue only created subtrees; registration stays off the consumer.
					if _, ignored := fw.ignoredFolders[filepath.Base(name)]; !ignored {
						queueDirectory(name)
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
			err = fw.reconcileInitialFiles(watchCtx, os.Lstat)
		}
		if err == nil {
			err = watchCtx.Err()
		}
		registered <- err
		if err != nil {
			return
		}
		scan := func() {
			discoveryMu.Lock()
			directories := pendingDirectories
			pendingDirectories = make(map[string]struct{})
			discoveryMu.Unlock()
			for path := range directories {
				if err := fw.watchRecursive(watchCtx, path, watcher); err != nil && watchCtx.Err() == nil {
					log.Printf("failed to update directory watches for %s: %v", path, err)
				}
			}
		}
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-rescan:
				scan()
			case barrier := <-fw.flush:
				scan()
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

// flushDirectories joins in-flight registration and subtrees already queued when
// the owner accepts the barrier, without blocking event accounting or consumption.
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

// reconcileInitialFiles closes the inventory-to-registration deletion gap.
// Unlike later transient creations, a missing initial file is always a deletion,
// even if it was briefly recreated or its path is now a directory.
func (fw *fileWatcher) reconcileInitialFiles(
	ctx context.Context, stat func(string) (os.FileInfo, error),
) error {
	fw.mu.Lock()
	fw.startupRevisions = make(map[string]startupRevision)
	fw.mu.Unlock()
	defer func() {
		fw.mu.Lock()
		fw.startupRevisions = nil
		fw.mu.Unlock()
	}()
	for path := range fw.initialFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		relPath, err := filepath.Rel(fw.root, path)
		if err != nil {
			return fmt.Errorf("failed to compute relative path for %s: %w", path, err)
		}
		if fw.ignoreMatcher.IsIgnored(relPath, false) {
			continue
		}
		fw.mu.Lock()
		revision := fw.startupRevisions[path].revision
		fw.mu.Unlock()
		info, err := stat(path)
		if ctxErr := ctx.Err(); ctxErr != nil {
			if err != nil {
				return fmt.Errorf("failed to reconcile initial file %s: %w; watch context ended: %w", path, err, ctxErr)
			}
			return ctxErr
		}
		if errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
			fw.mu.Lock()
			// Preserve a newer creation or write, but a newer Remove or Rename still
			// confirms deletion of the original file after a brief recreation.
			current := fw.startupRevisions[path]
			if current.revision == revision || current.removed {
				delete(fw.fileChanges.Created, path)
				delete(fw.fileChanges.Modified, path)
				fw.fileChanges.Deleted[path] = true
				fw.revision++
			}
			fw.mu.Unlock()
		} else if err != nil {
			return fmt.Errorf("failed to reconcile initial file %s: %w", path, err)
		}
	}
	return nil
}

// trackFileEventLocked updates file change accounting. The caller must hold fw.mu.
func (fw *fileWatcher) trackFileEventLocked(event fsnotify.Event) {
	fw.revision++
	name := event.Name
	_, existed := fw.initialFiles[name]
	if existed && fw.startupRevisions != nil {
		current := fw.startupRevisions[name]
		current.revision++
		current.removed = (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) &&
			!event.Has(fsnotify.Create) && !event.Has(fsnotify.Write)
		fw.startupRevisions[name] = current
	}
	switch {
	case event.Has(fsnotify.Create):
		fw.fileChanges.Created[name] = true
	case event.Has(fsnotify.Write) || event.Has(fsnotify.Rename):
		if existed && !fw.fileChanges.Created[name] && !fw.fileChanges.Deleted[name] {
			fw.fileChanges.Modified[name] = true
		}
	case event.Has(fsnotify.Remove):
		if fw.fileChanges.Created[name] {
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
					fw.revision++
					fw.mu.Unlock()
				}
			}
		}
		return nil
	})
}

func (fw *fileWatcher) PrintChangedFiles(ctx context.Context) {
	changes := fw.snapshotFileChanges(os.Lstat)
	createdFiles := slices.Collect(maps.Keys(changes.Created))

	createdFileLength := len(createdFiles)
	modifiedFileLength := len(changes.Modified)
	deletedFileLength := len(changes.Deleted)

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
		for _, file := range createdFiles {
			fmt.Println(output.WithGrayFormat("| "), color.GreenString("+ Created  "), getDisplayPath(file))
		}
	}

	if modifiedFileLength > 0 {
		for file := range changes.Modified {
			fmt.Println(output.WithGrayFormat("| "), color.YellowString("± Modified "), getDisplayPath(file))
		}
	}

	if deletedFileLength > 0 {
		for file := range changes.Deleted {
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

// snapshotFileChanges filters missing created files outside the event-accounting lock.
// Some backends (notably Darwin kqueue) can miss Remove events when a file is
// removed before its per-file watch is registered. Reconcile before reporting
// changes, even if the final snapshot immediately precedes watcher cancellation.
// The fixed startup inventory distinguishes pre-existing files from late events
// for reclaimed ephemeral paths, without retaining a tombstone for each path.
func (fw *fileWatcher) snapshotFileChanges(stat func(string) (os.FileInfo, error)) fileChanges {
	fw.flushDirectories()
	fw.mu.Lock()
	revision := fw.revision
	snapshot := fileChanges{
		Created: maps.Clone(fw.fileChanges.Created), Modified: maps.Clone(fw.fileChanges.Modified),
		Deleted: maps.Clone(fw.fileChanges.Deleted),
	}
	fw.mu.Unlock()

	retained := make(map[string]bool, len(snapshot.Created))
	for name, created := range snapshot.Created {
		if info, err := stat(name); !errors.Is(err, os.ErrNotExist) && (err != nil || !info.IsDir()) {
			retained[name] = created
		}
	}
	if len(retained) != len(snapshot.Created) {
		fw.mu.Lock()
		// A concurrent event may recreate a path after its stat result. Only
		// reclaim the live map when this snapshot still owns its revision.
		if fw.revision == revision {
			fw.fileChanges.Created = maps.Clone(retained)
			fw.revision++
		}
		fw.mu.Unlock()
	}
	snapshot.Created = retained
	return snapshot
}

// GetFileChanges returns tracked file changes, excluding missing created files,
// sorted by path.
func (fw *fileWatcher) GetFileChanges() FileChanges {
	snapshot := fw.snapshotFileChanges(os.Lstat)

	changes := make(FileChanges, 0,
		len(snapshot.Created)+len(snapshot.Modified)+len(snapshot.Deleted))

	for file := range snapshot.Created {
		changes = append(changes, FileChange{Path: file, ChangeType: FileCreated})
	}
	for file := range snapshot.Modified {
		changes = append(changes, FileChange{Path: file, ChangeType: FileModified})
	}
	for file := range snapshot.Deleted {
		changes = append(changes, FileChange{Path: file, ChangeType: FileDeleted})
	}

	slices.SortFunc(changes, func(a, b FileChange) int {
		return cmp.Compare(a.Path, b.Path)
	})

	return changes
}
