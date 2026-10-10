// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

// FoundryConnectionServiceIdentity describes a Connection service.
type FoundryConnectionServiceIdentity struct {
	Host         string
	ServiceKey   string
	ResourceName string
}

// ResolveFoundryConnectionServiceIdentity prefers an exact key, then
// matches the effective resource name case-insensitively.
func ResolveFoundryConnectionServiceIdentity(
	services map[string]*azdext.ServiceConfig,
	reference string,
	projectRoot string,
) (FoundryConnectionServiceIdentity, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return FoundryConnectionServiceIdentity{}, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Connection service key or resource name is required",
			"use a local azure.ai.connection service key or resource name",
		)
	}

	if service, exists := services[reference]; exists {
		if service == nil || service.GetHost() != foundryConnectionHost {
			host := ""
			if service != nil {
				host = service.GetHost()
			}
			return FoundryConnectionServiceIdentity{}, exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf(
					"connection reference %q resolves to service host %q instead of %q",
					reference,
					host,
					foundryConnectionHost,
				),
				fmt.Sprintf(
					"use a service with host %q or a Connection resource name",
					foundryConnectionHost,
				),
			)
		}

		return foundryConnectionServiceIdentity(reference, service, projectRoot)
	}

	identities, err := foundryConnectionServiceIdentities(services, projectRoot)
	if err != nil {
		return FoundryConnectionServiceIdentity{}, err
	}

	var matches []FoundryConnectionServiceIdentity
	for _, identity := range identities {
		if strings.EqualFold(identity.ResourceName, reference) {
			matches = append(matches, identity)
		}
	}
	switch len(matches) {
	case 0:
		return FoundryConnectionServiceIdentity{}, exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf(
				"connection reference %q does not match an azure.ai.connection service key or resource name",
				reference,
			),
			"declare a local azure.ai.connection service with that key or name",
		)
	case 1:
		return matches[0], nil
	default:
		serviceKeys := make([]string, len(matches))
		for i, identity := range matches {
			serviceKeys[i] = identity.ServiceKey
		}
		slices.Sort(serviceKeys)
		return FoundryConnectionServiceIdentity{}, exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf(
				"connection reference %q is ambiguous: matches service keys %q",
				reference,
				serviceKeys,
			),
			"use a unique Connection resource name or an exact service key",
		)
	}
}

func foundryConnectionServiceIdentities(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) ([]FoundryConnectionServiceIdentity, error) {
	identities := make([]FoundryConnectionServiceIdentity, 0, len(services))
	for _, serviceKey := range slices.Sorted(maps.Keys(services)) {
		service := services[serviceKey]
		if service == nil || service.GetHost() != foundryConnectionHost {
			continue
		}

		identity, err := foundryConnectionServiceIdentity(serviceKey, service, projectRoot)
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

func foundryConnectionServiceIdentity(
	serviceKey string,
	service *azdext.ServiceConfig,
	projectRoot string,
) (FoundryConnectionServiceIdentity, error) {
	var properties map[string]any
	if props := ServiceConfigProps(service); props != nil {
		properties = props.AsMap()
	}
	if strings.TrimSpace(projectRoot) == "" && containsFileRef(properties) {
		return FoundryConnectionServiceIdentity{}, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("cannot resolve $ref for connection service %q: project root is empty", serviceKey),
			"provide the project directory containing azure.yaml to resolve connection definition references",
		)
	}

	resolved, err := foundry.ResolveFileRefs(properties, projectRoot)
	if err != nil {
		return FoundryConnectionServiceIdentity{}, err
	}
	resourceName, _ := resolved["name"].(string)
	resourceName = strings.TrimSpace(resourceName)
	if resourceName == "" {
		resourceName = serviceKey
	}

	return FoundryConnectionServiceIdentity{
		Host:         service.GetHost(),
		ServiceKey:   serviceKey,
		ResourceName: resourceName,
	}, nil
}
