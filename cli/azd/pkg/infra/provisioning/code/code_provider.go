// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	infraBicep "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	codeDotnet "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/code/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	toolDotnet "github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// CodeProvider is a language-agnostic router for code-based infrastructure providers.
// It detects the language from the infra path contents and dispatches to the
// appropriate language-specific sub-provider (e.g., dotnet).
type CodeProvider struct {
	// Language-specific sub-provider, set during Initialize
	delegate provisioning.Provider

	// Dependencies needed to construct language sub-providers
	bicepProvider *infraBicep.BicepProvider
	dotnetCli     *toolDotnet.Cli
	console       input.Console
}

func (p *CodeProvider) Name() string {
	return "Code"
}

func (p *CodeProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	infraPath := options.Path
	if !filepath.IsAbs(infraPath) {
		infraPath = filepath.Join(projectPath, infraPath)
	}

	lang, err := detectLanguage(infraPath)
	if err != nil {
		return err
	}

	switch lang {
	case languageDotNet:
		p.delegate = codeDotnet.NewDotNetProvider(p.bicepProvider, p.dotnetCli, p.console)
	default:
		return fmt.Errorf("unsupported language detected in '%s'. "+
			"The 'code' infrastructure provider currently supports C# (.cs/.csproj)", infraPath)
	}

	return p.delegate.Initialize(ctx, projectPath, options)
}

func (p *CodeProvider) EnsureEnv(ctx context.Context) error {
	return p.delegate.EnsureEnv(ctx)
}

func (p *CodeProvider) State(ctx context.Context, options *provisioning.StateOptions) (*provisioning.StateResult, error) {
	return p.delegate.State(ctx, options)
}

func (p *CodeProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	return p.delegate.Deploy(ctx)
}

func (p *CodeProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	return p.delegate.Preview(ctx)
}

func (p *CodeProvider) Destroy(
	ctx context.Context, options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return p.delegate.Destroy(ctx, options)
}

func (p *CodeProvider) Parameters(ctx context.Context) ([]provisioning.Parameter, error) {
	return p.delegate.Parameters(ctx)
}

type language string

const (
	languageDotNet  language = "dotnet"
	languageUnknown language = "unknown"
)

// detectLanguage scans the infra directory to determine which code language is being used.
func detectLanguage(infraPath string) (language, error) {
	info, err := os.Stat(infraPath)
	if err != nil {
		return languageUnknown, fmt.Errorf("infrastructure path '%s' does not exist: %w", infraPath, err)
	}

	// Direct file reference
	if !info.IsDir() {
		ext := strings.ToLower(filepath.Ext(infraPath))
		switch ext {
		case ".cs", ".csproj":
			return languageDotNet, nil
		}
		return languageUnknown, fmt.Errorf(
			"'%s' is not a recognized code infrastructure file", infraPath)
	}

	// Directory — check for language markers
	files, err := os.ReadDir(infraPath)
	if err != nil {
		return languageUnknown, fmt.Errorf("reading infrastructure directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(file.Name()))
		switch ext {
		case ".cs", ".csproj":
			return languageDotNet, nil
		}
	}

	return languageUnknown, fmt.Errorf(
		"no recognized code infrastructure files found in '%s'. "+
			"Supported: C# (.cs, .csproj)",
		infraPath)
}

// NewCodeProvider creates a new code-based infrastructure provider.
func NewCodeProvider(
	bicepProvider *infraBicep.BicepProvider,
	dotnetCli *toolDotnet.Cli,
	console input.Console,
) provisioning.Provider {
	return &CodeProvider{
		bicepProvider: bicepProvider,
		dotnetCli:     dotnetCli,
		console:       console,
	}
}
