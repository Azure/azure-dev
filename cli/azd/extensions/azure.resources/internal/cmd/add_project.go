// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"azure.resources/internal/add"
	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/types/known/structpb"
)

const defaultResourcesLayer = "azureresources"

type addProject struct {
	layers   []any
	index    int
	target   map[string]any
	provider string
	config   resources.Config
	services map[string]map[string]any
}

func loadProject(ctx context.Context, client azdext.ProjectServiceClient) (map[string]any, error) {
	project := map[string]any{}
	for _, path := range []string{"resources", "layers", "services", "infra"} {
		response, err := client.GetConfigValue(ctx, &azdext.GetProjectConfigValueRequest{Path: path})
		if err != nil {
			return nil, fmt.Errorf("reading project %s: %w", path, err)
		}
		if response.GetFound() {
			project[path] = response.GetValue().AsInterface()
		}
	}
	return project, nil
}

func parseProject(project map[string]any, layerName string) (*addProject, error) {
	if layerName == "" {
		return nil, errors.New("infrastructure layer name must not be empty")
	}
	if err := resources.ValidateEntryName(layerName); err != nil {
		return nil, fmt.Errorf("invalid infrastructure layer name: %w", err)
	}
	if legacy, ok := project["resources"].(map[string]any); ok && len(legacy) > 0 {
		return nil, errors.New("azd resources add writes to infra.layers[].config, not top-level resources; " +
			"migrate legacy resources before using this command")
	}
	if _, exists := project["layers"]; exists {
		return nil, errors.New("azd resources add currently supports infra.layers, not top-level project layers")
	}
	if err := resources.ValidateProjectLayers(project, ""); err != nil {
		return nil, err
	}
	p := &addProject{index: -1, config: resources.Config{}, services: map[string]map[string]any{}}
	if services, exists := project["services"]; exists {
		serviceMap, ok := services.(map[string]any)
		if !ok {
			return nil, errors.New("services must be a mapping")
		}
		for name, value := range serviceMap {
			service, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("service %q must be a mapping", name)
			}
			p.services[name] = service
		}
	}
	if value, exists := project["infra"]; exists {
		infra, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("infra must be a mapping")
		}
		if layers, exists := infra["layers"]; exists {
			p.layers, ok = layers.([]any)
			if !ok {
				return nil, errors.New("infra.layers must be a sequence")
			}
		} else if len(infra) > 0 {
			return nil, errors.New("move existing flat infrastructure into infra.layers before using azd resources add")
		}
	}
	names := map[string]bool{}
	for i, value := range p.layers {
		layer, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("infra.layers[%d] must be a mapping", i)
		}
		name, ok := layer["name"].(string)
		if !ok || name == "" || names[name] {
			return nil, fmt.Errorf("infra.layers[%d] requires a unique, nonempty name", i)
		}
		names[name] = true
		if name != layerName {
			continue
		}
		p.index, p.target = i, layer
		provider, exists := layer["provider"]
		if !exists {
			infra, ok := project["infra"].(map[string]any)
			if !ok {
				return nil, errors.New("infra must be a mapping")
			}
			provider = infra["provider"]
		}
		p.provider, ok = provider.(string)
		if provider != nil && !ok {
			return nil, fmt.Errorf("layer %q provider must be a string", layerName)
		}
		if p.provider != resources.ProviderName {
			continue
		}
		config, exists := layer["config"]
		if !exists {
			continue
		}
		raw, ok := config.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("layer %q config must be a mapping", layerName)
		}
		if len(raw) > 0 {
			config, err := resources.Decode(raw)
			if err != nil {
				return nil, fmt.Errorf("layer %q: %w", layerName, err)
			}
			p.config = config
		}
	}
	return p, nil
}

func (p *addProject) layerChange(
	layerName string, result *add.Result,
) (*azdext.SetProjectConfigValueRequest, error) {
	raw, err := result.Resources.Raw()
	if err != nil {
		return nil, err
	}
	if err := result.Resources.Validate(); err != nil {
		return nil, err
	}
	layers := slices.Clone(p.layers)
	layer := map[string]any{"name": layerName, "provider": resources.ProviderName, "config": raw}
	if p.target != nil && p.provider == resources.ProviderName {
		layer = maps.Clone(p.target)
		config := map[string]any{}
		if existing, ok := layer["config"].(map[string]any); ok {
			config = maps.Clone(existing)
		}
		maps.Copy(config, raw)
		layer["config"] = config
	}
	if p.index < 0 {
		layers = append(layers, layer)
	} else {
		layers[p.index] = layer
	}
	value, err := structpb.NewValue(layers)
	if err != nil {
		return nil, err
	}
	return &azdext.SetProjectConfigValueRequest{Path: "infra.layers", Value: value}, nil
}

// serviceChanges prepares every RPC payload before any project mutation.
func (p *addProject) serviceChanges(result *add.Result) ([]*azdext.SetProjectConfigSectionRequest, error) {
	var requests []*azdext.SetProjectConfigSectionRequest
	for _, name := range slices.Sorted(maps.Keys(result.Services)) {
		if previous, exists := p.services[name]; exists && reflect.DeepEqual(previous, result.Services[name]) {
			continue
		}
		// Project RPC paths are dot-separated, without literal-key escaping.
		if name == "" || strings.Contains(name, ".") {
			return nil, fmt.Errorf("service name %q cannot be addressed by the project configuration RPC", name)
		}
		section, err := structpb.NewStruct(result.Services[name])
		if err != nil {
			return nil, fmt.Errorf("encoding service %q: %w", name, err)
		}
		requests = append(requests, &azdext.SetProjectConfigSectionRequest{Path: "services." + name, Section: section})
	}
	return requests, nil
}

// saveAddServices writes each service after the layer has been saved. The project RPCs
// can't write several paths at once, so if a service write fails, infra and services are
// put back to their original values rather than leaving azure.yaml half-updated.
func saveAddServices(
	ctx context.Context,
	client azdext.ProjectServiceClient,
	original map[string]any,
	requests []*azdext.SetProjectConfigSectionRequest,
) error {
	for _, request := range requests {
		// Use the existing raw-section RPC rather than implementing service YAML writes
		// or translating provider-authored service fields into the typed AddService model.
		if _, err := client.SetConfigSection(ctx, request); err != nil {
			err = fmt.Errorf("saving %s: %w", request.Path, err)
			if restoreErr := restoreAddProject(ctx, client, original); restoreErr != nil {
				return fmt.Errorf("%w; restoring azure.yaml also failed, so infra and services need manual cleanup: %w",
					err, restoreErr)
			}
			return fmt.Errorf("%w; azure.yaml was left unchanged", err)
		}
	}
	return nil
}

// restoreAddProject puts infra and services back to the values read before add started.
func restoreAddProject(ctx context.Context, client azdext.ProjectServiceClient, original map[string]any) error {
	for _, path := range []string{"services", "infra"} {
		value, existed := original[path]
		if !existed {
			if _, err := client.UnsetConfig(ctx, &azdext.UnsetProjectConfigRequest{Path: path}); err != nil {
				return err
			}
			continue
		}

		protoValue, err := structpb.NewValue(value)
		if err != nil {
			return err
		}
		if _, err := client.SetConfigValue(ctx,
			&azdext.SetProjectConfigValueRequest{Path: path, Value: protoValue}); err != nil {
			return err
		}
	}
	return nil
}

func (p *addProject) summary(layerName string, result *add.Result) string {
	action := "Updated"
	if p.target == nil {
		action = "Added"
	} else if p.provider != resources.ProviderName {
		action = "Replaced"
	}
	lines := []string{"\nazure.yaml updated:", fmt.Sprintf("  %s resources layer %q.", action, layerName)}
	for _, name := range slices.Sorted(maps.Keys(result.Resources)) {
		previous, exists := p.config[name]
		if exists && reflect.DeepEqual(previous, result.Resources[name]) {
			continue
		}
		action := "Added"
		if exists {
			action = "Updated"
		}
		lines = append(lines, fmt.Sprintf("  %s resource %q (%s).", action, name, result.Resources[name].Type))
	}
	for _, name := range slices.Sorted(maps.Keys(result.Services)) {
		previous, exists := p.services[name]
		if exists && reflect.DeepEqual(previous, result.Services[name]) {
			continue
		}
		action := "Added"
		if exists {
			action = "Updated"
		}
		lines = append(lines, fmt.Sprintf("  %s service %q.", action, name))
	}
	return strings.Join(lines, "\n")
}
