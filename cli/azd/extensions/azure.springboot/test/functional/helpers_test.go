// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		t.Skip("set CLI_TEST_AZD_PATH to an official azd 1.37.2 or newer binary to run Spring functional tests")
	}
	require.True(t, filepath.IsAbs(os.Getenv("CLI_TEST_AZD_PATH")), "CLI_TEST_AZD_PATH must be absolute")
	cli := azdcli.NewCLI(t)
	cli.Env = isolatedEnvironment(
		"DOCKER_CONFIG="+tempDirWithDiagnostics(t),
		"AZD_CONFIG_DIR="+tempDirWithDiagnostics(t),
		"AZURE_DEV_COLLECT_TELEMETRY=no",
		"AZD_FORCE_TTY=false",
		"NO_COLOR=1",
		"PATH="+filepath.Dir(cli.AzdPath)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return cli
}

func isolatedEnvironment(overrides ...string) []string {
	replaced := map[string]bool{}
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		replaced[strings.ToUpper(key)] = true
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !replaced[strings.ToUpper(key)] {
			environment = append(environment, entry)
		}
	}
	return append(environment, overrides...)
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
