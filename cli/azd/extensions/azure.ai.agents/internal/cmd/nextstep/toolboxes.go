// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/types/known/structpb"
)

type bundledToolbox struct {
	Name string `json:"name"`
}

// UnmarshalJSON accepts both a toolbox name and a toolbox object.
func (t *bundledToolbox) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		t.Name = name
		return nil
	}

	type toolbox bundledToolbox
	var value toolbox
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*t = bundledToolbox(value)
	return nil
}

// populateToolboxes assembles split, bundled, and legacy toolboxes.
// Split services have precedence over bundled definitions, which have
// precedence over legacy manifests.
func populateToolboxes(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	errs *[]error,
) splitToolboxResult {
	splitState := populateSplitToolboxes(
		ctx,
		src,
		envName,
		projectCfg,
		state,
		errs,
	)
	if projectCfg == nil || state == nil {
		return splitState
	}

	fallbacks := collectBundledAndLegacyToolboxes(
		ctx,
		src,
		envName,
		projectCfg,
		state,
		splitState.excludedAgents,
		splitState.checkedAgents,
		errs,
	)
	mergeToolboxFallbacks(state, fallbacks, splitState.reservedKeys)
	slices.SortFunc(state.Toolboxes, func(a, b ResourceRef) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ServiceName, b.ServiceName)
	})
	slices.Sort(state.ToolboxLoadErrors)
	state.HasToolboxes = len(state.Toolboxes) > 0
	return splitState
}

func collectBundledAndLegacyToolboxes(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	excludedAgents map[string]struct{},
	checkedAgents map[string]struct{},
	errs *[]error,
) map[string]ResourceRef {
	collected := make(map[string]ResourceRef)
	if excludedAgents == nil {
		excludedAgents = make(map[string]struct{})
	}
	if checkedAgents == nil {
		checkedAgents = make(map[string]struct{})
	}

	for _, serviceName := range sortedServiceKeys(projectCfg) {
		svc := projectCfg.Services[serviceName]
		if svc == nil || svc.GetHost() != agentHost {
			continue
		}
		agentName := serviceName
		if strings.TrimSpace(agentName) == "" {
			agentName = strings.TrimSpace(svc.GetName())
		}
		if _, excluded := excludedAgents[serviceName]; excluded {
			continue
		}
		if _, excluded := excludedAgents[agentName]; excluded {
			continue
		}

		resolved, explicit, err := resolveAgentToolboxConfig(
			svc,
			projectCfg.Path,
		)
		if err != nil {
			if agentServiceMayContainToolboxes(svc) {
				enabled, conditionErr := ensureAgentServiceEnabled(
					ctx,
					src,
					envName,
					serviceName,
					checkedAgents,
				)
				if conditionErr != nil {
					recordToolboxLoadError(
						state,
						errs,
						fmt.Sprintf(
							"agent service %q deployment condition: %v",
							agentName,
							conditionErr,
						),
					)
					addExcludedAgent(excludedAgents, serviceName)
					continue
				}
				if !enabled {
					addExcludedAgent(excludedAgents, serviceName)
					continue
				}
			}
			recordToolboxLoadError(
				state,
				errs,
				fmt.Sprintf("agent service %q: %v", agentName, err),
			)
			continue
		}
		if resolvedToolboxConditionError(
			state,
			errs,
			"agent service",
			agentName,
			resolved,
		) {
			continue
		}

		var bundled []bundledToolbox
		var decodeErr error
		if explicit {
			bundled, decodeErr = decodeBundledToolboxes(resolved)
		}

		var legacy []string
		if !explicit {
			data := readManifestBytes(projectCfg.Path, svc.GetRelativePath())
			if data != nil {
				resources, err := agent_yaml.ExtractResourceDefinitions(data)
				if err == nil {
					for _, resource := range resources {
						toolbox, ok := resource.(agent_yaml.ToolboxResource)
						if ok && strings.TrimSpace(toolbox.Name) != "" {
							legacy = append(legacy, toolbox.Name)
						}
					}
				}
			}
		}

		hasBundled := explicit && hasBundledToolboxEntries(resolved)
		if !hasBundled && len(legacy) == 0 {
			continue
		}

		enabled, conditionErr := ensureAgentServiceEnabled(
			ctx,
			src,
			envName,
			serviceName,
			checkedAgents,
		)
		if conditionErr != nil {
			recordToolboxLoadError(
				state,
				errs,
				fmt.Sprintf(
					"agent service %q deployment condition: %v",
					agentName,
					conditionErr,
				),
			)
			addExcludedAgent(excludedAgents, serviceName)
			continue
		}
		if !enabled {
			addExcludedAgent(excludedAgents, serviceName)
			continue
		}

		if decodeErr != nil {
			recordToolboxLoadError(
				state,
				errs,
				fmt.Sprintf(
					"agent service %q: decode toolboxes: %v",
					agentName,
					decodeErr,
				),
			)
			continue
		}

		for _, toolbox := range bundled {
			addToolboxIfHigherPriority(
				collected,
				ResourceRef{
					Name:          toolbox.Name,
					ServiceName:   agentName,
					ToolboxSource: ToolboxSourceBundled,
				},
			)
		}
		for _, name := range legacy {
			addToolboxIfHigherPriority(
				collected,
				ResourceRef{
					Name:          name,
					ServiceName:   agentName,
					ToolboxSource: ToolboxSourceLegacyManifest,
				},
			)
		}
	}
	return collected
}

func decodeBundledToolboxes(
	resolved map[string]any,
) ([]bundledToolbox, error) {
	raw, found := resolved["toolboxes"]
	if !found {
		return nil, nil
	}

	entries, ok := raw.([]any)
	if !ok {
		return nil, errors.New("toolboxes must be an array")
	}

	decoded := make([]bundledToolbox, 0, len(entries))
	for i, entry := range entries {
		if entry == nil {
			return nil, fmt.Errorf(
				"toolboxes[%d] must be a named toolbox string or object",
				i,
			)
		}

		data, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("toolboxes[%d]: %w", i, err)
		}

		var toolbox bundledToolbox
		if err := json.Unmarshal(data, &toolbox); err != nil {
			return nil, fmt.Errorf("toolboxes[%d]: %w", i, err)
		}
		if strings.TrimSpace(toolbox.Name) == "" {
			return nil, fmt.Errorf(
				"toolboxes[%d] must include a non-empty name",
				i,
			)
		}
		decoded = append(decoded, toolbox)
	}
	return decoded, nil
}

func hasBundledToolboxEntries(resolved map[string]any) bool {
	raw, found := resolved["toolboxes"]
	if !found {
		return false
	}
	entries, ok := raw.([]any)
	return !ok || len(entries) > 0
}

func agentServiceMayContainToolboxes(svc *azdext.ServiceConfig) bool {
	if svc == nil {
		return false
	}
	for _, props := range []*structpb.Struct{
		svc.GetAdditionalProperties(),
		svc.GetConfig(),
	} {
		if props == nil {
			continue
		}
		fields := props.GetFields()
		if _, found := fields["toolboxes"]; found {
			return true
		}
		if _, found := fields["$ref"]; found {
			return true
		}
	}
	return false
}

func ensureAgentServiceEnabled(
	ctx context.Context,
	src Source,
	envName string,
	serviceName string,
	checkedAgents map[string]struct{},
) (bool, error) {
	if _, checked := checkedAgents[serviceName]; checked {
		return true, nil
	}
	enabled, err := isServiceEnabled(ctx, src, envName, serviceName)
	checkedAgents[serviceName] = struct{}{}
	return enabled, err
}

func addExcludedAgent(
	excludedAgents map[string]struct{},
	serviceName string,
) {
	excludedAgents[serviceName] = struct{}{}
}

func resolveAgentToolboxConfig(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (map[string]any, bool, error) {
	inline, err := resolveAgentToolboxProperties(
		svc.GetAdditionalProperties(),
		projectRoot,
		"service-level properties",
	)
	if err != nil {
		return nil, false, err
	}
	if len(inline) > 0 &&
		(hasToolboxField(inline) || mapHasToolboxKind(inline)) {
		_, explicit := inline["toolboxes"]
		return inline, explicit, nil
	}

	legacy, err := resolveAgentToolboxProperties(
		svc.GetConfig(),
		projectRoot,
		"deprecated config",
	)
	if err != nil {
		return nil, false, err
	}

	resolved := selectAgentToolboxProperties(inline, legacy)
	if len(resolved) == 0 {
		return nil, false, nil
	}
	_, explicit := resolved["toolboxes"]
	return resolved, explicit, nil
}

func hasToolboxField(values map[string]any) bool {
	_, found := values["toolboxes"]
	return found
}

func resolveAgentToolboxProperties(
	props *structpb.Struct,
	projectRoot string,
	source string,
) (map[string]any, error) {
	return resolveAgentConnectionProperties(props, projectRoot, source)
}

func resolveToolboxServiceProperties(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (map[string]any, error) {
	if svc == nil {
		return nil, errors.New("service configuration is nil")
	}
	if props := svc.GetAdditionalProperties(); props != nil &&
		len(props.GetFields()) > 0 {
		return resolveServiceProperties(svc, projectRoot)
	}
	if props := svc.GetConfig(); props != nil &&
		len(props.GetFields()) > 0 {
		return resolveAgentToolboxProperties(
			props,
			projectRoot,
			"deprecated config",
		)
	}
	return map[string]any{}, nil
}

func selectAgentToolboxProperties(
	inline, legacy map[string]any,
) map[string]any {
	if len(inline) == 0 {
		return legacy
	}
	if _, found := inline["toolboxes"]; found {
		return inline
	}
	if !mapHasToolboxKind(inline) &&
		mapHasToolboxKind(legacy) {
		return legacy
	}
	return inline
}

func mapHasToolboxKind(values map[string]any) bool {
	kind, ok := values["kind"].(string)
	return ok && strings.TrimSpace(kind) != ""
}

func addToolboxIfHigherPriority(
	collected map[string]ResourceRef,
	ref ResourceRef,
) {
	key := envkey.ToolboxMCPEndpoint(ref.Name)
	existing, found := collected[key]
	if !found || toolboxSourcePriority(ref.ToolboxSource) >
		toolboxSourcePriority(existing.ToolboxSource) {
		collected[key] = ref
	}
}

func toolboxSourcePriority(source ToolboxSource) int {
	switch source {
	case ToolboxSourceBundled:
		return 2
	case ToolboxSourceLegacyManifest:
		return 1
	case ToolboxSourceSplit:
		return 3
	default:
		return 0
	}
}

func mergeToolboxFallbacks(
	state *State,
	fallbacks map[string]ResourceRef,
	reservedKeys map[string]struct{},
) {
	merged := make(map[string]ResourceRef, len(state.Toolboxes)+len(fallbacks))
	for _, ref := range state.Toolboxes {
		merged[envkey.ToolboxMCPEndpoint(ref.Name)] = ref
	}
	for key, ref := range fallbacks {
		if _, reserved := reservedKeys[key]; reserved {
			continue
		}
		if existing, found := merged[key]; found &&
			toolboxSourcePriority(existing.ToolboxSource) >=
				toolboxSourcePriority(ref.ToolboxSource) {
			continue
		}
		merged[key] = ref
	}

	state.Toolboxes = state.Toolboxes[:0]
	for _, ref := range merged {
		state.Toolboxes = append(state.Toolboxes, ref)
	}
}

func recordToolboxLoadError(
	state *State,
	errs *[]error,
	issue string,
) {
	if recordToolboxLoadIssue(state, issue) {
		*errs = append(*errs, errors.New(issue))
	}
}

func recordToolboxLoadIssue(state *State, issue string) bool {
	if slices.Contains(state.ToolboxLoadErrors, issue) {
		return false
	}
	state.ToolboxLoadErrors = append(state.ToolboxLoadErrors, issue)
	return true
}

func resolvedToolboxConditionError(
	state *State,
	errs *[]error,
	serviceType string,
	serviceName string,
	resolved map[string]any,
) bool {
	if _, found := resolved["condition"]; !found {
		return false
	}
	issue := fmt.Sprintf(
		"%s %q has condition in its resolved $ref; "+
			"put condition beside host in azure.yaml",
		serviceType,
		serviceName,
	)
	recordToolboxLoadError(state, errs, issue)
	return true
}
