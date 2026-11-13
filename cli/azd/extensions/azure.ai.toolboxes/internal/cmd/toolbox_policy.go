// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/shared/foundry/policy"
)

const (
	toolboxDeniedToolsPolicyID    = policy.DeniedToolsPolicyID
	toolboxAllowedToolsPolicyID   = policy.AllowedToolsPolicyID
	toolboxDefaultGovernPortalURL = "https://ai.azure.com/?flight=govern_ignite"
	toolboxPolicyPreflightTimeout = policy.DefaultPolicyPreflightTimeout
)

type toolboxResourceGraphRequest = policy.ResourceGraphRequest
type toolboxPolicyQueryFunc = policy.ResourceGraphQueryFunc
type toolboxPolicyAssignment = policy.PolicyAssignment

func shouldSkipToolboxPolicyChecks(env map[string]string) bool {
	return policy.ShouldSkipPolicyChecks(env)
}

func isValidToolboxPolicyAssignmentID(assignmentID string) bool {
	return policy.IsValidPolicyAssignmentID(assignmentID)
}

func buildToolboxPolicyPortalURL(projectResourceID, assignmentID string) string {
	return policy.BuildPolicyAssignmentPortalURL(projectResourceID, assignmentID)
}

func newToolboxARMPipeline(cred azcore.TokenCredential) runtime.Pipeline {
	return policy.NewARMPipeline(cred, "azure-ai-toolboxes-policy", "v1.0.0", nil)
}

func newToolboxResourceGraphQueryFunc(pipeline runtime.Pipeline) toolboxPolicyQueryFunc {
	return policy.NewResourceGraphQueryFunc(pipeline, "", nil)
}

func extractToolboxToolCandidates(tools []map[string]any) []policy.ToolCandidate {
	candidates := make([]policy.ToolCandidate, 0, len(tools))
	for _, tool := range tools {
		toolType, _ := tool["type"].(string)
		toolName, _ := tool["name"].(string)
		serverURL, _ := tool["server_url"].(string)
		if serverURL == "" {
			serverURL, _ = tool["serverUrl"].(string)
		}
		connectorID, _ := tool["connector_id"].(string)
		if connectorID == "" {
			connectorID, _ = tool["connectorId"].(string)
		}
		connID, _ := tool["project_connection_id"].(string)
		if connID == "" {
			connID, _ = tool["connection_id"].(string)
		}
		candidates = append(candidates, policy.ToolCandidate{
			Type:         strings.TrimSpace(toolType),
			Name:         strings.TrimSpace(toolName),
			ConnectionID: strings.TrimSpace(connID),
			ServerURL:    strings.TrimSpace(serverURL),
			ConnectorID:  strings.TrimSpace(connectorID),
		})
	}
	return candidates
}

func checkToolboxToolsPolicy(
	ctx context.Context,
	access *toolboxAccess,
	scope *toolboxAccessScope,
	toolboxName string,
	tools []map[string]any,
) error {
	if access == nil || scope == nil || scope.resource == nil || scope.tenantID == "" || len(tools) == 0 {
		return nil
	}

	var envValues map[string]string
	if access.environment != nil {
		if resp, err := access.environment.GetValues(ctx, nil); err == nil && resp != nil {
			envValues = make(map[string]string, len(resp.GetKeyValues()))
			for _, kv := range resp.GetKeyValues() {
				envValues[kv.GetKey()] = kv.GetValue()
			}
		}
	}
	if shouldSkipToolboxPolicyChecks(envValues) {
		return nil
	}

	evalCtx, cancel := context.WithTimeout(ctx, toolboxPolicyPreflightTimeout)
	defer cancel()

	queryFn := access.policyQuery
	if queryFn == nil {
		if access.newCredential == nil {
			return nil
		}
		cred, err := access.newCredential(scope.tenantID)
		if err != nil {
			log.Printf("toolbox policy check: failed to create credential (failing open): %v", err)
			return nil
		}
		queryFn = newToolboxResourceGraphQueryFunc(newToolboxARMPipeline(cred))
	}

	projectID := scope.resource.String()
	inv, err := policy.LoadInventory(evalCtx, projectID, scope.resource.SubscriptionID, queryFn)
	if err != nil {
		log.Printf("toolbox policy check: Resource Graph query failed for %q (failing open): %v", toolboxName, err)
		return nil
	}

	res := policy.EvaluateToolPolicy(inv, extractToolboxToolCandidates(tools))
	if res.Outcome != policy.OutcomeBlocked || len(res.Violations) == 0 {
		return nil
	}
	return toolboxPolicyViolationsToError(toolboxName, res.Violations)
}

func toolboxPolicyViolationsToError(toolboxName string, violations []policy.Violation) error {
	var messages []string
	var links []string
	for _, v := range violations {
		msg := strings.TrimSpace(v.Message)
		if msg == "" {
			msg = fmt.Sprintf("Blocked by Azure Policy %q (tool: %s).", v.DisplayName, v.TargetValue)
		}
		if !slices.Contains(messages, msg) {
			messages = append(messages, msg)
		}
		if url := strings.TrimSpace(v.PortalURL); url != "" {
			linkLine := fmt.Sprintf("  View policy (%s): %s", v.DisplayName, url)
			if !slices.Contains(links, linkLine) {
				links = append(links, linkLine)
			}
		}
	}

	prefix := "toolbox deployment is blocked by Azure Policy"
	if strings.TrimSpace(toolboxName) != "" {
		prefix = fmt.Sprintf("toolbox %q is blocked by Azure Policy", strings.TrimSpace(toolboxName))
	}
	fullMessage := fmt.Sprintf("%s: %s", prefix, strings.Join(messages, " "))

	suggestion := "Update the toolbox tools to comply with your organization's Azure Policy " +
		"assignments, or select a different Microsoft Foundry project."
	if len(links) > 0 {
		suggestion += "\n" + strings.Join(links, "\n")
	}

	return exterrors.Validation(exterrors.CodeToolBlockedByPolicy, fullMessage, suggestion)
}

func tryClassifyToolboxPolicyError(
	err error,
	toolboxName string,
	scope *toolboxAccessScope,
) (error, bool) {
	projectID := ""
	if scope != nil && scope.resource != nil {
		projectID = scope.resource.String()
	}
	violations, ok := policy.ClassifyDataPlanePolicyError(err, projectID)
	if !ok {
		return nil, false
	}
	for i := range violations {
		if violations[i].Target != "" && violations[i].Target != policy.TargetTool {
			return nil, false
		}
		displayName := strings.TrimSpace(violations[i].DisplayName)
		if displayName == "" {
			displayName = "Azure Policy assignment"
		}
		violations[i].DisplayName = displayName
		violations[i].Message = fmt.Sprintf(
			"The %q policy doesn't allow one or more tools in this toolbox.",
			displayName,
		)
	}
	return toolboxPolicyViolationsToError(toolboxName, violations), true
}
