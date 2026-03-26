// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dotnet

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	infraBicep "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// DotNetProvider exposes infrastructure provisioning using C# projects that leverage Azure.Provisioning.
// It compiles C# code to Bicep as an intermediate step, then delegates to the Bicep provider for deployment.
type DotNetProvider struct {
	bicepProvider *infraBicep.BicepProvider
	dotnetCli     *dotnet.Cli
	console       input.Console

	projectPath string
	options     provisioning.Options
	// generatedBicepDir holds the temp directory where compiled Bicep files are placed.
	generatedBicepDir string
}

func (p *DotNetProvider) Name() string {
	return "DotNet"
}

func (p *DotNetProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	infraOptions, err := options.GetWithDefaults()
	if err != nil {
		return err
	}

	p.projectPath = projectPath
	p.options = infraOptions

	// Resolve the C# project path
	csharpProjectPath := infraOptions.Path
	if !filepath.IsAbs(csharpProjectPath) {
		csharpProjectPath = filepath.Join(projectPath, csharpProjectPath)
	}

	// Verify dotnet CLI is available
	if err := p.dotnetCli.CheckInstalled(ctx); err != nil {
		return fmt.Errorf(
			"dotnet CLI is required for the 'dotnet' infrastructure provider: %w", err)
	}

	// Resolve the C# entry point (.cs file or project directory)
	entryPoint, err := p.resolveEntryPoint(csharpProjectPath)
	if err != nil {
		return err
	}

	// Compile C# to Bicep
	p.console.ShowSpinner(ctx, "Compiling C# infrastructure to Bicep", input.Step)
	generatedDir, err := p.compileCSharpToBicep(ctx, entryPoint, infraOptions.ExtraArgs)
	p.console.StopSpinner(ctx, "", input.Step)
	if err != nil {
		return fmt.Errorf("compiling C# infrastructure to Bicep: %w", err)
	}
	p.generatedBicepDir = generatedDir

	// Rewrite the options to point the Bicep provider at the generated output
	bicepOptions := infraOptions
	bicepOptions.Provider = provisioning.Bicep
	bicepOptions.Path = generatedDir

	err = p.bicepProvider.Initialize(ctx, projectPath, bicepOptions)
	if err != nil {
		p.cleanupGeneratedBicep()
		return fmt.Errorf("initializing bicep provider: %w", err)
	}

	return nil
}

func (p *DotNetProvider) EnsureEnv(ctx context.Context) error {
	return p.bicepProvider.EnsureEnv(ctx)
}

func (p *DotNetProvider) State(ctx context.Context, options *provisioning.StateOptions) (*provisioning.StateResult, error) {
	return p.bicepProvider.State(ctx, options)
}

func (p *DotNetProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	defer p.cleanupGeneratedBicep()
	return p.bicepProvider.Deploy(ctx)
}

func (p *DotNetProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	defer p.cleanupGeneratedBicep()
	return p.bicepProvider.Preview(ctx)
}

func (p *DotNetProvider) Destroy(
	ctx context.Context, options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	defer p.cleanupGeneratedBicep()
	return p.bicepProvider.Destroy(ctx, options)
}

func (p *DotNetProvider) Parameters(ctx context.Context) ([]provisioning.Parameter, error) {
	return p.bicepProvider.Parameters(ctx)
}

// cleanupGeneratedBicep removes the temporary directory containing generated Bicep files.
func (p *DotNetProvider) cleanupGeneratedBicep() {
	if p.generatedBicepDir != "" {
		os.RemoveAll(p.generatedBicepDir)
		p.generatedBicepDir = ""
	}
}

// resolveEntryPoint locates the C# entry point for infrastructure provisioning.
// It supports:
//   - A direct .cs file path (dotnet 10+ file-based app)
//   - A directory containing a single .cs file
//   - A directory containing a .csproj/.fsproj/.vbproj project
//   - A direct .csproj/.fsproj/.vbproj file path
func (p *DotNetProvider) resolveEntryPoint(infraPath string) (string, error) {
	info, err := os.Stat(infraPath)
	if err != nil {
		return "", fmt.Errorf("infrastructure path '%s' does not exist: %w", infraPath, err)
	}

	// Direct file reference
	if !info.IsDir() {
		ext := strings.ToLower(filepath.Ext(infraPath))
		if ext == ".cs" {
			return infraPath, nil
		}
		if slices.Contains(dotnet.DotNetProjectExtensions, ext) {
			return infraPath, nil
		}
		return "", fmt.Errorf(
			"'%s' is not a valid .NET infrastructure file. Expected a .cs file or project file (.csproj, .fsproj, .vbproj)",
			infraPath)
	}

	// Directory — check for .csproj first (takes priority over single .cs file)
	csprojFiles, _ := filepath.Glob(filepath.Join(infraPath, "*.csproj"))
	if len(csprojFiles) > 0 {
		return infraPath, nil
	}

	// No .csproj — look for a single .cs file (dotnet 10+ file-based app)
	csFiles, _ := filepath.Glob(filepath.Join(infraPath, "*.cs"))
	if len(csFiles) == 1 {
		return csFiles[0], nil
	}

	if len(csFiles) > 1 {
		return "", fmt.Errorf(
			"multiple .cs files found in '%s'. "+
				"Use a single .cs file for file-based apps, or create a project file (.csproj) to combine multiple files",
			infraPath)
	}

	return "", fmt.Errorf(
		"no .cs or .NET project file found in '%s'. "+
			"Provide a .cs file (dotnet 10+) or a project with .csproj for the 'dotnet' infrastructure provider",
		infraPath)
}

// compileCSharpToBicep runs the C# entry point and captures generated Bicep files.
// The C# program is expected to accept an output directory path as its first argument
// and write .bicep files to that directory. Any extra args from `azd provision -- <args>`
// are appended after the output directory.
func (p *DotNetProvider) compileCSharpToBicep(ctx context.Context, entryPoint string, extraArgs []string) (string, error) {
	// Create a temp directory for the generated Bicep output
	tempDir, err := os.MkdirTemp("", "azd-dotnet-bicep-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory for Bicep output: %w", err)
	}

	log.Printf("Compiling C# infrastructure from '%s' to Bicep in '%s'", entryPoint, tempDir)

	// Build args: outputDir first, then any extra args forwarded from the CLI.
	// The C# program receives: args[0]=outputDir, args[1..n]=extra args
	runArgs := []string{tempDir}
	runArgs = append(runArgs, extraArgs...)

	// Run the C# entry point, passing the output directory and extra arguments.
	// For .cs files this uses `dotnet run file.cs -- <outputDir> <extraArgs>` (dotnet 10+).
	// For project directories this uses `dotnet run --project <dir> -- <outputDir> <extraArgs>`.
	result, err := p.dotnetCli.Run(ctx, entryPoint, runArgs, nil)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("running C# infrastructure project: %w\nOutput: %s", err, result.Stderr)
	}

	// Verify that at least one .bicep file was generated
	bicepFiles, err := filepath.Glob(filepath.Join(tempDir, "*.bicep"))
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("searching for generated Bicep files: %w", err)
	}

	if len(bicepFiles) == 0 {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf(
			"no .bicep files were generated by the C# infrastructure at '%s'. "+
				"Ensure your program calls infrastructure.Build().Save() with the output directory "+
				"passed as the first command-line argument",
			entryPoint)
	}

	// The Bicep provider expects a specific module file (<infra.module>.bicep, default "main.bicep").
	// Validate early so the user gets a targeted error instead of a later, less actionable one.
	expectedModule := p.options.Module
	if expectedModule == "" {
		expectedModule = "main"
	}
	expectedModuleFile := expectedModule + ".bicep"
	expectedModulePath := filepath.Join(tempDir, expectedModuleFile)
	if _, err := os.Stat(expectedModulePath); err != nil {
		os.RemoveAll(tempDir)
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"expected Bicep module file '%s' was not generated by the C# infrastructure at '%s'. "+
					"Generated files: %s. Ensure your Infrastructure name matches the infra.module setting "+
					"(default 'main')",
				expectedModuleFile, entryPoint, strings.Join(fileNames(bicepFiles), ", "))
		}
		return "", fmt.Errorf("checking for expected Bicep module file: %w", err)
	}

	log.Printf("Generated %d Bicep file(s): %s", len(bicepFiles), strings.Join(fileNames(bicepFiles), ", "))
	return tempDir, nil
}

// fileNames extracts just the file names from full paths.
func fileNames(paths []string) []string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return names
}

// NewDotNetProvider creates a new DotNet infrastructure provider.
func NewDotNetProvider(
	bicepProvider *infraBicep.BicepProvider,
	dotnetCli *dotnet.Cli,
	console input.Console,
) provisioning.Provider {
	return &DotNetProvider{
		bicepProvider: bicepProvider,
		dotnetCli:     dotnetCli,
		console:       console,
	}
}
