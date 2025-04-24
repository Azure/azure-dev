// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

type watchFlags struct {
	cwd string
}

func newWatchCommand() *cobra.Command {
	flags := &watchFlags{}

	watchCmd := &cobra.Command{
		Use:   "watch",
		Short: "Watches the AZD extension project for file changes and rebuilds it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Watch and azd extension (azd x watch)",
				"Watches the azd extension project for changes and rebuilds it.",
			)

			defaultWatchFlags(flags)
			err := runWatchAction(flags)
			if err != nil {
				return err
			}

			return nil
		},
	}

	watchCmd.Flags().StringVar(
		&flags.cwd,
		"cwd", ".",
		"Path to the azd extension project",
	)

	return watchCmd
}

func runWatchAction(flags *watchFlags) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Error creating watcher: %w", err)
	}
	defer watcher.Close()

	ignoredFolders := map[string]struct{}{
		"bin": {},
		"obj": {},
	}

	// Define glob patterns for ignored paths
	globIgnorePaths := []string{
		"bin",      // Matches the "bin" folder itself
		"bin/**/*", // Matches all files and subdirectories inside "bin"
		"obj",      // Matches the "obj" folder itself
		"obj/**/*", // Matches all files and subdirectories inside "obj"
	}

	if err := watchRecursive(flags.cwd, watcher, ignoredFolders); err != nil {
		return fmt.Errorf("Error watching for changes: %w", err)
	}

	rebuild(flags.cwd)

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	var debounceActive bool
	uniqueChanges := make(map[string]struct{})

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// Ignore events matching glob patterns
			shouldIgnore := false
			for _, pattern := range globIgnorePaths {
				matched, _ := doublestar.PathMatch(pattern, event.Name)
				if matched {
					shouldIgnore = true
					break
				}
			}
			if shouldIgnore {
				continue
			}

			// Collect unique changes
			uniqueChanges[event.Name] = struct{}{}

			// Reset debounce timer
			if !debounceActive {
				debounceActive = true
				debounce.Reset(500 * time.Millisecond)
			}

		case <-debounce.C:
			if debounceActive {
				debounceActive = false

				// Print unique changes
				color.HiWhite("Changes detected:")
				for change := range uniqueChanges {
					color.Cyan("- %s\n", change)
				}
				uniqueChanges = make(map[string]struct{}) // Clear the map

				// Trigger rebuild
				rebuild(flags.cwd)
				fmt.Println()
			}
		}
	}
}

func watchRecursive(root string, watcher *fsnotify.Watcher, ignoredFolders map[string]struct{}) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if _, has := ignoredFolders[info.Name()]; has {
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

func rebuild(extensionPath string) {
	flags := &buildFlags{}
	defaultBuildFlags(flags)

	if err := runBuildAction(flags); err != nil {
		color.Red("BUILD FAILED: \n%s\n\n", err.Error())
	}

	fmt.Println("Watching for changes...")
	color.HiBlack("Press Ctrl+C to stop.")
	fmt.Println()
}

func defaultWatchFlags(flags *watchFlags) {
	if flags.cwd == "" {
		flags.cwd = "."
	}
}
