// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

const roleMonitoringMetricsPublisher = "3913510d-42f4-4e42-8a64-420c390055eb"

const monitoringGrantAttempts = 4

// monitoringGrantRetryDelay is a variable so tests can avoid waiting. A new instance identity
// can take a short while to replicate before ARM accepts it as a role assignment principal.
var monitoringGrantRetryDelay = 15 * time.Second

type monitoringRoleAssigner func(
	ctx context.Context,
	principalID, roleID, roleName, scope string,
	principalType armauthorization.PrincipalType,
) (bool, error)

// azdManagedMonitoringComponentID returns the App Insights component azd wired to the project,
// or "" when azd did not provision or connect monitoring for this environment.
func azdManagedMonitoringComponentID(azdEnv map[string]string) string {
	if strings.TrimSpace(azdEnv["AZD_FOUNDRY_MONITORING_MODE"]) == "" {
		return ""
	}
	return strings.TrimSpace(azdEnv["AZURE_APPLICATION_INSIGHTS_RESOURCE_ID"])
}

// grantHostedAgentMonitoringPublisher lets a hosted agent's instance identity publish telemetry to the
// project's App Insights component. The identity only exists after the agent version is created, so
// this cannot be done in the provisioning template. It is best effort: a failure prints a manual
// command and does not fail the deploy.
func grantHostedAgentMonitoringPublisher(
	ctx context.Context,
	assign monitoringRoleAssigner,
	azdEnv map[string]string,
	principalID string,
) {
	componentID := azdManagedMonitoringComponentID(azdEnv)
	principalID = strings.TrimSpace(principalID)
	if componentID == "" || principalID == "" {
		return
	}

	var lastErr error
	for attempt := 1; attempt <= monitoringGrantAttempts; attempt++ {
		_, err := assign(
			ctx, principalID, roleMonitoringMetricsPublisher, "Monitoring Metrics Publisher",
			componentID, armauthorization.PrincipalTypeServicePrincipal,
		)
		if err == nil {
			return
		}
		lastErr = err
		if !strings.Contains(err.Error(), "PrincipalNotFound") || attempt == monitoringGrantAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(monitoringGrantRetryDelay):
		}
	}

	_, _ = fmt.Fprintf(
		os.Stderr,
		"Warning: could not grant the agent identity Monitoring Metrics Publisher on %s: %v\n"+
			"Traces from the hosted agent are rejected with 403 until it has the role. Grant it with:\n"+
			"  az role assignment create --assignee-object-id %s --assignee-principal-type ServicePrincipal "+
			"--role \"Monitoring Metrics Publisher\" --scope %s\n",
		componentID, lastErr, principalID, componentID,
	)
}

func newMonitoringRoleAssigner(credential azcore.TokenCredential, componentID string) (monitoringRoleAssigner, error) {
	client, err := armauthorization.NewRoleAssignmentsClient(
		extractSubscriptionID(componentID), credential, azure.NewArmClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create role assignments client: %w", err)
	}
	return func(
		ctx context.Context,
		principalID, roleID, roleName, scope string,
		principalType armauthorization.PrincipalType,
	) (bool, error) {
		return assignRoleToIdentityWithClient(ctx, client, principalID, roleID, roleName, scope, principalType)
	}, nil
}

// ensureHostedAgentMonitoringPublisher wires the real ARM client into grantHostedAgentMonitoringPublisher.
func (p *AgentServiceTargetProvider) ensureHostedAgentMonitoringPublisher(
	ctx context.Context,
	azdEnv map[string]string,
	principalID string,
) {
	componentID := azdManagedMonitoringComponentID(azdEnv)
	if componentID == "" || strings.TrimSpace(principalID) == "" {
		return
	}
	assign, err := newMonitoringRoleAssigner(p.credential, componentID)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: could not grant Monitoring Metrics Publisher: %v\n", err)
		return
	}
	grantHostedAgentMonitoringPublisher(ctx, assign, azdEnv, principalID)
}
