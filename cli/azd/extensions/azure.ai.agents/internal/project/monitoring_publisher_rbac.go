// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v4"
)

// monitoringGrantRetryDelay is a variable so tests can avoid waiting.
var monitoringGrantRetryDelay = defaultMonitoringGrantRetryDelay

type monitoringRoleAssigner func(
	ctx context.Context,
	principalID, roleID, roleName, scope string,
	principalType armauthorization.PrincipalType,
) (bool, error)

// monitoringComponentToucher refreshes the component after a role assignment. Application Insights
// caches Entra authorization for ingestion, and without a component update a new Monitoring Metrics
// Publisher assignment kept returning 403 for over 20 minutes in live runs.
//
// created reports whether this deploy created the assignment. When it did not, the toucher only
// updates the component if the sync tag is missing, which recovers from an earlier failed touch.
type monitoringComponentToucher func(ctx context.Context, componentID string, created bool) error

// privateMonitoringComponentID returns the App Insights component only when azd created it and
// connected it through the private path (Private Link Scope). Public-network projects and
// user-supplied components return "" so azd never changes their role assignments or tags.
func privateMonitoringComponentID(azdEnv map[string]string) string {
	if strings.TrimSpace(azdEnv[envFoundryMonitoringMode]) != monitoringModeCreate ||
		strings.TrimSpace(azdEnv[envMonitoringPrivateLinkScope]) == "" {
		return ""
	}
	return strings.TrimSpace(azdEnv[envApplicationInsightsResource])
}

// roleAssignmentsSkipped reports whether the user opted out of azd writing role assignments, using
// the same precedence as the developer RBAC check: environment value first, then the process.
func roleAssignmentsSkipped(azdEnv map[string]string) bool {
	value := strings.TrimSpace(azdEnv[envSkipRoleAssignments])
	if value == "" {
		value = os.Getenv(envSkipRoleAssignments)
	}
	skip, err := strconv.ParseBool(value)
	return err == nil && skip
}

func monitoringGrantCommand(principalID, componentID string) string {
	return fmt.Sprintf(
		"az role assignment create --assignee-object-id %s --assignee-principal-type ServicePrincipal "+
			"--role \"%s\" --scope %s",
		principalID, roleMonitoringMetricsPublisherName, componentID,
	)
}

// grantHostedAgentMonitoringPublisher lets a hosted agent's instance identity publish telemetry to the
// project's private App Insights component. The identity only exists after the agent version is
// created, so this cannot be done in the provisioning template.
//
// It is best effort and never fails the deploy. It returns a warning for the deploy note when
// telemetry may still be rejected, or "" when nothing needs the user's attention.
func grantHostedAgentMonitoringPublisher(
	ctx context.Context,
	assign monitoringRoleAssigner,
	touch monitoringComponentToucher,
	azdEnv map[string]string,
	principalID string,
) string {
	componentID := privateMonitoringComponentID(azdEnv)
	principalID = strings.TrimSpace(principalID)
	if componentID == "" || principalID == "" {
		return ""
	}
	if roleAssignmentsSkipped(azdEnv) {
		return fmt.Sprintf(
			"AZD_AGENT_SKIP_ROLE_ASSIGNMENTS is set, so azd did not grant the hosted agent identity access to "+
				"Application Insights. Hosted-agent telemetry is rejected with 403 until it has the role:\n  %s",
			monitoringGrantCommand(principalID, componentID),
		)
	}

	var lastErr error
	for attempt := 1; attempt <= monitoringGrantAttempts; attempt++ {
		created, err := assign(
			ctx, principalID, roleMonitoringMetricsPublisher, roleMonitoringMetricsPublisherName,
			componentID, armauthorization.PrincipalTypeServicePrincipal,
		)
		if err == nil {
			if touch != nil {
				if touchErr := touch(ctx, componentID, created); touchErr != nil {
					return fmt.Sprintf(
						"Granted the hosted agent identity Monitoring Metrics Publisher, but could not refresh %s: %v. "+
							"Hosted-agent telemetry can return 403 for a long time until the component is updated; "+
							"changing any tag on it forces a refresh.",
						componentID, touchErr,
					)
				}
			}
			return ""
		}
		lastErr = err
		if !strings.Contains(err.Error(), "PrincipalNotFound") || attempt == monitoringGrantAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(monitoringGrantRetryDelay):
		}
	}

	return fmt.Sprintf(
		"Could not grant the hosted agent identity Monitoring Metrics Publisher on %s: %v\n"+
			"Hosted-agent telemetry is rejected with 403 until it has the role. Grant it with:\n  %s",
		componentID, lastErr, monitoringGrantCommand(principalID, componentID),
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

// ensureHostedAgentMonitoringPublisher wires the real ARM clients into grantHostedAgentMonitoringPublisher
// and returns its warning for the deploy note.
func (p *AgentServiceTargetProvider) ensureHostedAgentMonitoringPublisher(
	ctx context.Context,
	azdEnv map[string]string,
	principalID string,
) string {
	componentID := privateMonitoringComponentID(azdEnv)
	if componentID == "" || strings.TrimSpace(principalID) == "" {
		return ""
	}
	assign, err := newMonitoringRoleAssigner(p.credential, componentID)
	if err != nil {
		return fmt.Sprintf(
			"Could not grant the hosted agent identity Monitoring Metrics Publisher: %v\n"+
				"Hosted-agent telemetry is rejected with 403 until it has the role. Grant it with:\n  %s",
			err, monitoringGrantCommand(strings.TrimSpace(principalID), componentID),
		)
	}
	touch := func(ctx context.Context, componentID string, created bool) error {
		tags, err := armresources.NewTagsClient(
			extractSubscriptionID(componentID), p.credential, azure.NewArmClientOptions(),
		)
		if err != nil {
			return err
		}
		return refreshMonitoringRoleSyncTag(ctx, pollingMonitoringTagsClient{tags}, componentID, created, time.Now())
	}
	return grantHostedAgentMonitoringPublisher(ctx, assign, touch, azdEnv, principalID)
}
