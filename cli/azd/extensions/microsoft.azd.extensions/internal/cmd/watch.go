package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

func newWatchCommand() *cobra.Command {
	watchCmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch the azd extension project",
		RunE:  watchAndRebuild,
	}

	watchCmd.Flags().StringP("path", "p", ".", "Paths to the extension directory. Defaults to the current directory.")

	return watchCmd
}

func watchAndRebuild(cmd *cobra.Command, args []string) error {
	extensionPath, _ := cmd.Flags().GetString("path")

	internal.WriteCommandHeader(
		"Watch and azd extension (azd x watch)",
		"Watches the azd extension project for changes and rebuilds it.",
	)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Error creating watcher: %w", err)
	}
	defer watcher.Close()

	ignoredFolders := map[string]struct{}{
		"bin": {},
	}

	// Define glob patterns for ignored paths
	globIgnorePaths := []string{
		"bin",      // Matches the "bin" folder itself
		"bin/**/*", // Matches all files and subdirectories inside "bin"
	}

	if err := watchRecursive(extensionPath, watcher, ignoredFolders); err != nil {
		return fmt.Errorf("Error watching for changes: %w", err)
	}

	rebuild(extensionPath)
	fmt.Println()

	fmt.Println("Watching for changes...")
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

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
				rebuild(extensionPath)
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
	cmd := exec.Command("azd", "x", "build")
	cmd.Dir = extensionPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	_ = cmd.Run()
}
