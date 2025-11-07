// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestDotNetAppHostDetector_DetectProject(t *testing.T) {
	tests := []struct {
		name         string
		setupDir     func(t *testing.T) (string, []fs.DirEntry)
		expectedPath string // Relative to setupDir
		expectNil    bool
	}{
		{
			name: "DetectsSingleFileAppHost",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create valid single-file apphost
				apphostPath := filepath.Join(dir, dotnet.SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectedPath: dotnet.SingleFileAspireHostName,
			expectNil:    false,
		},
		{
			name: "DetectsSingleFileAppHost_CaseInsensitive",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create AppHost.cs (different case)
				apphostPath := filepath.Join(dir, "AppHost.cs")
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectedPath: "AppHost.cs",
			expectNil:    false,
		},
		{
			name: "IgnoresOtherCsFiles",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create various .cs files that are NOT apphost.cs
				err := os.WriteFile(filepath.Join(dir, "Program.cs"), []byte("// program"), osutil.PermissionFile)
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(dir, "Startup.cs"), []byte("// startup"), osutil.PermissionFile)
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(dir, "Test.cs"), []byte("// test"), osutil.PermissionFile)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectNil: true,
		},
		{
			name: "RejectSingleFileWithSiblingCsproj",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create apphost.cs
				apphostPath := filepath.Join(dir, dotnet.SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Create sibling .csproj (should invalidate single-file)
				csprojPath := filepath.Join(dir, "MyApp.csproj")
				err = os.WriteFile(csprojPath, []byte("<Project></Project>"), osutil.PermissionFile)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectNil: true, // Single-file rejected, and .csproj is not an Aspire host (will be mocked as false)
		},
		{
			name: "RejectsSingleFileWithoutSDKDirective",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create apphost.cs WITHOUT SDK directive
				apphostPath := filepath.Join(dir, dotnet.SingleFileAspireHostName)
				content := `using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectNil: true,
		},
		{
			name: "SingleFileWinsWhenValid",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create valid single-file apphost
				apphostPath := filepath.Join(dir, dotnet.SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Also create other random files (not .NET projects)
				err = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# README"), osutil.PermissionFile)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectedPath: dotnet.SingleFileAspireHostName,
			expectNil:    false,
		},
		{
			name: "EmptyDirectory",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectNil: true,
		},
		{
			name: "OnlyDirectories",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create only subdirectories
				err := os.MkdirAll(filepath.Join(dir, "src"), osutil.PermissionDirectory)
				require.NoError(t, err)
				err = os.MkdirAll(filepath.Join(dir, "test"), osutil.PermissionDirectory)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectNil: true,
		},
		{
			name: "SingleFileWithOtherNonProjectFiles",
			setupDir: func(t *testing.T) (string, []fs.DirEntry) {
				dir := t.TempDir()
				// Create valid single-file apphost
				apphostPath := filepath.Join(dir, dotnet.SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Create other non-.NET files (should not interfere)
				err = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# README"), osutil.PermissionFile)
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), osutil.PermissionFile)
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(dir, "Program.cs"), []byte("// other cs"), osutil.PermissionFile)
				require.NoError(t, err)

				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				return dir, entries
			},
			expectedPath: dotnet.SingleFileAspireHostName,
			expectNil:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtx := mocks.NewMockContext(context.Background())

			// Mock msbuild calls to return false for non-Aspire projects
			mockCtx.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == "msbuild"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				// Return non-Aspire project result
				return exec.NewRunResult(0, `{"Properties":{"IsAspireHost":""},"Items":{"ProjectCapability":[]}}`, ""), nil
			})

			cli := dotnet.NewCli(mockCtx.CommandRunner)
			detector := &dotNetAppHostDetector{dotnetCli: cli}

			dir, entries := tt.setupDir(t)
			project, err := detector.DetectProject(*mockCtx.Context, dir, entries)

			require.NoError(t, err)

			if tt.expectNil {
				require.Nil(t, project, "Expected no project detection")
			} else {
				require.NotNil(t, project, "Expected project to be detected")
				require.Equal(t, DotNetAppHost, project.Language)
				expectedFullPath := filepath.Join(dir, tt.expectedPath)
				require.Equal(t, expectedFullPath, project.Path)
				require.Contains(t, project.DetectionRule, "Aspire AppHost")
			}
		})
	}
}

func TestDotNetAppHostDetector_Language(t *testing.T) {
	detector := &dotNetAppHostDetector{}
	require.Equal(t, DotNetAppHost, detector.Language())
}
