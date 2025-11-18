// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dotnet

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestIsSingleFileAspireHost(t *testing.T) {
	tests := []struct {
		name          string
		setupDir      func(t *testing.T) string
		expectedValid bool
		expectError   bool
		errorContains string
	}{
		{
			name: "ValidSingleFileAppHost",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs with SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return apphostPath
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "ValidSingleFileAppHost_CaseInsensitive",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create AppHost.cs (different case) with SDK directive
				apphostPath := filepath.Join(dir, "AppHost.cs")
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return apphostPath
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "InvalidWrongFilename",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create Program.cs instead of apphost.cs
				programPath := filepath.Join(dir, "Program.cs")
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(programPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return programPath
			},
			expectedValid: false,
			expectError:   false,
		},
		{
			name: "InvalidMissingSDKDirective",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs WITHOUT SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return apphostPath
			},
			expectedValid: false,
			expectError:   false,
		},
		{
			name: "InvalidHasSiblingCsproj",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs with SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Create a sibling .csproj file (should invalidate single-file detection)
				csprojPath := filepath.Join(dir, "MyApp.csproj")
				err = os.WriteFile(csprojPath, []byte("<Project></Project>"), osutil.PermissionFile)
				require.NoError(t, err)

				return apphostPath
			},
			expectedValid: false,
			expectError:   false,
		},
		{
			name: "InvalidHasSiblingFsproj",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs with SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Create a sibling .fsproj file (F# project - should also invalidate)
				fsprojPath := filepath.Join(dir, "MyApp.fsproj")
				err = os.WriteFile(fsprojPath, []byte("<Project></Project>"), osutil.PermissionFile)
				require.NoError(t, err)

				return apphostPath
			},
			expectedValid: false,
			expectError:   false,
		},
		{
			name: "InvalidHasSiblingVbproj",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs with SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Create a sibling .vbproj file (VB.NET project - should also invalidate)
				vbprojPath := filepath.Join(dir, "MyApp.vbproj")
				err = os.WriteFile(vbprojPath, []byte("<Project></Project>"), osutil.PermissionFile)
				require.NoError(t, err)

				return apphostPath
			},
			expectedValid: false,
			expectError:   false,
		},
		{
			name: "ValidWithOtherCsFiles",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs with SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Create other .cs files (should not invalidate)
				err = os.WriteFile(filepath.Join(dir, "Program.cs"), []byte("// other file"), osutil.PermissionFile)
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(dir, "Startup.cs"), []byte("// other file"), osutil.PermissionFile)
				require.NoError(t, err)

				return apphostPath
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "ErrorNonExistentFile",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Return path to non-existent file
				return filepath.Join(dir, SingleFileAspireHostName)
			},
			expectedValid: false,
			expectError:   true,
			errorContains: "reading file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtx := mocks.NewMockContext(context.Background())
			cli := NewCli(mockCtx.CommandRunner)

			filePath := tt.setupDir(t)
			isValid, err := cli.IsSingleFileAspireHost(filePath)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					require.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedValid, isValid)
			}
		})
	}
}

func TestIsAspireHostProject(t *testing.T) {
	tests := []struct {
		name          string
		setupDir      func(t *testing.T) string
		mockMsbuild   func(commandRunner *mockexec.MockCommandRunner, projectPath string)
		expectedValid bool
		expectError   bool
		errorContains string
	}{
		{
			name: "ValidProjectWithIsAspireHostProperty",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				csprojPath := filepath.Join(dir, "MyAppHost.csproj")
				content := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <IsAspireHost>true</IsAspireHost>
  </PropertyGroup>
</Project>`
				err := os.WriteFile(csprojPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return csprojPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				// Mock msbuild response with IsAspireHost=true
				commandRunner.When(func(args exec.RunArgs, command string) bool {
					return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == "msbuild"
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					jsonOutput := `{"Properties":{"IsAspireHost":"true"},` +
						`"Items":{"ProjectCapability":[]}}`
					return exec.NewRunResult(0, jsonOutput, ""), nil
				})
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "ValidProjectWithAspireCapability",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				csprojPath := filepath.Join(dir, "MyAppHost.csproj")
				content := `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectCapability Include="Aspire" />
  </ItemGroup>
</Project>`
				err := os.WriteFile(csprojPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return csprojPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				// Mock msbuild response with Aspire capability
				commandRunner.When(func(args exec.RunArgs, command string) bool {
					return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == "msbuild"
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(
						0,
						`{"Properties":{"IsAspireHost":""},"Items":{"ProjectCapability":[{"Identity":"Aspire"}]}}`,
						"",
					), nil
				})
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "ValidProjectWithBoth",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				csprojPath := filepath.Join(dir, "MyAppHost.csproj")
				content := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <IsAspireHost>true</IsAspireHost>
  </PropertyGroup>
  <ItemGroup>
    <ProjectCapability Include="Aspire" />
  </ItemGroup>
</Project>`
				err := os.WriteFile(csprojPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return csprojPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				// Mock msbuild response with both property and capability
				commandRunner.When(func(args exec.RunArgs, command string) bool {
					return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == "msbuild"
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(
						0,
						`{"Properties":{"IsAspireHost":"true"},"Items":{"ProjectCapability":[{"Identity":"Aspire"}]}}`,
						"",
					), nil
				})
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "InvalidProjectNoAspireMarkers",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				csprojPath := filepath.Join(dir, "MyApp.csproj")
				content := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`
				err := os.WriteFile(csprojPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return csprojPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				// Mock msbuild response without Aspire markers
				commandRunner.When(func(args exec.RunArgs, command string) bool {
					return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == "msbuild"
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					jsonOutput := `{"Properties":{"IsAspireHost":""},` +
						`"Items":{"ProjectCapability":[]}}`
					return exec.NewRunResult(0, jsonOutput, ""), nil
				})
			},
			expectedValid: false,
			expectError:   false,
		},
		{
			name: "ValidFsprojWithAspire",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				fsprojPath := filepath.Join(dir, "MyAppHost.fsproj")
				content := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <IsAspireHost>true</IsAspireHost>
  </PropertyGroup>
</Project>`
				err := os.WriteFile(fsprojPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return fsprojPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				commandRunner.When(func(args exec.RunArgs, command string) bool {
					return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == "msbuild"
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					jsonOutput := `{"Properties":{"IsAspireHost":"true"},` +
						`"Items":{"ProjectCapability":[]}}`
					return exec.NewRunResult(0, jsonOutput, ""), nil
				})
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "ValidSingleFileCsFile",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs with SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return apphostPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				// No msbuild mock needed - .cs files don't call msbuild
			},
			expectedValid: true,
			expectError:   false,
		},
		{
			name: "InvalidSingleFileCsFileWithSiblingProject",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				// Create apphost.cs with SDK directive
				apphostPath := filepath.Join(dir, SingleFileAspireHostName)
				content := `#:sdk Aspire.AppHost.Sdk
using Aspire.Hosting;

var builder = DistributedApplication.CreateBuilder(args);
builder.Build().Run();`
				err := os.WriteFile(apphostPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)

				// Create sibling project file
				csprojPath := filepath.Join(dir, "MyApp.csproj")
				err = os.WriteFile(csprojPath, []byte("<Project></Project>"), osutil.PermissionFile)
				require.NoError(t, err)

				return apphostPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				// No msbuild mock needed - .cs files don't call msbuild
			},
			expectedValid: false,
			expectError:   false,
		},
		{
			name: "ErrorMsbuildFailure",
			setupDir: func(t *testing.T) string {
				dir := t.TempDir()
				csprojPath := filepath.Join(dir, "MyAppHost.csproj")
				content := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <IsAspireHost>true</IsAspireHost>
  </PropertyGroup>
</Project>`
				err := os.WriteFile(csprojPath, []byte(content), osutil.PermissionFile)
				require.NoError(t, err)
				return csprojPath
			},
			mockMsbuild: func(commandRunner *mockexec.MockCommandRunner, projectPath string) {
				// Mock msbuild failure
				commandRunner.When(func(args exec.RunArgs, command string) bool {
					return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == "msbuild"
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.RunResult{}, &exec.ExitError{ExitCode: 1}
				})
			},
			expectedValid: false,
			expectError:   true,
			errorContains: "running dotnet msbuild",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtx := mocks.NewMockContext(context.Background())
			cli := NewCli(mockCtx.CommandRunner)

			projectPath := tt.setupDir(t)
			if tt.mockMsbuild != nil {
				tt.mockMsbuild(mockCtx.CommandRunner, projectPath)
			}

			isValid, err := cli.IsAspireHostProject(*mockCtx.Context, projectPath)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					require.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedValid, isValid)
			}
		})
	}
}

func TestDotNetProjectExtensions(t *testing.T) {
	// Verify the constants are set correctly
	require.Equal(t, []string{".csproj", ".fsproj", ".vbproj"}, DotNetProjectExtensions)
}

func TestAspireConstants(t *testing.T) {
	// Verify the constants are set correctly
	require.Equal(t, "apphost.cs", SingleFileAspireHostName)
	require.Equal(t, "#:sdk Aspire.AppHost.Sdk", AspireSdkDirective)
}
