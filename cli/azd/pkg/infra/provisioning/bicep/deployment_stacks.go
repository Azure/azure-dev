// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// resolveDeploymentStacksMap resolves the typed deployment-stacks configuration into a
// camelCase map[string]any consumable by the deployment-stacks API layer
// (azapi.parseDeploymentStackOptions). It performs ${VAR} environment-variable substitution
// on denySettings.excludedPrincipals and denySettings.excludedActions, resolving values from
// plan-time layer outputs (VirtualEnv) first and then the azd environment.
//
// It returns nil when no deployment-stacks configuration is present, in which case the caller
// should omit the DeploymentStacks key entirely so the API layer applies its defaults.
func (p *BicepProvider) resolveDeploymentStacksMap() (map[string]any, error) {
	cfg := p.options.DeploymentStacks
	if cfg == nil {
		return nil, nil
	}

	result := map[string]any{}

	if cfg.ActionOnUnmanage != nil {
		actionOnUnmanage := map[string]any{}
		if cfg.ActionOnUnmanage.Resources != "" {
			actionOnUnmanage["resources"] = cfg.ActionOnUnmanage.Resources
		}
		if cfg.ActionOnUnmanage.ResourceGroups != "" {
			actionOnUnmanage["resourceGroups"] = cfg.ActionOnUnmanage.ResourceGroups
		}
		if cfg.ActionOnUnmanage.ManagementGroups != "" {
			actionOnUnmanage["managementGroups"] = cfg.ActionOnUnmanage.ManagementGroups
		}
		result["actionOnUnmanage"] = actionOnUnmanage
	}

	if cfg.DenySettings != nil {
		denySettings := map[string]any{}
		if cfg.DenySettings.Mode != "" {
			denySettings["mode"] = cfg.DenySettings.Mode
		}
		if cfg.DenySettings.ApplyToChildScopes != nil {
			denySettings["applyToChildScopes"] = *cfg.DenySettings.ApplyToChildScopes
		}

		excludedActions, err := p.resolveDeploymentStacksValues(cfg.DenySettings.ExcludedActions)
		if err != nil {
			return nil, err
		}
		if excludedActions != nil {
			denySettings["excludedActions"] = excludedActions
		}

		excludedPrincipals, err := p.resolveDeploymentStacksValues(cfg.DenySettings.ExcludedPrincipals)
		if err != nil {
			return nil, err
		}
		if excludedPrincipals != nil {
			denySettings["excludedPrincipals"] = excludedPrincipals
		}

		result["denySettings"] = denySettings
	}

	return result, nil
}

// resolveDeploymentStacksValues evaluates ${VAR} references in each value, resolving from the
// plan-time layer outputs (VirtualEnv) first and then the azd environment. It returns an error
// if any referenced environment variable is unset, since a blank value in deny settings (for
// example, an empty excluded principal) is almost always a misconfiguration.
func (p *BicepProvider) resolveDeploymentStacksValues(values []osutil.ExpandableString) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	resolved := make([]string, 0, len(values))
	for _, value := range values {
		var unset []string
		substituted, err := value.Envsubst(func(name string) string {
			if p.options.VirtualEnv != nil {
				if v, has := p.options.VirtualEnv[name]; has {
					return v
				}
			}

			v, ok := p.env.LookupEnv(name)
			if !ok {
				unset = append(unset, name)
			}
			return v
		})
		if err != nil {
			return nil, fmt.Errorf("resolving deploymentStacks value: %w", err)
		}

		if len(unset) > 0 {
			return nil, fmt.Errorf(
				"deploymentStacks references unset environment variable(s): %s", strings.Join(unset, ", "))
		}

		resolved = append(resolved, substituted)
	}

	return resolved, nil
}

// deploymentOptionsMap builds the generic options map handed to the deployment API layer,
// with the deployment-stacks configuration resolved (including ${VAR} substitution). Standard
// (non-stack) deployments ignore this map; stack deployments read only the DeploymentStacks key.
func (p *BicepProvider) deploymentOptionsMap() (map[string]any, error) {
	optionsMap, err := convert.ToMap(p.options)
	if err != nil {
		return nil, err
	}

	stacks, err := p.resolveDeploymentStacksMap()
	if err != nil {
		return nil, err
	}

	if stacks == nil {
		// The typed DeploymentStacksConfig is not JSON-serializable in a form the API layer can
		// consume (ExpandableString has no exported fields), so drop whatever convertOptionsToMap
		// produced for it and let the API layer apply its defaults.
		delete(optionsMap, "DeploymentStacks")
	} else {
		optionsMap["DeploymentStacks"] = stacks
	}

	return optionsMap, nil
}
