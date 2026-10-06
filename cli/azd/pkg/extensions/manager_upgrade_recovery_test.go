// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

type recoveryConfigManager struct {
	config.FileConfigManager
	saveCalls int
	failAt    int
	saveError error
}

func TestPrepareUpgradeRecoveryRejectsUnsafeInstalledIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "foo/../bar", "nested/bar", `nested\bar`, `foo\..\bar`} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			root := filepath.Join(configDir, "extensions")
			neighbor := filepath.Join(root, "bar", "installed.exe")
			nested := filepath.Join(root, "nested", "bar", "installed.exe")
			for _, path := range []string{neighbor, nested} {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte("unchanged"), 0o600))
			}
			before := []string{}
			require.NoError(t, filepath.WalkDir(configDir, func(path string, entry os.DirEntry, err error) error {
				if err == nil {
					before = append(before, path)
				}
				return err
			}))
			manager := &Manager{}
			finish, err := manager.prepareUpgradeRecovery(t.Context(), &Extension{Id: id, Version: "1.0.0"})
			require.ErrorContains(t, err, "invalid installed extension directory")
			require.Nil(t, finish)
			after := []string{}
			require.NoError(t, filepath.WalkDir(configDir, func(path string, entry os.DirEntry, err error) error {
				if err == nil {
					after = append(after, path)
				}
				return err
			}))
			require.Equal(t, before, after, "validation must precede backup staging or directory mutation")
			for _, path := range []string{neighbor, nested} {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, "unchanged", string(data))
			}
		})
	}
}

func (m *recoveryConfigManager) Save(cfg config.Config, path string) error {
	m.saveCalls++
	if m.saveCalls == m.failAt {
		return m.saveError
	}
	return m.FileConfigManager.Save(cfg, path)
}

func TestUpgradeRecoveryPreservesInstalledState(t *testing.T) {
	tests := []struct {
		name             string
		artifact         string
		entry            string
		checksum         ExtensionChecksum
		cancel           bool
		cancelOnDownload bool
		mutateInstalled  bool
		failSave         int
		version          string
		wantError        string
	}{
		{name: "missing artifact", artifact: "missing", wantError: "failed to download artifact"},
		{
			name: "checksum", artifact: "replacement",
			checksum:  ExtensionChecksum{Algorithm: "sha256", Value: "incorrect"},
			wantError: "checksum validation failed",
		},
		{name: "archive", artifact: "invalid.zip", wantError: "failed to extract zip"},
		{name: "entry point", artifact: "replacement", entry: "absent", wantError: "executable permission"},
		{name: "missing release", artifact: "replacement", version: "9.0.0", wantError: "was not found"},
		{name: "cancellation", artifact: "cancel", cancel: true, wantError: "context canceled"},
		{name: "download cancellation", artifact: "cancel", cancelOnDownload: true, wantError: "context canceled"},
		{
			name: "download mutation", artifact: "cancel", cancelOnDownload: true,
			mutateInstalled: true, wantError: "context canceled",
		},
		{name: "uninstall save", artifact: "replacement", failSave: 1, wantError: "injected save failure"},
		{name: "install save", artifact: "replacement", failSave: 2, wantError: "injected save failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockCtx := mocks.NewMockContext(t.Context())
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			configManager := &recoveryConfigManager{
				FileConfigManager: mockCtx.ConfigManager,
				saveError:         errors.New("injected save failure"),
			}
			userConfig := config.NewUserConfigManager(configManager)
			sourceManager := NewSourceManager(mockCtx.Container, userConfig, mockCtx.HttpClient)
			runner := lazy.NewLazy(func() (*Runner, error) {
				return NewRunner(mockCtx.CommandRunner), nil
			})
			manager, err := NewManager(userConfig, sourceManager, runner, mockCtx.HttpClient)
			require.NoError(t, err)

			artifactDir := t.TempDir()
			oldFile, err := os.CreateTemp(artifactDir, "recovery-artifact-*")
			require.NoError(t, err)
			oldPath := oldFile.Name()
			_, err = oldFile.WriteString("installed bytes")
			require.NoError(t, err)
			require.NoError(t, oldFile.Close())
			platform := runtime.GOOS + "/" + runtime.GOARCH
			metadata := &ExtensionMetadata{
				Id: "test.recovery", Source: "test",
				Versions: []ExtensionVersion{{
					Version: "1.0.0", EntryPoint: filepath.Base(oldPath),
					Artifacts: map[string]ExtensionArtifact{platform: {URL: oldPath}},
				}},
			}
			_, err = manager.Install(t.Context(), metadata, "1.0.0")
			require.NoError(t, err)
			installed, err := manager.GetInstalled(FilterOptions{Id: metadata.Id})
			require.NoError(t, err)
			installed.InstalledAsDependency = true
			if test.mutateInstalled {
				installed.Dependencies = []ExtensionDependency{{Id: "test.dependency", Version: "1.0.0"}}
			}
			require.NoError(t, manager.UpdateInstalled(installed))
			before, err := json.Marshal(installed)
			require.NoError(t, err)
			installedPath := filepath.Join(configDir, installed.Path)
			historyPath := filepath.Join(filepath.Dir(installedPath), "history.json")
			require.NoError(t, os.WriteFile(historyPath, []byte("preserved history"), 0o600))

			newPath := filepath.Join(artifactDir, filepath.Base(oldPath)+"."+test.artifact)
			if test.artifact != "missing" {
				require.NoError(t, os.WriteFile(newPath, []byte("replacement bytes"), 0o600))
			}
			if test.cancel || test.cancelOnDownload {
				newPath = "https://test.example.com/cancel"
			}
			entry := test.entry
			if entry == "" {
				entry = filepath.Base(newPath)
			}
			metadata.Versions = []ExtensionVersion{{
				Version: "1.1.0", EntryPoint: entry,
				Artifacts: map[string]ExtensionArtifact{platform: {URL: newPath, Checksum: test.checksum}},
			}}
			ctx := t.Context()
			configManager.saveCalls = 0
			configManager.failAt = test.failSave
			downloadCanceled := false
			if test.cancel || test.cancelOnDownload {
				cancelCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				if test.cancel {
					cancel()
				}
				ctx = cancelCtx
				mockCtx.HttpClient.When(func(request *http.Request) bool {
					return request.Method == http.MethodGet && request.URL.String() == newPath
				}).RespondFn(func(request *http.Request) (*http.Response, error) {
					if test.cancelOnDownload {
						_, statErr := os.Stat(installedPath)
						require.ErrorIs(t, statErr, os.ErrNotExist)
						cancel()
						downloadCanceled = true
						if test.mutateInstalled {
							installed.Version = "9.0.0"
							installed.Dependencies[0].Version = "9.0.0"
						}
					}
					require.ErrorIs(t, request.Context().Err(), context.Canceled)
					return nil, request.Context().Err()
				})
			}
			_, _, err = manager.Upgrade(ctx, metadata, DefaultUpgradeOptions(test.version))
			require.ErrorContains(t, err, test.wantError)
			if test.cancel || test.cancelOnDownload {
				require.ErrorIs(t, err, context.Canceled)
			}
			if test.cancelOnDownload {
				require.True(t, downloadCanceled)
			}
			if test.failSave != 0 {
				require.ErrorIs(t, err, configManager.saveError)
			}
			require.NoError(t, manager.ReloadUserConfig())
			restored, err := manager.GetInstalled(FilterOptions{Id: metadata.Id})
			require.NoError(t, err)
			after, err := json.Marshal(restored)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			content, err := os.ReadFile(installedPath)
			require.NoError(t, err)
			require.Equal(t, "installed bytes", string(content))
			content, err = os.ReadFile(historyPath)
			require.NoError(t, err)
			require.Equal(t, "preserved history", string(content))
			backups, err := filepath.Glob(filepath.Join(configDir, "extensions", ".upgrade-backup-*"))
			require.NoError(t, err)
			require.Empty(t, backups)
		})
	}
}
