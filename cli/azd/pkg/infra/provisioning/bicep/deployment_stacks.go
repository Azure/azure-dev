// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/drone/envsubst/parse"
)

// deploymentStacksEnabled reports whether the Deployment Stacks alpha feature is active. This
// mirrors the deployment-service selection in cmd/container.go: only when the feature is enabled
// is the stacks deployment service used (and the deployment-stacks options actually consumed).
func (p *BicepProvider) deploymentStacksEnabled() bool {
	var featureManager *alpha.FeatureManager
	if err := p.serviceLocator.Resolve(&featureManager); err != nil {
		return false
	}

	return featureManager.IsEnabled(azapi.FeatureDeploymentStacks)
}

// deploymentProjectName returns the project identity used for ARM deployment tags and lookup.
// Deployment stacks retain their existing naming and matching behavior because changing a stack
// identity can conflict with resources still managed by the previous stack.
func (p *BicepProvider) deploymentProjectName() string {
	if p.deploymentStacksEnabled() {
		return ""
	}

	return p.projectName
}

// hasActiveDeploymentStacksConfig reports whether an effective deployment-stacks configuration is
// present (config supplied AND the alpha feature enabled). When true, the deployment-state shortcut
// must be bypassed: the stacks deny/unmanage settings — including ${VAR}-resolved deny lists — can
// change independently of the ARM template and parameters that the state hash covers. Skipping the
// deployment would otherwise leave stale deny settings on the stack and would also bypass the
// ${VAR} resolution/validation performed while building the options map.
func (p *BicepProvider) hasActiveDeploymentStacksConfig() bool {
	return p.options.DeploymentStacks != nil && p.deploymentStacksEnabled()
}

// useDeploymentStateShortcut reports whether the deployment-state shortcut (which skips a
// redeploy when the template and parameters are unchanged) may be used. It returns false when
// deployment-state tracking is disabled (--no-state), when the parameters hash could not be
// computed, or when an active deployment-stacks configuration is present. In the stacks case the
// deny/unmanage settings — including ${VAR}-resolved deny lists — can change independently of the
// template+parameters the state hash covers, so the shortcut must be bypassed to avoid leaving
// stale settings on the stack and to preserve ${VAR} resolution/validation.
func (p *BicepProvider) useDeploymentStateShortcut(parametersHashErr error) bool {
	if p.ignoreDeploymentState || parametersHashErr != nil {
		return false
	}

	return !p.hasActiveDeploymentStacksConfig()
}

// resolveDeploymentStacksMap resolves the typed deployment-stacks configuration into a
// camelCase map[string]any consumable by the deployment-stacks API layer
// (azapi.parseDeploymentStackOptions). It performs ${VAR} environment-variable substitution
// on denySettings.excludedPrincipals and denySettings.excludedActions, resolving values from
// plan-time layer outputs (VirtualEnv) first and then the azd environment.
//
// When includeDenySettings is false the denySettings block is omitted entirely (and therefore
// not resolved). The stack delete APIs only consume actionOnUnmanage and the bypass flag, so the
// destroy path passes false to avoid failing `azd down` when a ${VAR} referenced only by the deny
// lists is no longer available.
//
// It returns nil when no deployment-stacks configuration is present, in which case the caller
// should omit the DeploymentStacks key entirely so the API layer applies its defaults.
func (p *BicepProvider) resolveDeploymentStacksMap(includeDenySettings bool) (map[string]any, error) {
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

	if includeDenySettings && cfg.DenySettings != nil {
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
// plan-time layer outputs (VirtualEnv) first and then the azd environment. A bare ${VAR} reference
// that remains unresolved or a value that resolves to an empty string is treated as a
// misconfiguration. Shell-style default expressions such as ${VAR:-fallback} are honored:
// Envsubst applies the default, so a reference with a usable default is accepted.
func (p *BicepProvider) resolveDeploymentStacksValues(values []osutil.ExpandableString) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	lookup := func(name string) string {
		if v, has := p.options.VirtualEnv[name]; has {
			return v
		}
		v, _ := p.env.LookupEnv(name)
		return v
	}

	resolved := make([]string, 0, len(values))
	for _, value := range values {
		tree, err := parse.Parse(value.Raw())
		if err != nil {
			return nil, fmt.Errorf("resolving deploymentStacks value: %w", err)
		}

		unresolved := unsetDeploymentStacksReferences(tree.Root, lookup)
		if len(unresolved) > 0 {
			return nil, fmt.Errorf(
				"deploymentStacks references unset environment variable(s): %s",
				strings.Join(unresolved, ", "))
		}

		substituted, err := value.Envsubst(lookup)
		if err != nil {
			return nil, fmt.Errorf("resolving deploymentStacks value: %w", err)
		}
		if strings.TrimSpace(substituted) == "" {
			return nil, fmt.Errorf("deploymentStacks value resolved to an empty string")
		}

		resolved = append(resolved, substituted)
	}

	return resolved, nil
}

func unsetDeploymentStacksReferences(node parse.Node, lookup func(string) string) []string {
	var children []parse.Node
	switch node := node.(type) {
	case *parse.ListNode:
		children = node.Nodes
	case *parse.FuncNode:
		switch node.Name {
		case "=", ":=", ":-", ":?", ":+", "-", "+":
			// Envsubst evaluates fallback arguments eagerly; only validate a fallback when it is selected.
			if lookup(node.Param) != "" {
				return nil
			}
		case "":
			if len(node.Args) == 0 && lookup(node.Param) == "" {
				return []string{node.Param}
			}
		default:
			if lookup(node.Param) == "" {
				return []string{node.Param}
			}
		}
		children = node.Args
	}

	var unset []string
	for _, child := range children {
		unset = append(unset, unsetDeploymentStacksReferences(child, lookup)...)
	}
	return unset
}

// deploymentOptionsMap builds the generic options map handed to the deployment API layer,
// with the deployment-stacks configuration resolved (including ${VAR} substitution). Standard
// (non-stack) deployments ignore this map; stack deployments read only the DeploymentStacks key.
//
// Resolution is skipped entirely when the Deployment Stacks alpha feature is inactive, so an
// otherwise-valid standard provision can't be failed by an unavailable ${VAR} in an inactive
// deploymentStacks block. includeDenySettings is false on the destroy path, where the deny lists
// are not consumed by the stack delete APIs.
func (p *BicepProvider) deploymentOptionsMap(includeDenySettings bool) (map[string]any, error) {
	optionsMap, err := convert.ToMap(p.options)
	if err != nil {
		return nil, err
	}

	// The typed DeploymentStacksConfig is not JSON-serializable in a form the API layer can
	// consume (ExpandableString has no exported fields), so always drop whatever convert.ToMap
	// produced for it and only re-add a resolved map when stacks are actually in play.
	delete(optionsMap, "DeploymentStacks")

	if !p.deploymentStacksEnabled() {
		return optionsMap, nil
	}

	stacks, err := p.resolveDeploymentStacksMap(includeDenySettings)
	if err != nil {
		return nil, err
	}

	if stacks != nil {
		optionsMap["DeploymentStacks"] = stacks
	}

	return optionsMap, nil
}
