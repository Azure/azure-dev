// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestExtensionInstall_ExplicitVersion(t *testing.T) {
	const older, newer = "1.0.0-beta.29", "1.0.0-beta.30"
	tests := []struct {
		name            string
		installed       string
		installedSource string
		requested       string
		target          string
		force           bool
		interactive     bool
		confirm         bool
		wantError       bool
		wantInstall     bool
	}{
		{
			name: "downgrade requires force", installed: newer, installedSource: "test",
			requested: older, target: older, wantError: true,
		},
		{
			name: "source change requires force", installed: newer, installedSource: "other",
			requested: older, target: older, wantError: true,
		},
		{
			name: "source change upgrade requires force", installed: older, installedSource: "other",
			requested: newer, target: newer, wantError: true,
		},
		{
			name: "same version source change requires force", installed: older, installedSource: "other",
			requested: older, target: older, wantError: true,
		},
		{
			name: "same version is idempotent", installed: older, installedSource: "test",
			requested: older, target: older,
		},
		{
			name: "forced downgrade", installed: newer, installedSource: "test",
			requested: older, target: older, force: true, wantInstall: true,
		},
		{
			name: "forced source change", installed: newer, installedSource: "other",
			requested: older, target: older, force: true, wantInstall: true,
		},
		{name: "fresh exact install", requested: older, target: older, wantInstall: true},
		{
			name: "same source upgrade", installed: older, installedSource: "test",
			requested: newer, target: newer, wantInstall: true,
		},
		{name: "default downgrade still skips", installed: newer, installedSource: "test", target: older},
		{
			name: "explicit latest requires force", installed: newer, installedSource: "test",
			requested: "latest", target: older, wantError: true,
		},
		{
			name: "interactive decline", installed: newer, installedSource: "test",
			requested: older, target: older, interactive: true,
		},
		{
			name: "interactive confirmation", installed: newer, installedSource: "test",
			requested: older, target: older, interactive: true, confirm: true, wantInstall: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
			t.Setenv("NO_COLOR", "1")

			const id = "test.version"
			oldContent := []byte("previous installed artifact")
			newContent := []byte("requested artifact " + tt.target)
			artifact, err := os.CreateTemp(t.TempDir(), "azd-version-contract-*.bin")
			require.NoError(t, err)
			_, err = artifact.Write(newContent)
			require.NoError(t, err)
			require.NoError(t, artifact.Close())
			entryPoint := filepath.Base(artifact.Name())
			relativePath := filepath.Join("extensions", id, entryPoint)
			installedPath := filepath.Join(configDir, relativePath)
			installed := map[string]*extensions.Extension{}
			if tt.installed != "" {
				require.NoError(t, os.MkdirAll(filepath.Dir(installedPath), 0o700))
				require.NoError(t, os.WriteFile(installedPath, oldContent, 0o600))
				installed[id] = &extensions.Extension{
					Id: id, Version: tt.installed, Source: tt.installedSource, Path: relativePath,
				}
			}

			mockCtx := mocks.NewMockContext(t.Context())
			manager, sourceManager := createUpgradeTestManager(t, mockCtx, installed,
				"https://test.example.com/version-registry.json", testRegistry(&extensions.ExtensionMetadata{
					Id: id, Source: "test",
					Versions: []extensions.ExtensionVersion{{
						Version: tt.target, EntryPoint: entryPoint,
						Artifacts: map[string]extensions.ExtensionArtifact{
							runtime.GOOS: {URL: artifact.Name()},
						},
					}},
				}))
			console := mockinput.NewMockConsole()
			if tt.interactive {
				console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(tt.confirm)
			}
			action := &extensionInstallAction{
				args: []string{id},
				flags: &extensionInstallFlags{
					version: tt.requested, source: "test", force: tt.force, noDependencies: true,
					global: &internal.GlobalCommandOptions{NoPrompt: !tt.interactive},
				},
				console: console, sourceManager: sourceManager, extensionManager: manager,
			}

			result, runErr := action.Run(t.Context())
			require.NoError(t, manager.ReloadUserConfig())
			record, err := manager.GetInstalled(extensions.FilterOptions{Id: id})
			require.NoError(t, err)
			gotContent, err := os.ReadFile(filepath.Join(configDir, record.Path))
			require.NoError(t, err)
			if tt.wantInstall {
				require.Equal(t, tt.target, record.Version)
				require.Equal(t, "test", record.Source)
				require.Equal(t, sha256.Sum256(newContent), sha256.Sum256(gotContent))
			} else {
				require.Equal(t, tt.installed, record.Version)
				require.Equal(t, tt.installedSource, record.Source)
				require.Equal(t, sha256.Sum256(oldContent), sha256.Sum256(gotContent))
			}
			if tt.wantError {
				require.Error(t, runErr)
				require.Nil(t, result)
				suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](runErr)
				require.True(t, ok)
				require.Contains(t, suggestion.Suggestion, "--force")
				require.Contains(t, suggestion.Suggestion, "AZD_NON_INTERACTIVE=false")
				require.Contains(t, runErr.Error(), "--version")
				require.Contains(t, runErr.Error(), tt.installed)
				if tt.installedSource != "test" {
					require.Contains(t, runErr.Error(), `source "other"`)
					require.Contains(t, runErr.Error(), `source "test"`)
				}
				require.Contains(t, console.SpinnerOps(), mockinput.SpinnerOp{
					Op: mockinput.SpinnerOpStop, Message: extensionTaskMessage("Installing", id), Format: input.StepFailed,
				})
			} else {
				require.NoError(t, runErr)
				require.NotNil(t, result)
				if !tt.wantInstall {
					require.Nil(t, result.Message)
				}
			}
		})
	}
}
