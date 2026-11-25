// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "time"

// Environment keys written by the azure.ai.projects provisioning extension. They are defined
// separately in that Go module, so these values must stay identical to its outputs.
const (
	envFoundryMonitoringMode       = "AZD_FOUNDRY_MONITORING_MODE"
	envMonitoringPrivateLinkScope  = "AZD_MONITORING_PRIVATE_LINK_SCOPE_ID"
	envApplicationInsightsResource = "AZURE_APPLICATION_INSIGHTS_RESOURCE_ID"
	envSkipRoleAssignments         = "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"

	// monitoringModeCreate means azd created the Application Insights component.
	monitoringModeCreate = "create"
)

const (
	roleMonitoringMetricsPublisher     = "3913510d-42f4-4e42-8a64-420c390055eb"
	roleMonitoringMetricsPublisherName = "Monitoring Metrics Publisher"

	// monitoringRoleSyncTag is merged onto the component after a grant so Application Insights
	// refreshes its cached ingestion authorization.
	monitoringRoleSyncTag = "azd-monitoring-role-sync"

	monitoringGrantAttempts = 4
)

// defaultMonitoringGrantRetryDelay is how long to wait between grant attempts. A new instance
// identity can take a short while to replicate before ARM accepts it as a role assignment principal.
const defaultMonitoringGrantRetryDelay = 15 * time.Second
