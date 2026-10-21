// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// RequireAgentFeatures checks the inline or $ref definition.
func RequireAgentFeatures(
	ctx context.Context,
	client *azdext.AzdClient,
	service *azdext.ServiceConfig,
	projectRoot string,
) error {
	if service.GetHost() != foundryAgentHost {
		return nil
	}
	props := ServiceConfigProps(service)
	if props == nil {
		return nil
	}
	resolved, err := resolveServiceProps(props, service.GetName(), projectRoot)
	if err != nil {
		return err
	}
	return featureflags.RequireDefinition(ctx, client, resolved.AsMap())
}

func (p *AgentServiceTargetProvider) requirePreviewFeatures(ctx context.Context) error {
	if props := ServiceConfigProps(p.serviceConfig); props != nil &&
		containsFileRef(props.AsMap()) {
		if err := p.loadProjectPath(ctx); err != nil {
			return err
		}
	}
	return RequireAgentFeatures(ctx, p.azdClient, p.serviceConfig, p.projectPath)
}
