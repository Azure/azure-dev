// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type emulatorProvider struct {
	definitions        []emulator.Definition
	resolve            func(string, string) (launcher, error)
	currentEnvironment func(context.Context) (string, error)
	projectDir         string
	entries            []emulatorEntry
}

type emulatorEntry struct {
	definition emulator.Definition
	request    emulator.Request
}

func validContentDirectory(path string) bool {
	return filepath.IsLocal(path) && !slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "..")
}

func (p *emulatorProvider) Initialize(_ context.Context, projectDir string, options *azdext.ProvisioningOptions) error {
	projectDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return fmt.Errorf("resolving emulator project directory: %w", err)
	}
	p.projectDir = projectDir
	config := options.GetConfig().AsMap()
	if len(config) == 0 {
		return errors.New("declare at least one emulator in the emulators layer config")
	}
	entries := make([]emulatorEntry, 0, len(config))
	for _, id := range slices.Sorted(maps.Keys(config)) {
		index := slices.IndexFunc(p.definitions, func(definition emulator.Definition) bool { return definition.Name == id })
		if index < 0 {
			return fmt.Errorf("unknown emulator %q in layer config", id)
		}
		definition := p.definitions[index]
		settings, ok := config[id].(map[string]any)
		if !ok {
			return fmt.Errorf("emulator %q settings must be a mapping", id)
		}
		request := emulator.Request{
			Timeout:       defaultTimeout,
			AcceptLicense: len(definition.LicenseURLs) > 0,
			Options:       emulator.Options{Ports: make(map[string]int, len(definition.Ports))},
		}
		for _, port := range definition.Ports {
			request.Options.Ports[port.Flag] = port.Default
		}
		if definition.HasUpstream {
			request.Options.UpstreamURL = defaultUpstreamURL
		}
		if definition.HasDirectory {
			directory, ok := settings["directory"].(string)
			if !ok || !validContentDirectory(directory) {
				return fmt.Errorf("emulator %q requires a project-relative directory", id)
			}
			request.Options.Directory = filepath.Join(p.projectDir, directory)
			request.Changed = map[string]bool{"directory": true}
		}
		for key := range settings {
			switch {
			case key == "directory" && definition.HasDirectory:
			case key == "acceptLicense" && len(definition.LicenseURLs) > 0:
				accepted, ok := settings[key].(bool)
				if !ok {
					return fmt.Errorf("emulator %q acceptLicense must be a boolean", id)
				}
				request.AcceptLicense = accepted
			default:
				return fmt.Errorf("unknown setting %q for emulator %q", key, id)
			}
		}
		entries = append(entries, emulatorEntry{definition: definition, request: request})
	}
	p.entries = entries
	return nil
}

func (p *emulatorProvider) Deploy(
	ctx context.Context, progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDeployResult, error) {
	entries := slices.Clone(p.entries)
	for i := range entries {
		definition := entries[i].definition
		if len(definition.LicenseURLs) > 0 && !entries[i].request.AcceptLicense {
			return nil, &azdext.LocalError{
				Category: azdext.LocalErrorCategoryValidation, Code: "license_not_accepted",
				Message: definition.DisplayName + " requires license acceptance before it can start.",
				Suggestion: fmt.Sprintf("Review %s. If you accept, set acceptLicense: true under %s in azure.yaml.",
					strings.Join(definition.LicenseURLs, ", "), definition.Name),
			}
		}
		if !entries[i].definition.HasDirectory {
			continue
		}
		path, err := filepath.EvalSymlinks(entries[i].request.Options.Directory)
		if err != nil {
			return nil, fmt.Errorf("resolving static-content directory: %w", err)
		}
		rel, err := filepath.Rel(p.projectDir, path)
		if err != nil || !validContentDirectory(rel) {
			return nil, errors.New("static-content directory must be inside the project")
		}
		entries[i].request.Options.Directory = path
	}
	service, err := p.resolveLauncher(ctx)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := service.CheckRequirements(ctx, entry.definition); err != nil {
			return nil, err
		}
	}
	outputs := make(map[string]*azdext.ProvisioningOutputParameter)
	for _, entry := range entries {
		if progress != nil {
			progress("Starting " + entry.definition.DisplayName + " locally")
		}
		result, err := service.Run(ctx, entry.definition, entry.request)
		if err != nil {
			return nil, fmt.Errorf("provisioning local emulator %s: %w", entry.definition.Name, err)
		}
		for _, connection := range result.Connections {
			key := emulatorOutputKey(entry.definition.Name, connection.Name)
			outputs[key] = &azdext.ProvisioningOutputParameter{Type: "string", Value: connection.Value}
		}
	}
	return &azdext.ProvisioningDeployResult{Deployment: &azdext.ProvisioningDeployment{Outputs: outputs}}, nil
}

func emulatorOutputKey(emulatorName, connectionName string) string {
	emulatorName = strings.ReplaceAll(emulatorName, "-", "_")
	return "AZD_EMULATOR_" + strings.ToUpper(emulatorName+"_"+connectionName)
}

func (p *emulatorProvider) Preview(
	_ context.Context, _ grpcbroker.ProgressFunc,
) (*azdext.ProvisioningPreviewResult, error) {
	var names []string
	for _, entry := range p.entries {
		names = append(names, entry.definition.DisplayName)
	}
	return &azdext.ProvisioningPreviewResult{Preview: &azdext.ProvisioningDeploymentPreview{
		Summary: "Start local emulators: " + strings.Join(names, ", "),
	}}, nil
}

func (p *emulatorProvider) Destroy(
	ctx context.Context, _ *azdext.ProvisioningDestroyOptions, _ grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDestroyResult, error) {
	service, err := p.resolveLauncher(ctx)
	if err != nil {
		return nil, err
	}
	var invalidated []string
	for _, entry := range p.entries {
		result, err := service.Run(ctx, entry.definition, emulator.Request{Stop: true, Timeout: defaultTimeout})
		if emulator.IsInstanceMissing(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stopping local emulator %s: %w", entry.definition.Name, err)
		}
		for _, name := range result.RemovedConnectionNames {
			invalidated = append(invalidated, emulatorOutputKey(entry.definition.Name, name))
		}
	}
	return &azdext.ProvisioningDestroyResult{InvalidatedEnvKeys: invalidated}, nil
}

func (p *emulatorProvider) resolveLauncher(ctx context.Context) (launcher, error) {
	environment, err := p.currentEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	if environment == "" {
		return nil, errors.New("no active azd environment for the emulators layer")
	}
	return p.resolve(p.projectDir, environment)
}

func (*emulatorProvider) State(
	context.Context, *azdext.ProvisioningStateOptions,
) (*azdext.ProvisioningStateResult, error) {
	return &azdext.ProvisioningStateResult{State: &azdext.ProvisioningState{}}, nil
}

func (*emulatorProvider) EnsureEnv(context.Context) error { return nil }

func (*emulatorProvider) Parameters(context.Context) ([]*azdext.ProvisioningParameter, error) {
	return nil, nil
}

func (*emulatorProvider) PlannedOutputs(context.Context) ([]*azdext.ProvisioningPlannedOutput, error) {
	return nil, nil
}
