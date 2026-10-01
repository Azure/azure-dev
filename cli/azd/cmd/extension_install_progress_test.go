// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockzip"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type installProgressBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *installProgressBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *installProgressBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type installProgressConsole struct {
	input.Console
	t      *testing.T
	writer *installProgressBuffer
}

func (c *installProgressConsole) ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType) {
	c.t.Helper()
	before := strings.Count(c.writer.String(), title)
	c.Console.ShowSpinner(ctx, title, format)
	if !c.IsSpinnerInteractive() {
		// Wait for asynchronous painting so fast local installs cannot hide duplicate progress.
		require.Eventually(c.t, func() bool {
			return strings.Count(c.writer.String(), title) > before
		}, 2*time.Second, 5*time.Millisecond)
	}
}

func TestExtensionInstall_Progress(t *testing.T) {
	const id = "test.progress"
	tests := []struct {
		name         string
		installed    string
		force        bool
		confirm      bool
		incompatible bool
		dependency   bool
		badChecksum  bool
		wantStarts   int
		wantStatus   string
	}{
		{name: "fresh", wantStarts: 1, wantStatus: "Done:"},
		{name: "forced reinstall", installed: "1.0.0", force: true, wantStarts: 1, wantStatus: "Done:"},
		{name: "upgrade", installed: "0.9.0", wantStarts: 1, wantStatus: "Done:"},
		{name: "already installed", installed: "1.0.0", wantStarts: 1, wantStatus: "Skipped:"},
		{name: "confirmed downgrade", installed: "2.0.0", confirm: true, wantStarts: 2, wantStatus: "Done:"},
		{name: "compatibility warning", incompatible: true, wantStarts: 2, wantStatus: "Done:"},
		{name: "dependency installed", dependency: true, wantStarts: 1, wantStatus: "Done:"},
		{name: "artifact failure", badChecksum: true, wantStarts: 1, wantStatus: "Failed:"},
		{
			name: "dependency failure", dependency: true, badChecksum: true,
			wantStarts: 1, wantStatus: "Failed:",
		},
	}
	for _, tty := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("tty=%t/%s", tty, tt.name), func(t *testing.T) {
				configDir := t.TempDir()
				t.Setenv("AZD_CONFIG_DIR", configDir)
				t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
				t.Setenv("NO_COLOR", "1")
				t.Setenv("TERM", "xterm")

				const entryPoint = "extension.bin"
				archive, err := mockzip.Zip([]mockzip.File{{Name: entryPoint, Content: "installed artifact"}})
				require.NoError(t, err)
				artifactFile, err := os.CreateTemp(t.TempDir(), "azd-install-progress-*.zip")
				require.NoError(t, err)
				_, err = artifactFile.Write(archive.Bytes())
				require.NoError(t, err)
				require.NoError(t, artifactFile.Close())
				artifactPath := artifactFile.Name()
				artifact := extensions.ExtensionArtifact{URL: artifactPath}
				if tt.badChecksum {
					artifact.Checksum = extensions.ExtensionChecksum{Algorithm: "sha256", Value: strings.Repeat("0", 64)}
				}
				metadata := &extensions.ExtensionMetadata{
					Id: id, Source: "test",
					Versions: []extensions.ExtensionVersion{{
						Version: "1.0.0", EntryPoint: entryPoint,
						Artifacts: map[string]extensions.ExtensionArtifact{runtime.GOOS: artifact},
					}},
				}
				registry := testRegistry(metadata)
				if tt.dependency {
					metadata.Versions[0].Dependencies = []extensions.ExtensionDependency{{Id: "test.dependency"}}
					metadata.Versions[0].Artifacts[runtime.GOOS] = extensions.ExtensionArtifact{URL: artifactPath}
					registry.Extensions = append(registry.Extensions, &extensions.ExtensionMetadata{
						Id: "test.dependency", Source: "test",
						Versions: []extensions.ExtensionVersion{{
							Version: "1.0.0", EntryPoint: entryPoint,
							Artifacts: map[string]extensions.ExtensionArtifact{runtime.GOOS: artifact},
						}},
					})
				}
				if tt.incompatible {
					metadata.Versions = append(metadata.Versions, extensions.ExtensionVersion{
						Version: "2.0.0", RequiredAzdVersion: ">=99.0.0",
					})
				}
				installed := map[string]*extensions.Extension{}
				if tt.installed != "" {
					relativePath := filepath.Join("extensions", id, entryPoint)
					path := filepath.Join(configDir, relativePath)
					require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
					require.NoError(t, os.WriteFile(path, []byte("previous artifact"), 0o600))
					installed[id] = &extensions.Extension{
						Id: id, Version: tt.installed, Source: "test", Path: relativePath,
					}
				}
				mockCtx := mocks.NewMockContext(t.Context())
				manager, sources := createUpgradeTestManagerWithOptions(t, mockCtx, installed,
					"https://test.example.com/progress-registry.json", registry,
					extensions.ManagerOptions{AzdVersion: semver.MustParse("1.34.1")})

				var promptConfig *input.ExternalPromptConfiguration
				if tt.confirm {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, err := w.Write([]byte(`{"status":"success","value":"true"}`))
						assert.NoError(t, err)
					}))
					t.Cleanup(server.Close)
					promptConfig = &input.ExternalPromptConfiguration{
						Endpoint: server.URL, Key: "test", Transporter: http.DefaultClient,
					}
				}
				writer := &installProgressBuffer{}
				handles := &installProgressBuffer{}
				console := &installProgressConsole{
					Console: input.NewConsole(!tt.confirm, tty, input.Writers{Output: writer},
						input.ConsoleHandles{Stdin: os.Stdin, Stdout: handles, Stderr: os.Stderr},
						&output.NoneFormatter{}, promptConfig),
					t: t, writer: writer,
				}
				t.Cleanup(func() { console.StopSpinner(t.Context(), "", input.Step) })
				cmd := &cobra.Command{}
				flags := newExtensionInstallFlags(cmd, &internal.GlobalCommandOptions{NoPrompt: !tt.confirm})
				args := []string{id, "--source=test"}
				if tt.force {
					args = append(args, "--force")
				}
				require.NoError(t, cmd.ParseFlags(args))
				action := newExtensionInstallAction(cmd, cmd.Flags().Args(), flags, console, manager, sources, nil)
				result, runErr := action.Run(t.Context())
				require.False(t, console.IsSpinnerRunning(t.Context()))
				require.Equal(t, tty, console.IsSpinnerInteractive())
				text := writer.String()
				t.Logf("installer output:\n%s", text)
				require.Empty(t, handles.String(), "output must use the injected writer")
				require.Equal(t, 1, strings.Count(text, tt.wantStatus+" Installing "+id), text)
				if !tty {
					var starts int
					for line := range strings.SplitSeq(text, "\n") {
						if strings.TrimSpace(line) == "Installing "+id {
							starts++
						}
					}
					// A warning deliberately prints a final status before resuming progress.
					wantLines := tt.wantStarts
					if tt.incompatible {
						wantLines++
					}
					require.Equal(t, wantLines, starts, text)
					require.NotContains(t, text, "\x1b[")
				} else {
					require.Contains(t, text, "\r", "TTY progress should update in place")
				}
				if tt.badChecksum {
					require.ErrorContains(t, runErr, "checksum")
					require.Nil(t, result)
					require.NotContains(t, text, "Done:")
					records, err := manager.ListInstalled()
					require.NoError(t, err)
					require.NotContains(t, records, id)
					return
				}
				require.NoError(t, runErr)
				require.NotNil(t, result)
				record, err := manager.GetInstalled(extensions.FilterOptions{Id: id})
				require.NoError(t, err)
				require.Equal(t, "1.0.0", record.Version)
				content, err := os.ReadFile(filepath.Join(configDir, record.Path))
				require.NoError(t, err)
				if tt.wantStatus == "Skipped:" {
					require.Nil(t, result.Message)
					require.Equal(t, "previous artifact", string(content))
				} else {
					require.NotNil(t, result.Message)
					require.Contains(t, text, "Done: Installing "+id+" (1.0.0)")
					require.Equal(t, "installed artifact", string(content))
				}
				if tt.dependency {
					require.Contains(t, text, "Done: Installing test.dependency dependency (1.0.0)")
				}
				if tt.incompatible {
					require.Contains(t, text, "99.0.0")
				}
			})
		}
	}
}
