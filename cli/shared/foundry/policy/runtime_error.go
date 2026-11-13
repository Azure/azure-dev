// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore noncompliant agentkind

package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/shared/foundry/ferrors"
)

var runtimePolicyAssignmentRegex = regexp.MustCompile(`(?i)noncompliant with policy (/[^\s:]+):`)

type runtimePolicyAdditionalInfo struct {
	PolicyAssignmentID      string `json:"policyAssignmentId"`
	PolicyAssignmentIDSnake string `json:"policy_assignment_id"`
	PolicyDefinitionID      string `json:"policyDefinitionId"`
	PolicyDefinitionIDSnake string `json:"policy_definition_id"`
	Effect                  string `json:"effect"`
}

func (a *runtimePolicyAdditionalInfo) assignmentID() string {
	if a == nil {
		return ""
	}
	if id := strings.TrimSpace(a.PolicyAssignmentID); id != "" {
		return id
	}
	return strings.TrimSpace(a.PolicyAssignmentIDSnake)
}

func (a *runtimePolicyAdditionalInfo) definitionID() string {
	if a == nil {
		return ""
	}
	if id := strings.TrimSpace(a.PolicyDefinitionID); id != "" {
		return id
	}
	return strings.TrimSpace(a.PolicyDefinitionIDSnake)
}

func safeRuntimePolicyViolationMessage(displayName string, target TargetKind) string {
	switch target {
	case TargetTool:
		return fmt.Sprintf("The %q policy doesn't allow one or more configured tools in this project.", displayName)
	case TargetAgentKind:
		return fmt.Sprintf("The %q policy doesn't allow this agent kind in this project.", displayName)
	case TargetContainerImage, TargetContainerRegistry:
		return fmt.Sprintf("The %q policy doesn't allow the configured container image in this project.", displayName)
	case TargetModel:
		return fmt.Sprintf("The %q policy doesn't allow the configured model in this project.", displayName)
	default:
		return fmt.Sprintf("The %q policy blocks this operation in this project.", displayName)
	}
}

func inferRuntimeTargetKind(policyDefinitionID, msgLower string) TargetKind {
	normDef := NormalizeScope(policyDefinitionID)
	switch normDef {
	case NormalizeScope(DeniedAgentKindsPolicyID),
		NormalizeScope(AllowedAgentKindsPolicyID):
		return TargetAgentKind
	case NormalizeScope(DeniedContainerImagesPolicyID),
		NormalizeScope(AllowedContainerImagesPolicyID):
		return TargetContainerImage
	case NormalizeScope(DeniedToolsPolicyID),
		NormalizeScope(AllowedToolsPolicyID):
		return TargetTool
	case NormalizeScope(AllowedModelsPolicyID),
		NormalizeScope(DeniedModelsPolicyID):
		return TargetModel
	default:
		switch {
		case strings.Contains(msgLower, "microsoft.cognitiveservices.data/accounts/projects/agents/tools") ||
			strings.Contains(msgLower, "microsoft.cognitiveservices.data/accounts/projects/toolboxes/tools") ||
			strings.Contains(msgLower, "/agents/tools[") ||
			strings.Contains(msgLower, "/toolboxes/tools["):
			return TargetTool
		case strings.Contains(msgLower, "/agents/agentkind"):
			return TargetAgentKind
		case strings.Contains(msgLower, "/agents/image"):
			return TargetContainerImage
		default:
			return ""
		}
	}
}

// ClassifyDataPlanePolicyError inspects a runtime Azure SDK error from Foundry data-plane
// operations (such as agent or toolbox creation/update) and extracts structured Violation
// entries when the HTTP 403 error represents an Azure Policy denial.
//
// It validates extracted policy assignment IDs with IsValidPolicyAssignmentID and synthesizes
// credential-safe violation messages without copying raw service error strings or request URLs.
func ClassifyDataPlanePolicyError(err error, projectResourceID string) ([]Violation, bool) {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok || respErr == nil || respErr.StatusCode != http.StatusForbidden {
		return nil, false
	}
	if respErr.RawResponse == nil || respErr.RawResponse.Body == nil {
		return nil, false
	}
	body, readErr := runtime.Payload(respErr.RawResponse)
	if readErr != nil || len(body) == 0 {
		return nil, false
	}

	type runtimePolicyErrorEnvelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Code           string                       `json:"code"`
			Message        string                       `json:"message"`
			AdditionalInfo *runtimePolicyAdditionalInfo `json:"additionalInfo"`
		} `json:"error"`
	}

	var parsed runtimePolicyErrorEnvelope
	if json.Unmarshal(body, &parsed) != nil {
		return nil, false
	}

	code := strings.TrimSpace(respErr.ErrorCode)
	rawMsg := strings.TrimSpace(parsed.Message)
	if strings.TrimSpace(parsed.Code) != "" {
		code = strings.TrimSpace(parsed.Code)
	}
	var policyAssignmentID, policyDefinitionID string
	if parsed.Error != nil {
		if strings.TrimSpace(parsed.Error.Code) != "" {
			code = strings.TrimSpace(parsed.Error.Code)
		}
		if strings.TrimSpace(parsed.Error.Message) != "" {
			rawMsg = strings.TrimSpace(parsed.Error.Message)
		}
		if parsed.Error.AdditionalInfo != nil {
			policyAssignmentID = parsed.Error.AdditionalInfo.assignmentID()
			policyDefinitionID = parsed.Error.AdditionalInfo.definitionID()
		}
	}

	msgLower := strings.ToLower(rawMsg)
	isAgentPolicyDenied := strings.EqualFold(code, "agent_policy_denied") ||
		strings.EqualFold(respErr.ErrorCode, "agent_policy_denied")
	isForbiddenPolicyDenied := (strings.EqualFold(code, "forbidden") ||
		strings.EqualFold(respErr.ErrorCode, "forbidden")) &&
		strings.Contains(msgLower, "noncompliant with policy")
	if !isAgentPolicyDenied && !isForbiddenPolicyDenied {
		return nil, false
	}

	var assignmentIDs []string
	if IsValidPolicyAssignmentID(policyAssignmentID) {
		assignmentIDs = []string{strings.TrimSuffix(strings.TrimSpace(policyAssignmentID), "/")}
	} else {
		for _, m := range runtimePolicyAssignmentRegex.FindAllStringSubmatch(rawMsg, -1) {
			if len(m) >= 2 {
				rawID := strings.TrimSpace(m[1])
				if IsValidPolicyAssignmentID(rawID) {
					id := strings.TrimSuffix(rawID, "/")
					if !slices.ContainsFunc(assignmentIDs, func(existing string) bool {
						return strings.EqualFold(existing, id)
					}) {
						assignmentIDs = append(assignmentIDs, id)
					}
				}
			}
		}
	}
	if len(assignmentIDs) == 0 {
		if !isAgentPolicyDenied {
			return nil, false
		}
		assignmentIDs = []string{""}
	}

	target := inferRuntimeTargetKind(policyDefinitionID, msgLower)
	violations := make([]Violation, 0, len(assignmentIDs))
	for _, id := range assignmentIDs {
		displayName := "Azure Policy assignment"
		normID := ""
		if id != "" {
			normID = NormalizeScope(id)
			if seg := lastScopeSegment(id); seg != "" {
				displayName = seg
			}
		}
		violations = append(violations, Violation{
			Target:       target,
			AssignmentID: normID,
			DisplayName:  displayName,
			PortalURL:    BuildPolicyAssignmentPortalURL(projectResourceID, id),
			Message:      safeRuntimePolicyViolationMessage(displayName, target),
		})
	}
	return violations, true
}

func selectPolicyErrorCode(defaultCode string, violations []Violation) string {
	fallback := strings.TrimSpace(defaultCode)
	if fallback == "" {
		fallback = ferrors.CodeAgentPolicyBlocked
	}
	if len(violations) == 0 {
		return fallback
	}
	firstTarget := violations[0].Target
	for _, v := range violations[1:] {
		if v.Target != firstTarget {
			return fallback
		}
	}
	switch firstTarget {
	case TargetAgentKind:
		return ferrors.CodeAgentKindBlockedByPolicy
	case TargetContainerImage, TargetContainerRegistry:
		return ferrors.CodeContainerImageBlockedByPolicy
	case TargetModel:
		return ferrors.CodeModelBlockedByPolicy
	case TargetTool:
		if fallback == ferrors.CodeToolboxBlockedByPolicy {
			return ferrors.CodeToolboxBlockedByPolicy
		}
		return ferrors.CodeToolBlockedByPolicy
	default:
		return fallback
	}
}

// ViolationsToError converts one or more Azure Policy violations into a structured
// error (*ferrors.Error with CategoryValidation) carrying a target-specific error code
// (or defaultCode when provided/overridden), deduplicated violation messages, and
// Foundry Govern portal links. When violations is empty, it returns nil.
func ViolationsToError(subjectLabel string, defaultCode string, violations []Violation) error {
	if len(violations) == 0 {
		return nil
	}

	code := selectPolicyErrorCode(defaultCode, violations)
	var messages []string
	var links []string
	for _, v := range violations {
		displayName := strings.TrimSpace(v.DisplayName)
		if displayName == "" {
			displayName = "Azure Policy assignment"
		}
		msg := strings.TrimSpace(v.Message)
		if msg == "" {
			if strings.TrimSpace(v.TargetValue) != "" && strings.TrimSpace(string(v.Target)) != "" {
				msg = fmt.Sprintf("Blocked by Azure Policy %q (%s: %s).", displayName, v.Target, v.TargetValue)
			} else {
				msg = fmt.Sprintf("Blocked by Azure Policy %q.", displayName)
			}
		}
		if !slices.Contains(messages, msg) {
			messages = append(messages, msg)
		}
		if portalURL := strings.TrimSpace(v.PortalURL); portalURL != "" {
			linkLine := fmt.Sprintf("  View policy (%s): %s", displayName, portalURL)
			if !slices.Contains(links, linkLine) {
				links = append(links, linkLine)
			}
		}
	}

	prefix := "deployment is blocked by Azure Policy"
	if label := strings.TrimSpace(subjectLabel); label != "" {
		prefix = fmt.Sprintf("%s is blocked by Azure Policy", label)
	}
	fullMessage := fmt.Sprintf("%s: %s", prefix, strings.Join(messages, " "))

	suggestion := "Update the configuration to comply with your organization's Azure Policy " +
		"assignments, or select a different Microsoft Foundry project."
	if len(links) > 0 {
		suggestion += "\n" + strings.Join(links, "\n")
	}

	return ferrors.Validation(code, fullMessage, suggestion)
}
