// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver/legacybridge"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// registerLegacyServices is the only host registration point for the temporary
// pre-versioning compatibility bridge. See legacybridge for removal criteria.
func (s *Server) registerLegacyServices() error {
	return legacybridge.Register(s.grpcServer, map[string]any{
		azdext.AccountService_ServiceDesc.ServiceName:       s.accountService,
		azdext.AiModelService_ServiceDesc.ServiceName:       s.aiModelService,
		azdext.ComposeService_ServiceDesc.ServiceName:       s.composeService,
		azdext.ContainerService_ServiceDesc.ServiceName:     s.containerService,
		azdext.CopilotService_ServiceDesc.ServiceName:       s.copilotService,
		azdext.DeploymentService_ServiceDesc.ServiceName:    s.deploymentService,
		azdext.EnvironmentService_ServiceDesc.ServiceName:   s.environmentService,
		azdext.EventService_ServiceDesc.ServiceName:         s.eventService,
		azdext.ExtensionService_ServiceDesc.ServiceName:     s.extensionService,
		azdext.FrameworkService_ServiceDesc.ServiceName:     s.frameworkService,
		azdext.ProjectService_ServiceDesc.ServiceName:       s.projectService,
		azdext.PromptService_ServiceDesc.ServiceName:        s.promptService,
		azdext.ProvisioningService_ServiceDesc.ServiceName:  s.provisioningService,
		azdext.ServiceTargetService_ServiceDesc.ServiceName: s.serviceTargetService,
		azdext.TelemetryService_ServiceDesc.ServiceName:     s.telemetryService,
		azdext.UserConfigService_ServiceDesc.ServiceName:    s.userConfigService,
		azdext.ValidationService_ServiceDesc.ServiceName:    s.validationService,
		azdext.WorkflowService_ServiceDesc.ServiceName:      s.workflowService,
	})
}
