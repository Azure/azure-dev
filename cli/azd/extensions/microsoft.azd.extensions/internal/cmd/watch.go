// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

type watchFlags struct {
}

func newWatchCommand() *cobra.Command {
	flags := &watchFlags{}

	watchCmd := &cobra.Command{
		Use:   "watch",
		Short: "Watches the azd extension project for file changes and rebuilds it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Watch and azd extension (azd x watch)",
				"Watches the azd extension project for changes and rebuilds it.",
			)

			err := runWatchAction(cmd.Context(), flags)
			if err != nil {
				return err
			}

			return nil
		},
	}

	return watchCmd
}

func runWatchAction(ctx context.Context, flags *watchFlags) error {
	// Create a new context that includes the azd access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new azd client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Error creating watcher: %w", err)
	}
	defer watcher.Close()

	ignoredFolders := map[string]struct{}{
		"bin":          {},
		"obj":          {},
		"build":        {},
		"node_modules": {},
		".git":         {},
	}

	globIgnorePaths := []string{}

	for folder := range ignoredFolders {
		globIgnorePaths = append(globIgnorePaths, folder)
		globIgnorePaths = append(globIgnorePaths, fmt.Sprintf("%s/**/*", folder))
	}

	// Define glob patterns for ignored paths
	globIgnorePaths = append(globIgnorePaths,
		"*.spec",            // Matches all .spec files
		"package-lock.json", // Matches package-lock.json files
	)

	if err := watchRecursive(".", watcher, ignoredFolders); err != nil {
		return fmt.Errorf("Error watching for changes: %w", err)
	}

	rebuild(ctx, ".")

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
				rebuild(ctx, ".")
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

func rebuild(ctx context.Context, extensionPath string) {
	flags := &buildFlags{}
	defaultBuildFlags(flags)

	if err := runBuildAction(ctx, flags); err != nil {
		color.Red("BUILD FAILED: \n%s\n\n", err.Error())
	}

	fmt.Println("Watching for changes...")
	color.HiBlack("Press Ctrl+C to stop.")
	fmt.Println()
}
