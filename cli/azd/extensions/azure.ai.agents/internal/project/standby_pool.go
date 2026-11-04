// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS

package project

import (
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// RequireStandbyPoolHosting permits explicit standby targets only for opted-in
// agents bound through uses to a single existing-AKS project declaration.
func RequireStandbyPoolHosting(
	endpoint *agent_yaml.AgentEndpoint,
	service *azdext.ServiceConfig,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) error {
	if endpoint == nil || endpoint.StandbyPool == nil || endpoint.StandbyPool.Size == nil {
		return nil
	}
	if err := featureflags.Require(featureflags.BYOAKS); err != nil {
		return err
	}
	var projectService *azdext.ServiceConfig
	for _, name := range service.GetUses() {
		dependency := services[name]
		if dependency.GetHost() != foundryProjectHost {
			continue
		}
		if projectService != nil && projectService != dependency {
			return standbyPoolHostingError(service.GetName())
		}
		projectService = dependency
	}
	if projectService == nil {
		return standbyPoolHostingError(service.GetName())
	}
	props, err := ResolveServiceConfigProps(projectService, projectRoot)
	if err != nil {
		return err
	}
	fields := props.AsMap()
	hosting, _ := fields["agentHosting"].(map[string]any)
	clusterID, _ := hosting["clusterResourceId"].(string)
	if strings.TrimSpace(clusterID) == "" || fields["endpoint"] != nil {
		return standbyPoolHostingError(service.GetName())
	}
	return nil
}

func standbyPoolHostingError(service string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("agentEndpoint.standbyPool.size for service %q requires BYO AKS hosting", service),
		"declare exactly one azure.ai.project dependency in uses with agentHosting.clusterResourceId "+
			"and without endpoint, or remove agentEndpoint.standbyPool.size",
	)
}
