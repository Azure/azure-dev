// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewWatcher_WithoutIgnoreFile(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "watch-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Change to the temporary directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the watcher without .azdxignore file
	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)
	require.NotNil(t, watcher)

	// Cancel the context to stop the watcher
	cancel()
	time.Sleep(100 * time.Millisecond) // Give time for cleanup
}

func TestNewWatcher_WithIgnoreFile(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "watch-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Change to the temporary directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Create a .azdxignore file
	ignoreContent := `# Test ignore file
*.log
node_modules/
build/
`
	err = os.WriteFile(".azdxignore", []byte(ignoreContent), 0600)
	require.NoError(t, err)

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the watcher with .azdxignore file
	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)
	require.NotNil(t, watcher)

	// Verify the watcher has the ignorer set
	fw, ok := watcher.(*fileWatcher)
	require.True(t, ok)
	require.NotNil(t, fw.ignorer)

	// Cancel the context to stop the watcher
	cancel()
	time.Sleep(100 * time.Millisecond) // Give time for cleanup
}

func TestNewWatcher_IgnoresSpecifiedDirectories(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "watch-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Change to the temporary directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Create a .azdxignore file
	ignoreContent := `node_modules/
`
	err = os.WriteFile(".azdxignore", []byte(ignoreContent), 0600)
	require.NoError(t, err)

	// Create node_modules directory
	err = os.Mkdir("node_modules", 0755)
	require.NoError(t, err)

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the watcher
	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)
	require.NotNil(t, watcher)

	fw, ok := watcher.(*fileWatcher)
	require.True(t, ok)

	// Verify that node_modules directory is ignored
	nodeModulesPath := filepath.Join(tmpDir, "node_modules")
	isIgnored := fw.ignorer.Absolute(nodeModulesPath, true)
	require.NotNil(t, isIgnored, "node_modules should be ignored")

	// Cancel the context to stop the watcher
	cancel()
	time.Sleep(100 * time.Millisecond) // Give time for cleanup
}
