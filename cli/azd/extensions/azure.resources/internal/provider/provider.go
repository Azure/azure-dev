// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package provider combines resource generation with the extension-owned Bicep executor.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"azure.resources/internal/bicep"
	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/proto"
)

type executionProvider interface {
	azdext.ProvisioningProvider
	Close() error
}

// Provider owns generated infrastructure for the extension's provisioning session.
type Provider struct {
	executionProvider
	generator *resources.Generator
	resolver  *resources.ParameterResolver
	client    *azdext.AzdClient
	tempDir   string
}

// New creates a resources provider with its own generator and Bicep executor.
func New(client *azdext.AzdClient) *Provider {
	resolver := resources.NewParameterResolver(client)
	return &Provider{
		executionProvider: bicep.New(client, resolver), generator: &resources.Generator{},
		resolver: resolver, client: client,
	}
}

// Initialize materializes this entry's sources and initializes its private Bicep executor.
func (p *Provider) Initialize(
	ctx context.Context, projectPath string, options *azdext.ProvisioningOptions,
) error {
	if err := p.Close(); err != nil {
		return fmt.Errorf("closing previous resources infrastructure: %w", err)
	}
	if options == nil {
		return errors.New("resources initialization requires provisioning options")
	}

	if p.resolver != nil {
		if err := p.resolver.Configure(options); err != nil {
			return err
		}
		if p.client != nil {
			project := map[string]any{}
			for _, path := range []string{"infra", "layers"} {
				response, err := p.client.Project().GetConfigValue(ctx, &azdext.GetProjectConfigValueRequest{Path: path})
				if err != nil {
					return fmt.Errorf("reading project %s: %w", path, err)
				}
				if response.GetFound() {
					project[path] = response.GetValue().AsInterface()
				}
			}
			if err := resources.ValidateProjectLayers(project, ""); err != nil {
				return err
			}
		}
	}
	if err := p.materializeInfrastructure(ctx, options); err != nil {
		return err
	}
	if err := p.initializeExecutor(ctx, projectPath, options); err != nil {
		return errors.Join(err, p.Close())
	}
	return nil
}

// materializeInfrastructure generates Bicep and parameter files in a temporary directory owned
// by this provider. On failure, it closes the executor and removes any files it created.
func (p *Provider) materializeInfrastructure(ctx context.Context, options *azdext.ProvisioningOptions) error {
	files, err := p.generator.Generate(ctx, resources.GenerateRequest{Name: options.Name, Config: options.Config.AsMap()})
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "azd-resources-")
	if err != nil {
		return fmt.Errorf("creating resources infrastructure directory: %w", err)
	}
	p.tempDir = tempDir
	for name, contents := range files {
		if !fs.ValidPath(name) || name == "." || !filepath.IsLocal(name) || strings.Contains(name, "\\") {
			return errors.Join(fmt.Errorf("invalid generated file name %q", name), p.Close())
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, p.Close())
		}
		target := filepath.Join(tempDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return errors.Join(fmt.Errorf("creating generated directory: %w", err), p.Close())
		}
		if err := os.WriteFile(target, contents, 0o600); err != nil {
			return errors.Join(fmt.Errorf("writing generated infrastructure: %w", err), p.Close())
		}
	}
	return nil
}

// initializeExecutor prepares the Bicep executor to use the generated infrastructure files
// and resolves the Azure deployment context. Missing subscription and location values are
// prompted for and saved in the active azd environment so later infrastructure entries can reuse them.
// If setup fails, Initialize closes the executor and removes the generated files.
func (p *Provider) initializeExecutor(
	ctx context.Context, projectPath string, options *azdext.ProvisioningOptions,
) error {
	generatedOptions := proto.Clone(options).(*azdext.ProvisioningOptions)
	generatedOptions.Path = p.tempDir
	generatedOptions.Module = "main"

	if err := p.executionProvider.Initialize(ctx, projectPath, generatedOptions); err != nil {
		return err
	}
	// Match built-in providers: serial initialization must save scope before parallel layers start.
	return p.executionProvider.EnsureEnv(ctx)
}

// Close releases the executor before removing the source files it may still be using.
func (p *Provider) Close() error {
	if err := p.executionProvider.Close(); err != nil {
		return err
	}
	if p.tempDir == "" {
		return nil
	}
	if err := os.RemoveAll(p.tempDir); err != nil {
		return fmt.Errorf("removing generated resources infrastructure: %w", err)
	}
	p.tempDir = ""
	return nil
}
