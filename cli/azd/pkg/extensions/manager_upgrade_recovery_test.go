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
	partial   bool
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
			finish, err := manager.prepareUpgradeRecovery(
				t.Context(), &Extension{Id: id, Version: "1.0.0"}, "test.replacement",
			)
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

func TestPrepareUpgradeRecoveryRejectsMismatchedReplacementID(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	manager := &Manager{}
	finish, err := manager.prepareUpgradeRecovery(
		t.Context(), &Extension{Id: "test.installed", Version: "1.0.0"}, "test.replacement",
	)
	require.ErrorContains(t, err, "invalid replacement extension directory")
	require.Nil(t, finish)
	require.NoDirExists(t, filepath.Join(configDir, "extensions"))
}

func (m *recoveryConfigManager) Save(cfg config.Config, path string) error {
	m.saveCalls++
	if m.saveCalls == m.failAt {
		if m.partial {
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				return errors.Join(m.saveError, err)
			}
		}
		return m.saveError
	}
	return m.FileConfigManager.Save(cfg, path)
}

func TestPrepareUpgradeRecoveryRejectsUnsafeReplacementIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../other", "nested/other", `nested\other`} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			extensionRoot := filepath.Join(configDir, "extensions")
			extensionDir := filepath.Join(extensionRoot, "test.recovery")
			require.NoError(t, os.MkdirAll(extensionDir, 0o700))
			installedPath := filepath.Join(extensionDir, "installed")
			require.NoError(t, os.WriteFile(installedPath, []byte("installed bytes"), 0o600))
			manager := &Manager{}
			finish, err := manager.prepareUpgradeRecovery(t.Context(), &Extension{Id: "test.recovery"}, id)
			require.ErrorContains(t, err, "invalid replacement extension directory")
			require.Nil(t, finish)
			content, err := os.ReadFile(installedPath)
			require.NoError(t, err)
			require.Equal(t, "installed bytes", string(content))
			entries, err := os.ReadDir(extensionRoot)
			require.NoError(t, err)
			require.Len(t, entries, 1, "validation must precede backup staging")
		})
	}
}

func TestPrepareUpgradeRecoverySnapshotsInstalledMetadata(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	mockCtx := mocks.NewMockContext(t.Context())
	configManager := config.NewFileConfigManager(config.NewManager())
	userConfig := config.NewUserConfigManager(configManager)
	sourceManager := NewSourceManager(mockCtx.Container, userConfig, mockCtx.HttpClient)
	runner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockCtx.CommandRunner), nil
	})
	manager, err := NewManager(userConfig, sourceManager, runner, mockCtx.HttpClient)
	require.NoError(t, err)

	installed := &Extension{
		Id:                    "test.snapshot",
		Version:               "1.0.0",
		Source:                "original",
		InstalledAsDependency: true,
		Dependencies:          []ExtensionDependency{{Id: "test.dependency", Version: "1.0.0"}},
	}
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{installed.Id: installed}))
	require.NoError(t, manager.configManager.Save(manager.userConfig))
	extensionDir := filepath.Join(configDir, "extensions", installed.Id)
	require.NoError(t, os.MkdirAll(extensionDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "installed"), []byte("installed bytes"), 0o600))
	before, err := json.Marshal(installed)
	require.NoError(t, err)

	finish, err := manager.prepareUpgradeRecovery(t.Context(), installed, installed.Id)
	require.NoError(t, err)
	installed.Version = "9.0.0"
	installed.Source = "mutated"
	installed.Dependencies[0].Version = "9.0.0"
	require.NoError(t, finish(t.Context(), true))

	require.NoError(t, manager.ReloadUserConfig())
	restored, err := manager.GetInstalled(FilterOptions{Id: installed.Id})
	require.NoError(t, err)
	after, err := json.Marshal(restored)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}

func TestUpgradeRecoveryPreservesInstalledState(t *testing.T) {
	tests := []struct {
		name              string
		artifact          string
		entry             string
		checksum          ExtensionChecksum
		cancel            bool
		cancelOnDownload  bool
		mutateInstalled   bool
		installDependency bool
		failSave          int
		version           string
		wantError         string
		replacementID     string
		recoverySave      bool
		partialSave       bool
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
		{
			name: "dependency install", artifact: "missing", installDependency: true,
			wantError: "failed to download artifact",
		},
		{name: "uninstall save", artifact: "replacement", failSave: 1, wantError: "injected save failure"},
		{name: "install save", artifact: "replacement", failSave: 2, wantError: "injected save failure"},
		{
			name: "case variant install save", artifact: "replacement", failSave: 2,
			replacementID: "TEST.RECOVERY", wantError: "injected save failure",
		},
		{
			name: "recovery save", artifact: "missing", failSave: 2, recoverySave: true,
			wantError: "failed to save restored installed extension metadata",
		},
		{
			name: "partial recovery save", artifact: "missing", failSave: 2, recoverySave: true, partialSave: true,
			wantError: "failed to save restored installed extension metadata",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockCtx := mocks.NewMockContext(t.Context())
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			configManager := &recoveryConfigManager{
				FileConfigManager: config.NewFileConfigManager(config.NewManager()),
				saveError:         errors.New("injected save failure"),
				partial:           test.partialSave,
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
			dependencies := []ExtensionDependency(nil)
			if test.installDependency {
				dependencyPath := filepath.Join(artifactDir, "dependency")
				require.NoError(t, os.WriteFile(dependencyPath, []byte("dependency bytes"), 0o600))
				dependency := &ExtensionMetadata{
					Id: "test.dependency", Source: metadata.Source,
					Versions: []ExtensionVersion{{
						Version: "2.0.0", EntryPoint: filepath.Base(dependencyPath),
						Artifacts: map[string]ExtensionArtifact{platform: {URL: dependencyPath}},
					}},
				}
				manager.sources = []Source{&mockSource{name: metadata.Source, extensions: []*ExtensionMetadata{dependency}}}
				dependencies = []ExtensionDependency{{Id: dependency.Id, Version: dependency.Versions[0].Version}}
			}
			metadata.Versions = []ExtensionVersion{{
				Version: "1.1.0", EntryPoint: entry,
				Artifacts:    map[string]ExtensionArtifact{platform: {URL: newPath, Checksum: test.checksum}},
				Dependencies: dependencies,
			}}
			if test.replacementID != "" {
				metadata.Id = test.replacementID
			}
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
			upgradeErr := err
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
			content, err := os.ReadFile(installedPath)
			require.NoError(t, err)
			require.Equal(t, "installed bytes", string(content))
			content, err = os.ReadFile(historyPath)
			require.NoError(t, err)
			require.Equal(t, "preserved history", string(content))
			backups, err := filepath.Glob(filepath.Join(configDir, "extensions", ".upgrade-backup-*"))
			require.NoError(t, err)
			if test.recoverySave {
				require.Len(t, backups, 1)
				require.ErrorContains(t, upgradeErr, fmt.Sprintf("%q", backups[0]))
				require.ErrorContains(t, upgradeErr, fmt.Sprintf("%q", filepath.Dir(installedPath)))
				configPath, err := config.GetUserConfigFilePath()
				require.NoError(t, err)
				persisted, err := os.ReadFile(configPath)
				require.NoError(t, err)
				if test.partialSave {
					require.Equal(t, "{", string(persisted), "simulate an interrupted metadata write")
				} else {
					require.NotContains(t, string(persisted), installed.Id, "the removed record needs manual recovery")
				}
				metadataPath := filepath.Join(backups[0], "metadata.json")
				content, err := os.ReadFile(metadataPath)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(content))
				if runtime.GOOS != "windows" {
					info, err := os.Stat(metadataPath)
					require.NoError(t, err)
					require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
					info, err = os.Stat(backups[0])
					require.NoError(t, err)
					require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
				}
				// Recover from the on-disk snapshot rather than the manager's in-memory record.
				previous := new(Extension)
				require.NoError(t, json.Unmarshal(content, previous))
				require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{previous.Id: previous}))
				configManager.failAt = 0
				require.NoError(t, manager.configManager.Save(manager.userConfig))
			} else {
				require.Empty(t, backups)
			}
			require.NoError(t, manager.ReloadUserConfig())
			restored, err := manager.GetInstalled(FilterOptions{Id: metadata.Id})
			require.NoError(t, err)
			after, err := json.Marshal(restored)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			records, err := manager.ListInstalled()
			require.NoError(t, err)
			wantRecords := 1
			if test.installDependency {
				wantRecords++
				dependency := records["test.dependency"]
				require.NotNil(t, dependency)
				require.Equal(t, "2.0.0", dependency.Version)
				require.True(t, dependency.InstalledAsDependency)
			}
			require.Len(t, records, wantRecords)
			require.Contains(t, records, installed.Id)
			if test.replacementID != "" {
				entries, err := os.ReadDir(filepath.Join(configDir, "extensions"))
				require.NoError(t, err)
				for _, entry := range entries {
					require.NotEqual(t, test.replacementID, entry.Name(), "failed replacement tree must be removed")
				}
			}
		})
	}
}
