// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// applyLayerInputMappings creates an infra-entry-scoped environment for the entry's
// provisioning provider. It contains input aliases resolved from shared azd environment
// keys. Mappings sourced from planned values remain virtual so the provider does not
// mistake them for deployable values.
func applyLayerInputMappings(options Options, env *environment.Environment) (Options, *environment.Environment) {
	if len(options.Inputs) == 0 {
		return options, env
	}

	providerEnv := environment.NewWithValues(env.Name(), env.Dotenv())

	providerEnv.Config = config.Clone(env.Config)
	options.VirtualEnv = maps.Clone(options.VirtualEnv)

	for providerInput, environmentKey := range options.Inputs {
		if value, has := options.VirtualEnv[environmentKey]; has {
			options.VirtualEnv[providerInput] = value
			continue
		}

		if value, has := env.LookupEnv(environmentKey); has {
			providerEnv.DotenvSet(providerInput, value)
		}
	}

	return options, providerEnv
}

// layerEnvironmentManager prevents infra-entry-scoped input aliases from being
// persisted while preserving other environment changes made by the entry's provider.
type layerEnvironmentManager struct {
	// Manager persists the shared azd environment after infra-entry-scoped changes
	// have been synchronized into baseEnv.
	environment.Manager
	// baseEnv is the shared azd environment represented by the persisted .env file.
	baseEnv *environment.Environment
	// initialValues is a snapshot of the infra entry's environment created from
	// baseEnv before provider execution. It includes input aliases derived from
	// existing .env values and is used to identify the provider's delta.
	initialValues map[string]string
	// initialConfig is the infra entry's config baseline used to reconcile only
	// provider changes back into the owning environment.
	initialConfig config.Config
	// inputs maps aliases used by this infra entry's provider to shared azd environment
	// keys. Alias keys are excluded when synchronizing provider changes back into baseEnv.
	inputs map[string]string
}

func (m *layerEnvironmentManager) Save(ctx context.Context, providerEnv *environment.Environment) error {
	m.sync(providerEnv)
	return m.Manager.Save(ctx, m.baseEnv)
}

func (m *layerEnvironmentManager) SaveWithOptions(
	ctx context.Context,
	providerEnv *environment.Environment,
	options *environment.SaveOptions,
) error {
	m.sync(providerEnv)
	return m.Manager.SaveWithOptions(ctx, m.baseEnv, options)
}

func (m *layerEnvironmentManager) sync(providerEnv *environment.Environment) {
	providerValues := providerEnv.Dotenv()

	// Apply changes only when the provider modified or removed a value from its
	// initial snapshot. Copying the full snapshot would overwrite values changed
	// concurrently in the shared environment by another infra entry or service.
	for key, initialValue := range m.initialValues {
		// Input aliases exist only for this infra entry and must never leak into
		// the shared environment, even if the provider modifies or removes them.
		if _, isProviderInput := m.inputs[key]; isProviderInput {
			continue
		}
		value, has := providerValues[key]
		if !has {
			m.baseEnv.DotenvDelete(key)
		} else if value != initialValue {
			m.baseEnv.DotenvSet(key, value)
		}
	}

	// Values absent from the initial snapshot were created by the provider and
	// need to be copied back. Existing keys were handled above; skipping them
	// here avoids replacing a concurrent shared-environment update with an
	// unchanged value from the provider's stale snapshot.
	for key, value := range providerValues {
		_, existedInitially := m.initialValues[key]
		_, isProviderInput := m.inputs[key]
		if !existedInitially && !isProviderInput {
			m.baseEnv.DotenvSet(key, value)
		}
	}

	initialConfig := m.initialConfig
	if initialConfig == nil {
		initialConfig = config.Clone(m.baseEnv.Config)
	}
	config.ApplyDelta(m.baseEnv.Config, initialConfig, providerEnv.Config)
}

// validateLayerOutputMappings makes sure the user's mapped outputs actually match
// a variable in the planned outputs. Helps them avoid typos.
func validateLayerOutputMappings(
	outputs map[string]OutputParameter,
	outputMappings map[string]string,
) error {
	if len(outputMappings) == 0 {
		return nil
	}

	var missing []string
	for _, providerOutput := range slices.Sorted(maps.Keys(outputMappings)) {
		if _, has := outputs[providerOutput]; !has {
			missing = append(missing, providerOutput)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	available := slices.Sorted(maps.Keys(outputs))
	return fmt.Errorf(
		"output mappings reference unknown provider outputs: %s; available outputs: %s",
		strings.Join(missing, ", "),
		strings.Join(available, ", "),
	)
}

func plannedOutputParameters(outputs []PlannedOutput) map[string]OutputParameter {
	parameters := make(map[string]OutputParameter, len(outputs))
	for _, output := range outputs {
		parameters[output.Name] = OutputParameter{}
	}
	return parameters
}

func applyPlannedOutputMappings(outputs []PlannedOutput, mappings map[string]string) ([]PlannedOutput, error) {
	mapped := make([]PlannedOutput, len(outputs))
	owners := make(map[string]string, len(outputs))
	for i, output := range outputs {
		name := output.Name
		if configured, has := mappings[name]; has {
			name = configured
		}
		if owner, has := owners[name]; has && owner != output.Name {
			return nil, fmt.Errorf(
				"provider outputs %q and %q both map to environment key %q",
				owner, output.Name, name,
			)
		}
		owners[name] = output.Name
		mapped[i] = PlannedOutput{Name: name}
	}
	return mapped, nil
}

// applyLayerOutputMappings maps provider output names to shared azd environment
// keys. It rejects runtime collisions when two provider outputs resolve to the
// same shared key.
func applyLayerOutputMappings(
	outputs map[string]OutputParameter,
	mappings map[string]string,
) (map[string]OutputParameter, error) {
	if len(outputs) == 0 {
		return outputs, nil
	}

	mapped := make(map[string]OutputParameter, len(outputs))
	owners := make(map[string]string, len(outputs))
	for providerOutput, parameter := range outputs {
		environmentKey := providerOutput
		if configured, has := mappings[providerOutput]; has {
			if configured == "" {
				return nil, fmt.Errorf("provider output %q maps to an empty environment key", providerOutput)
			}
			environmentKey = configured
		}
		// Track the original output name because assigning directly to mapped
		// would otherwise silently replace an output mapped to the same key.
		if owner, has := owners[environmentKey]; has && owner != providerOutput {
			return nil, fmt.Errorf(
				"provider outputs %q and %q both map to environment key %q",
				owner, providerOutput, environmentKey,
			)
		}
		owners[environmentKey] = providerOutput
		mapped[environmentKey] = parameter
	}
	return mapped, nil
}

// applyLayerOutputKeyMappings maps provider invalidation keys to the shared azd
// environment keys used for persisted outputs.
func applyLayerOutputKeyMappings(keys []string, mappings map[string]string) []string {
	if len(keys) == 0 {
		return keys
	}
	mapped := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if configured, has := mappings[key]; has && configured != "" {
			key = configured
		}
		mapped[key] = struct{}{}
	}
	return slices.Sorted(maps.Keys(mapped))
}
