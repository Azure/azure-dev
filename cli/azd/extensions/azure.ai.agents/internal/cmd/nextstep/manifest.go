// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const toolboxHost = "azure.ai.toolbox"

// populateSplitToolboxes adds active toolbox dependencies to state.
func populateSplitToolboxes(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	errs *[]error,
) splitToolboxResult {
	if projectCfg == nil || state == nil {
		return splitToolboxResult{}
	}

	candidates, excludedAgents, checkedAgents := splitToolboxDependencies(
		ctx,
		src,
		envName,
		projectCfg,
		state,
		errs,
	)
	endpointKeys := make(map[string]struct{}, len(candidates))
	for key := range candidates {
		endpointKeys[key] = struct{}{}
	}
	if len(candidates) == 0 {
		slices.Sort(state.ToolboxDependencyErrors)
		slices.Sort(state.ToolboxLoadErrors)
		return splitToolboxResult{
			excludedAgents: excludedAgents,
			checkedAgents:  checkedAgents,
			endpointKeys:   endpointKeys,
			reservedKeys:   make(map[string]struct{}),
		}
	}

	split := make(map[string]ResourceRef)
	reserved := make(map[string]struct{}, len(candidates))
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	for _, key := range keys {
		candidate := candidates[key]
		reserved[key] = struct{}{}

		enabled, err := isServiceEnabled(
			ctx,
			src,
			envName,
			candidate.configName,
		)
		if err != nil {
			issue := fmt.Sprintf(
				"toolbox service %q used by agent service(s) %s has an invalid deployment condition: %v",
				candidate.ref.ServiceName,
				strings.Join(candidate.agents, ", "),
				err,
			)
			state.ToolboxDependencyErrors = append(
				state.ToolboxDependencyErrors,
				issue,
			)
			*errs = append(
				*errs,
				fmt.Errorf(
					"toolbox service %q deployment condition: %w",
					candidate.ref.ServiceName,
					err,
				),
			)
			recordToolboxLoadIssue(state, issue)
			continue
		}
		if !enabled {
			issue := fmt.Sprintf(
				"toolbox service %q used by agent service(s) %s is disabled by its deployment condition",
				candidate.ref.ServiceName,
				strings.Join(candidate.agents, ", "),
			)
			state.ToolboxDependencyErrors = append(
				state.ToolboxDependencyErrors,
				issue,
			)
			*errs = append(*errs, errors.New(issue))
			continue
		}

		svc := projectCfg.Services[candidate.configName]
		resolved, err := resolveToolboxServiceProperties(
			svc,
			projectCfg.Path,
		)
		if err != nil {
			recordToolboxLoadError(
				state,
				errs,
				fmt.Sprintf(
					"toolbox service %q: %v",
					candidate.ref.ServiceName,
					err,
				),
			)
			continue
		}
		if resolvedToolboxConditionError(
			state,
			errs,
			"toolbox service",
			candidate.ref.ServiceName,
			resolved,
		) {
			continue
		}

		if existing, found := split[key]; !found ||
			candidate.ref.ServiceName < existing.ServiceName {
			split[key] = candidate.ref
		}
	}

	merged := make([]ResourceRef, 0, len(state.Toolboxes)+len(split))
	for _, ref := range state.Toolboxes {
		if _, replaced := reserved[envkey.ToolboxMCPEndpoint(ref.Name)]; replaced {
			continue
		}
		merged = append(merged, ref)
	}
	for _, ref := range split {
		merged = append(merged, ref)
	}
	slices.SortFunc(merged, func(a, b ResourceRef) int {
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.ServiceName, b.ServiceName)
	})
	state.Toolboxes = merged
	state.HasToolboxes = len(merged) > 0
	slices.Sort(state.ToolboxDependencyErrors)
	return splitToolboxResult{
		excludedAgents: excludedAgents,
		checkedAgents:  checkedAgents,
		endpointKeys:   endpointKeys,
		reservedKeys:   reserved,
	}
}

type splitToolboxResult struct {
	excludedAgents map[string]struct{}
	checkedAgents  map[string]struct{}
	endpointKeys   map[string]struct{}
	reservedKeys   map[string]struct{}
}

type splitToolboxCandidate struct {
	ref        ResourceRef
	configName string
	agents     []string
}

type splitToolboxService struct {
	ref        ResourceRef
	configName string
}

func splitToolboxDependencies(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	errs *[]error,
) (map[string]splitToolboxCandidate, map[string]struct{}, map[string]struct{}) {
	services := make(map[string]splitToolboxService)
	for _, serviceName := range sortedServiceKeys(projectCfg) {
		svc := projectCfg.Services[serviceName]
		if svc == nil || svc.GetHost() != toolboxHost {
			continue
		}
		name := strings.TrimSpace(serviceName)
		if name == "" {
			name = strings.TrimSpace(svc.GetName())
		}
		if name == "" {
			continue
		}
		ref := ResourceRef{
			Name:          name,
			ServiceName:   name,
			ToolboxSource: ToolboxSourceSplit,
		}
		service := splitToolboxService{
			ref:        ref,
			configName: serviceName,
		}
		services[serviceName] = service
		if name != serviceName {
			services[name] = service
		}
	}

	candidates := make(map[string]splitToolboxCandidate)
	excludedAgents := make(map[string]struct{})
	checkedAgents := make(map[string]struct{})
	for _, serviceName := range sortedServiceKeys(projectCfg) {
		svc := projectCfg.Services[serviceName]
		if svc == nil || svc.GetHost() != agentHost {
			continue
		}
		agentName := strings.TrimSpace(serviceName)
		if agentName == "" {
			agentName = strings.TrimSpace(svc.GetName())
		}
		dependencies := make([]splitToolboxService, 0)
		for _, dependencyName := range svc.GetUses() {
			service, ok := services[dependencyName]
			if !ok {
				continue
			}
			dependencies = append(dependencies, service)
		}

		if len(dependencies) == 0 {
			continue
		}
		checkedAgents[serviceName] = struct{}{}
		enabled, err := isServiceEnabled(ctx, src, envName, serviceName)
		if err != nil {
			names := make([]string, 0, len(dependencies))
			for _, service := range dependencies {
				names = appendUnique(names, service.ref.ServiceName)
			}
			slices.Sort(names)
			issue := fmt.Sprintf(
				"agent service %q uses toolbox service(s) %s but has an invalid deployment condition: %v",
				agentName,
				strings.Join(names, ", "),
				err,
			)
			state.ToolboxDependencyErrors = append(
				state.ToolboxDependencyErrors,
				issue,
			)
			recordToolboxLoadIssue(state, issue)
			*errs = append(
				*errs,
				fmt.Errorf(
					"agent service %q deployment condition: %w",
					agentName,
					err,
				),
			)
			excludedAgents[agentName] = struct{}{}
			excludedAgents[serviceName] = struct{}{}
			continue
		}
		if !enabled {
			excludedAgents[agentName] = struct{}{}
			excludedAgents[serviceName] = struct{}{}
			continue
		}

		for _, service := range dependencies {
			key := envkey.ToolboxMCPEndpoint(service.ref.Name)
			candidate := candidates[key]
			if candidate.ref.Name == "" ||
				service.ref.ServiceName < candidate.ref.ServiceName {
				candidate.ref = service.ref
				candidate.configName = service.configName
			}
			candidate.agents = appendUnique(candidate.agents, agentName)
			candidates[key] = candidate
		}
	}

	for key, candidate := range candidates {
		slices.Sort(candidate.agents)
		candidates[key] = candidate
	}
	return candidates, excludedAgents, checkedAgents
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func formatConnectionDetail(category, target string) string {
	switch {
	case category != "" && target != "":
		return category + " | " + target
	case category != "":
		return category
	default:
		return target
	}
}
