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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

type bundledConnection struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Target   string `json:"target"`
}

// populateConnections collects enabled unified connection services.
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
) bool {
	hasLoadError := false
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
			hasLoadError = true
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
			hasLoadError = true
			continue
		}
		if recordResolvedConditionError(
			state,
			errs,
			"connection service",
			serviceName,
			resolved,
		) {
			hasLoadError = true
		}

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
			hasLoadError = true
			continue
		}
		connectionName := decoded.Name
		if connectionName == "" {
			connectionName = serviceName
		}
		if _, exists := collected[connectionName]; exists {
			continue
		}
		collected[connectionName] = ResourceRef{
			Name:        connectionName,
			ServiceName: serviceName,
			Detail: formatConnectionDetail(
				decoded.Category,
				decoded.Target,
			),
		}
	}
	return hasLoadError
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
) bool {
	if _, found := resolved["condition"]; !found {
		return false
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
	return true
}
