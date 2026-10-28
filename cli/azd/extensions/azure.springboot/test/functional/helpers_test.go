// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

func azdSourcePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	source := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	require.FileExists(t, filepath.Join(source, "main.go"))
	require.FileExists(t, filepath.Join(source, "extensions", "azure.springboot", "extension.yaml"))
	return source
}

func newSpringCLI(t *testing.T) *azdcli.CLI {
	t.Helper()
	if os.Getenv("CLI_TEST_AZD_PATH") == "" {
		t.Skip("set CLI_TEST_AZD_PATH to the current development azd binary to run Spring functional tests")
	}
	require.True(t, filepath.IsAbs(os.Getenv("CLI_TEST_AZD_PATH")), "CLI_TEST_AZD_PATH must be absolute")
	cli := azdcli.NewCLI(t)
	cli.Env = []string{
		"DOCKER_CONFIG=" + tempDirWithDiagnostics(t),
		"AZURE_DEV_COLLECT_TELEMETRY=no",
		"AZD_FORCE_TTY=false",
		"NO_COLOR=1",
	}
	return cli
}

func tempDirWithDiagnostics(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := osutil.RemoveAll(ctx, dir); err != nil {
				t.Errorf("removing Spring test directory %q: %v", dir, err)
			}
		})
	}
	return dir
}

// Exclude local build output when making the isolated source tree used by azd x build.
type extensionSourceFS struct{ fs.FS }

func (source extensionSourceFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(source.FS, name)
	if err != nil {
		return nil, err
	}
	if name != "." {
		return entries, nil
	}
	filtered := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() != "bin" {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}
