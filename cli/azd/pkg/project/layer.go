// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

// ValidateLayerGraph validates dependency and output ownership for persisted v2 layer infrastructure.
func ValidateLayerGraph(projectConfig *ProjectConfig) error {
	if projectConfig == nil {
		return errors.New("project config is nil")
	}
	if projectConfig.Format() != ProjectFormatLayersV2 {
		return errors.New("layer graph validation requires a top-level layers project")
	}

	layerNames := make(map[string]struct{}, len(projectConfig.Layers))
	infraEntries := make([]provisioning.Options, 0)
	for _, configLayer := range projectConfig.Layers {
		if configLayer == nil {
			return errors.New("project layer cannot be nil")
		}
		if _, has := layerNames[configLayer.Name]; has {
			return fmt.Errorf("duplicate project layer %q", configLayer.Name)
		}
		layerNames[configLayer.Name] = struct{}{}
		for _, infra := range configLayer.Infra {
			infra.Layer = configLayer.Name
			infraEntries = append(infraEntries, infra)
		}
	}

	return validateLayerGraph(layerNames, infraEntries)
}

func validateLayerGraph(
	layerNames map[string]struct{},
	infraEntries []provisioning.Options,
) error {
	infraByName := make(map[string]provisioning.Options, len(infraEntries))
	infraOwnerByName := make(map[string]string, len(infraEntries))
	infraDependencySets := make(map[string]map[string]struct{}, len(infraEntries))
	for _, infra := range infraEntries {
		if infra.Name == "" {
			return errors.New("infrastructure entry name cannot be empty")
		}
		if _, has := infraByName[infra.Name]; has {
			return fmt.Errorf("duplicate infrastructure entry %q", infra.Name)
		}
		infraByName[infra.Name] = infra
		infraOwnerByName[infra.Name] = infra.Layer
		infraDependencySets[infra.Name] = map[string]struct{}{}
	}

	dependencySets := make(map[string]map[string]struct{}, len(layerNames))
	for name := range layerNames {
		dependencySets[name] = map[string]struct{}{}
	}

	for _, infra := range infraEntries {
		for _, dependencyName := range infra.DependsOn {
			if dependencyName == infra.Name {
				return fmt.Errorf("infrastructure layer %q cannot depend on itself", infra.Name)
			}
			if _, has := infraByName[dependencyName]; !has {
				return fmt.Errorf(
					"infrastructure layer %q depends on unknown infrastructure layer %q",
					infra.Name, dependencyName,
				)
			}
			infraDependencySets[infra.Name][dependencyName] = struct{}{}

			owner := infra.Layer
			dependencyOwner := infraOwnerByName[dependencyName]
			if dependencyOwner != owner {
				dependencySets[owner][dependencyOwner] = struct{}{}
			}
		}
	}
	if err := validateDependencyGraph(infraDependencySets, "infrastructure layer"); err != nil {
		return err
	}
	if err := validateDependencyGraph(dependencySets, "layer"); err != nil {
		return err
	}

	outputOwners := map[string]string{}
	for _, infra := range infraEntries {
		for providerOutput, environmentKey := range infra.Outputs {
			if environmentKey == "" {
				return fmt.Errorf(
					"infrastructure layer %q output %q maps to an empty environment key",
					infra.Name, providerOutput,
				)
			}
			if owner, has := outputOwners[environmentKey]; has && owner != infra.Name {
				return fmt.Errorf(
					"environment output %q is owned by both infrastructure layers %q and %q",
					environmentKey, owner, infra.Name,
				)
			}
			outputOwners[environmentKey] = infra.Name
		}
	}

	return nil
}

func validateDependencyGraph(dependencies map[string]map[string]struct{}, subject string) error {
	const (
		unvisited = iota
		visiting
		visited
	)

	states := make(map[string]int, len(dependencies))
	var visit func(string) error
	visit = func(name string) error {
		switch states[name] {
		case visiting:
			return fmt.Errorf("circular dependency detected at %s %q", subject, name)
		case visited:
			return nil
		}

		states[name] = visiting
		for _, dependency := range slices.Sorted(maps.Keys(dependencies[name])) {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		states[name] = visited
		return nil
	}

	for _, name := range slices.Sorted(maps.Keys(dependencies)) {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
