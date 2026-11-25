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
	layers         []any
	index          int
	target         map[string]any
	provider       string
	resourcesLayer string
	config         resources.Config
	services       map[string]map[string]any
}

func loadProject(ctx context.Context, client azdext.ProjectServiceClient) (map[string]any, error) {
	project := map[string]any{}
	for _, path := range []string{
		resources.FieldResources, resources.FieldLayers, resources.FieldServices, resources.FieldInfra,
	} {
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

// parseProject validates that the project is compatible with 'resources' and parses
// it.
func parseProject(project map[string]any, layerName string) (*addProject, error) {
	if err := validateProjectForResources(project, layerName); err != nil {
		return nil, err
	}

	services, err := parseServices(project)
	if err != nil {
		return nil, err
	}

	layers, err := parseInfra(project)
	if err != nil {
		return nil, err
	}

	addProjectState := &addProject{
		index: -1, config: resources.Config{}, services: services, layers: layers,
	}
	if err := addProjectState.readLayers(layerName); err != nil {
		return nil, err
	}

	return addProjectState, nil
}

func validateProjectForResources(project map[string]any, layerName string) error {
	if layerName == "" {
		return errors.New("infrastructure layer name must not be empty")
	}
	if err := resources.ValidateEntryName(layerName); err != nil {
		return fmt.Errorf("invalid infrastructure layer name: %w", err)
	}

	// the old top level .Resources isn't compatible with our approach
	if legacy, ok := project[resources.FieldResources].(map[string]any); ok && len(legacy) > 0 {
		return errors.New("azd resources add writes to infra.layers[].config, not top-level resources; " +
			"migrate legacy resources before using this command")
	}

	if _, exists := project[resources.FieldLayers]; exists {
		return errors.New("azd resources add currently supports infra.layers, not top-level project layers")
	}

	return resources.ValidateProjectLayers(project, "")
}

func parseServices(project map[string]any) (map[string]map[string]any, error) {
	services := map[string]map[string]any{}
	raw, exists := project[resources.FieldServices]
	if !exists {
		return services, nil
	}

	serviceMap, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("services must be a mapping")
	}
	for name, value := range serviceMap {
		service, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("service %q must be a mapping", name)
		}
		services[name] = service
	}

	return services, nil
}

func parseInfra(project map[string]any) ([]any, error) {
	raw, exists := project[resources.FieldInfra]
	if !exists {
		return nil, nil
	}

	infra, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("infra must be a mapping")
	}
	rawLayers, exists := infra[resources.FieldLayers]
	if !exists {
		if len(infra) > 0 {
			return nil, errors.New("move existing flat infrastructure into infra.layers before using azd resources add")
		}
		return nil, nil
	}

	layers, ok := rawLayers.([]any)
	if !ok {
		return nil, errors.New("infra.layers must be a sequence")
	}
	return layers, nil
}

func (p *addProject) readLayers(layerName string) error {
	for i, value := range p.layers {
		layer, err := resources.UnmarshalProjectLayer(value)
		if err != nil {
			return fmt.Errorf("infra.layers[%d] %w", i, err)
		}
		if layer.Provider == resources.ProviderName {
			p.resourcesLayer = layer.Name
		}

		if layer.Name == layerName {
			p.index, p.target, p.provider = i, layer.Raw, layer.Provider
		}
	}

	if p.provider == resources.ProviderName {
		config, err := unmarshalResourceConfig(p.target, layerName)
		if err != nil {
			return err
		}
		p.config = config
	}
	return nil
}

func unmarshalResourceConfig(layer map[string]any, layerName string) (resources.Config, error) {
	value, exists := layer[resources.FieldConfig]
	if !exists {
		return resources.Config{}, nil
	}

	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("layer %q config must be a mapping", layerName)
	}
	if len(raw) == 0 {
		return resources.Config{}, nil
	}

	config, err := resources.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("layer %q: %w", layerName, err)
	}
	return config, nil
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

	layer := map[string]any{
		resources.FieldName:     layerName,
		resources.FieldProvider: resources.ProviderName,
		resources.FieldConfig:   raw,
	}

	if p.target != nil && p.provider == resources.ProviderName {
		layer = maps.Clone(p.target)
		config := map[string]any{}
		if existing, ok := layer[resources.FieldConfig].(map[string]any); ok {
			config = maps.Clone(existing)
		}
		maps.Copy(config, raw)
		layer[resources.FieldConfig] = config
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
	return &azdext.SetProjectConfigValueRequest{
		Path: resources.FieldInfra + "." + resources.FieldLayers, Value: value,
	}, nil
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
		requests = append(requests, &azdext.SetProjectConfigSectionRequest{
			Path: resources.FieldServices + "." + name, Section: section,
		})
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
	for _, path := range []string{resources.FieldServices, resources.FieldInfra} {
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
