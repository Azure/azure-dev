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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/types/known/structpb"
)

type bundledConnection struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Target   string `json:"target"`
}

type bundledConnectionConfig struct {
	Connections []bundledConnection `json:"connections"`
}

// populateConnections merges enabled unified connection services,
// bundled agent config, and legacy manifest resources. Same Foundry
// names keep split > bundled > manifest precedence.
func populateConnections(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	errs *[]error,
) {
	if projectCfg == nil || state == nil {
		return
	}

	collected := map[string]ResourceRef{}
	collectSplitConnections(
		ctx,
		src,
		envName,
		projectCfg,
		state,
		errs,
		collected,
	)
	collectBundledConnections(
		ctx,
		src,
		envName,
		projectCfg,
		state,
		errs,
		collected,
	)
	collectManifestConnections(
		ctx,
		src,
		envName,
		projectCfg,
		state,
		errs,
		collected,
	)

	refs := make([]ResourceRef, 0, len(collected))
	for _, ref := range collected {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b ResourceRef) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ServiceName, b.ServiceName)
	})
	if len(refs) == 0 {
		state.Connections = nil
	} else {
		state.Connections = refs
	}
	state.HasConnections = len(refs) > 0
	slices.Sort(state.ConnectionLoadErrors)
}

func collectSplitConnections(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	errs *[]error,
	collected map[string]ResourceRef,
) {
	for _, serviceName := range sortedServiceKeys(projectCfg) {
		svc := projectCfg.Services[serviceName]
		if svc == nil || svc.GetHost() != connectionHost {
			continue
		}

		enabled, err := isServiceEnabled(ctx, src, envName, serviceName)
		if err != nil {
			recordConnectionLoadError(
				state,
				errs,
				fmt.Sprintf(
					"connection service %q has an invalid deployment condition: %v",
					serviceName,
					err,
				),
			)
			continue
		}
		if !enabled {
			continue
		}

		resolved, err := resolveServiceProperties(svc, projectCfg.Path)
		if err != nil {
			recordConnectionLoadError(
				state,
				errs,
				fmt.Sprintf(
					"connection service %q: %v",
					serviceName,
					err,
				),
			)
			continue
		}
		recordResolvedConditionError(
			state,
			errs,
			"connection service",
			serviceName,
			resolved,
		)

		var decoded bundledConnection
		if err := decodeJSONMap(resolved, &decoded); err != nil {
			recordConnectionLoadError(
				state,
				errs,
				fmt.Sprintf(
					"connection service %q: decode connection: %v",
					serviceName,
					err,
				),
			)
			continue
		}
		if _, exists := collected[serviceName]; exists {
			continue
		}
		collected[serviceName] = ResourceRef{
			Name:        serviceName,
			ServiceName: serviceName,
			Detail: formatConnectionDetail(
				decoded.Category,
				decoded.Target,
			),
		}
	}
}

func collectBundledConnections(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	errs *[]error,
	collected map[string]ResourceRef,
) {
	for _, serviceName := range sortedServiceKeys(projectCfg) {
		svc := projectCfg.Services[serviceName]
		if svc == nil || svc.GetHost() != agentHost {
			continue
		}
		enabled, err := isServiceEnabled(ctx, src, envName, serviceName)
		if err != nil {
			recordConnectionLoadError(
				state,
				errs,
				fmt.Sprintf(
					"agent service %q deployment condition: %v",
					serviceName,
					err,
				),
			)
			continue
		}
		if !enabled {
			continue
		}

		resolved, err := resolveAgentDefinition(svc, projectCfg.Path)
		if err != nil {
			recordConnectionLoadError(
				state,
				errs,
				fmt.Sprintf(
					"agent service %q: %v",
					serviceName,
					err,
				),
			)
			continue
		}
		if resolved == nil {
			continue
		}
		recordResolvedConditionError(
			state,
			errs,
			"agent service",
			serviceName,
			resolved,
		)

		var decoded bundledConnectionConfig
		if err := decodeJSONMap(resolved, &decoded); err != nil {
			recordConnectionLoadError(
				state,
				errs,
				fmt.Sprintf(
					"agent service %q: decode connections: %v",
					serviceName,
					err,
				),
			)
			continue
		}
		for _, conn := range decoded.Connections {
			if conn.Name == "" {
				continue
			}
			if _, exists := collected[conn.Name]; exists {
				continue
			}
			collected[conn.Name] = ResourceRef{
				Name:        conn.Name,
				ServiceName: serviceName,
				Detail: formatConnectionDetail(
					conn.Category,
					conn.Target,
				),
			}
		}
	}
}

func collectManifestConnections(
	ctx context.Context,
	src Source,
	envName string,
	projectCfg *azdext.ProjectConfig,
	state *State,
	errs *[]error,
	collected map[string]ResourceRef,
) {
	for _, serviceName := range sortedServiceKeys(projectCfg) {
		svc := projectCfg.Services[serviceName]
		if svc == nil || svc.GetHost() != agentHost {
			continue
		}
		enabled, err := isServiceEnabled(ctx, src, envName, serviceName)
		if err != nil {
			recordConnectionLoadError(
				state,
				errs,
				fmt.Sprintf(
					"agent service %q deployment condition: %v",
					serviceName,
					err,
				),
			)
			continue
		}
		if !enabled {
			continue
		}

		data := readManifestBytes(projectCfg.Path, svc.GetRelativePath())
		if data == nil {
			continue
		}
		resources, err := agent_yaml.ExtractResourceDefinitions(data)
		if err != nil {
			continue
		}
		for _, resource := range resources {
			conn, ok := resource.(agent_yaml.ConnectionResource)
			if !ok || conn.Name == "" {
				continue
			}
			if _, exists := collected[conn.Name]; exists {
				continue
			}
			collected[conn.Name] = ResourceRef{
				Name:        conn.Name,
				ServiceName: serviceName,
				Detail:      connectionDetail(conn),
			}
		}
	}
}

func resolveServiceProperties(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (map[string]any, error) {
	raw := map[string]any{}
	if props := svc.GetAdditionalProperties(); props != nil {
		raw = props.AsMap()
	}
	if projectRoot == "" {
		return raw, nil
	}
	resolved, err := foundry.ResolveFileRefs(raw, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve $ref includes: %w", err)
	}
	return resolved, nil
}

func resolveAgentDefinition(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (map[string]any, error) {
	for _, candidate := range []struct {
		name  string
		props *structpb.Struct
	}{
		{
			name:  "service-level properties",
			props: svc.GetAdditionalProperties(),
		},
		{
			name:  "deprecated config",
			props: svc.GetConfig(),
		},
	} {
		if candidate.props == nil ||
			len(candidate.props.GetFields()) == 0 {
			continue
		}
		raw := candidate.props.AsMap()
		resolved := raw
		if projectRoot != "" {
			var err error
			resolved, err = foundry.ResolveFileRefs(raw, projectRoot)
			if err != nil {
				return nil, fmt.Errorf(
					"resolve %s: %w",
					candidate.name,
					err,
				)
			}
		}
		if !mapHasKind(resolved) {
			continue
		}
		return resolved, nil
	}
	return nil, nil
}

func mapHasKind(values map[string]any) bool {
	kind, ok := values["kind"].(string)
	return ok && strings.TrimSpace(kind) != ""
}

func decodeJSONMap(values map[string]any, out any) error {
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func sortedServiceKeys(projectCfg *azdext.ProjectConfig) []string {
	keys := make([]string, 0, len(projectCfg.Services))
	for name := range projectCfg.Services {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	return keys
}

func recordConnectionLoadError(
	state *State,
	errs *[]error,
	issue string,
) {
	if slices.Contains(state.ConnectionLoadErrors, issue) {
		return
	}
	state.ConnectionLoadErrors = append(
		state.ConnectionLoadErrors,
		issue,
	)
	*errs = append(*errs, errors.New(issue))
}

func recordResolvedConditionError(
	state *State,
	errs *[]error,
	serviceType string,
	serviceName string,
	resolved map[string]any,
) {
	if _, found := resolved["condition"]; !found {
		return
	}
	recordConnectionLoadError(
		state,
		errs,
		fmt.Sprintf(
			"%s %q has condition in its resolved $ref; "+
				"put condition beside host in azure.yaml",
			serviceType,
			serviceName,
		),
	)
}
