// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured error helpers for the azure.ai.projects extension.
//
// This package mirrors a subset of azure.ai.agents/internal/exterrors so the
// two extensions can be consolidated into a shared package in a follow-up.
//
// Use plain Go errors until the current code can confidently choose a final
// category, code, and suggestion. At that point, create a structured error
// with one of the helpers in this package.
//
// Once an error is structured, return it unchanged. Avoid wrapping a structured
// error with [fmt.Errorf] and %w for extra context: azd serializes the
// structured error's own message and metadata, not the outer wrapper text.
package exterrors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	cognitiveServicesAccountsResourceType = "microsoft.cognitiveservices/accounts"
	insufficientQuotaCode                 = "InsufficientQuota"
	requestDisallowedByPolicyCode         = "RequestDisallowedByPolicy"
	policyViolationCode                   = "PolicyViolation"
)

var armPolicyAssignmentIDPattern = regexp.MustCompile(
	`(?i)(?:/subscriptions/[^/?#\\\s"':,\])}]+(?:/[^/?#\\\s"':,\])}]+)*|` +
		`/providers/Microsoft\.Management/managementGroups/[^/?#\\\s"':,\])}]+)` +
		`/providers/Microsoft\.Authorization/policyAssignments/[^/?#\\\s"':,\])}]+`,
)

// Validation returns a validation [azdext.LocalError] for user-input or
// configuration errors.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// Dependency returns a dependency [azdext.LocalError] for missing resources or
// services.
func Dependency(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: suggestion,
	}
}

// Compatibility returns a compatibility error for version mismatches.
func Compatibility(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryCompatibility,
		Suggestion: suggestion,
	}
}

// Auth returns an authentication or authorization error.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// User returns a user-action error without a suggestion.
func User(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryUser,
	}
}

// Internal returns an unexpected local failure.
func Internal(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

// ServiceFromAzure classifies an Azure SDK response error.
func ServiceFromAzure(err error, operation string) error {
	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		serviceName := ""
		if responseErr.RawResponse != nil &&
			responseErr.RawResponse.Request != nil {
			serviceName = responseErr.RawResponse.Request.Host
		}
		code := responseErr.ErrorCode
		if code == "" {
			code = fmt.Sprintf("%d", responseErr.StatusCode)
		}
		suggestion := quotaSuggestionForResponse(responseErr)
		if suggestion == "" {
			suggestion = policySuggestionForResponse(responseErr)
		}
		return &azdext.ServiceError{
			Message: fmt.Sprintf(
				"%s: %s",
				operation,
				responseErr.Error(),
			),
			ErrorCode:   fmt.Sprintf("%s.%s", operation, code),
			StatusCode:  responseErr.StatusCode,
			ServiceName: serviceName,
			Suggestion:  suggestion,
		}
	}
	if IsCancellation(err) {
		return Cancelled(fmt.Sprintf("%s was cancelled", operation))
	}
	return Internal(
		operation,
		fmt.Sprintf("%s: %s", operation, err.Error()),
	)
}

// CognitiveServicesQuotaSuggestion returns guidance for quota errors returned
// while provisioning a Foundry account, project, or deployment.
func CognitiveServicesQuotaSuggestion() string {
	return "Check Cognitive Services usage for the deployment region with " +
		"`az cognitiveservices usage list --location <region>` or request a " +
		"quota increase in the Azure portal. If you want to reuse an existing " +
		"Foundry project, configure its endpoint and set `AZURE_AI_PROJECT_ID` " +
		"to the full project resource ID before retrying (or initialize with " +
		"`azd ai agent init --project-id <full-project-resource-id>`)."
}

// GenericQuotaSuggestion returns generic quota guidance.
func GenericQuotaSuggestion() string {
	return "Check the quota and current usage for the affected resource provider " +
		"in the Azure portal, or request a quota increase."
}

// QuotaSuggestionForARMError returns guidance for an ARM quota error.
func QuotaSuggestionForARMError(err *armdeployments.ErrorResponse) string {
	if !armErrorResponseContainsCode(err, insufficientQuotaCode) {
		return ""
	}
	if armErrorResponseContainsCognitiveServicesQuota(err, "") {
		return CognitiveServicesQuotaSuggestion()
	}
	return GenericQuotaSuggestion()
}

func quotaSuggestionForResponse(responseErr *azcore.ResponseError) string {
	if payload := responseErrorPayload(responseErr); payload != nil {
		if suggestion := QuotaSuggestionForARMError(payload); suggestion != "" {
			return suggestion
		}
	}

	if strings.EqualFold(responseErr.ErrorCode, insufficientQuotaCode) {
		return GenericQuotaSuggestion()
	}

	return ""
}

// PolicySuggestion returns guidance for Azure Policy errors returned while
// provisioning a Foundry account, project, or model deployment.
func PolicySuggestion() string {
	return "Update the model deployment or project configuration to comply with " +
		"your organization's Azure Policy assignments."
}

// PolicySuggestionForARMError returns guidance for an ARM policy violation error.
func PolicySuggestionForARMError(err *armdeployments.ErrorResponse) string {
	if !armErrorResponseContainsCode(err, requestDisallowedByPolicyCode) &&
		!armErrorResponseContainsCode(err, policyViolationCode) {
		return ""
	}
	assignments := extractPolicyAssignmentsFromARMError(err)
	if len(assignments) > 0 {
		return fmt.Sprintf(
			"%s Policy assignment: %s.",
			PolicySuggestion(),
			strings.Join(assignments, ", "),
		)
	}
	return PolicySuggestion()
}

func policySuggestionForResponse(responseErr *azcore.ResponseError) string {
	if responseErr == nil {
		return ""
	}
	if payload := responseErrorPayload(responseErr); payload != nil {
		if suggestion := PolicySuggestionForARMError(payload); suggestion != "" {
			return suggestion
		}
	}
	if strings.EqualFold(responseErr.ErrorCode, requestDisallowedByPolicyCode) ||
		strings.EqualFold(responseErr.ErrorCode, policyViolationCode) {
		return PolicySuggestion()
	}
	return ""
}

func extractPolicyAssignmentsFromARMError(err *armdeployments.ErrorResponse) []string {
	var assignments []string
	addMatch := func(raw string) {
		for _, match := range armPolicyAssignmentIDPattern.FindAllString(raw, -1) {
			cleaned := strings.TrimRight(strings.TrimSpace(match), "./")
			if cleaned == "" {
				continue
			}
			if !slices.ContainsFunc(assignments, func(existing string) bool {
				return strings.EqualFold(existing, cleaned)
			}) {
				assignments = append(assignments, cleaned)
			}
		}
	}
	var walk func(node *armdeployments.ErrorResponse)
	walk = func(node *armdeployments.ErrorResponse) {
		if node == nil {
			return
		}
		for _, info := range node.AdditionalInfo {
			if info == nil || info.Info == nil {
				continue
			}
			if rawBytes, marshalErr := json.Marshal(info.Info); marshalErr == nil {
				addMatch(string(rawBytes))
			}
		}
		if node.Message != nil {
			addMatch(*node.Message)
		}
		for _, detail := range node.Details {
			walk(detail)
		}
	}
	walk(err)
	return assignments
}

func responseErrorPayload(responseErr *azcore.ResponseError) *armdeployments.ErrorResponse {
	if responseErr.RawResponse == nil || responseErr.RawResponse.Body == nil {
		return nil
	}

	bodyReader := responseErr.RawResponse.Body
	body, err := io.ReadAll(bodyReader)
	_ = bodyReader.Close()
	responseErr.RawResponse.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return nil
	}

	var envelope struct {
		Error      *armdeployments.ErrorResponse `json:"error"`
		Properties *struct {
			Error *armdeployments.ErrorResponse `json:"error"`
		} `json:"properties"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error != nil {
			return envelope.Error
		}
		if envelope.Properties != nil && envelope.Properties.Error != nil {
			return envelope.Properties.Error
		}
	}

	var payload armdeployments.ErrorResponse
	if json.Unmarshal(body, &payload) == nil && payload.Code != nil {
		return &payload
	}

	return nil
}

func armErrorResponseContainsCode(err *armdeployments.ErrorResponse, code string) bool {
	if err == nil {
		return false
	}
	if err.Code != nil && strings.EqualFold(*err.Code, code) {
		return true
	}
	for _, detail := range err.Details {
		if armErrorResponseContainsCode(detail, code) {
			return true
		}
	}
	return false
}

func armErrorResponseContainsCognitiveServicesQuota(
	err *armdeployments.ErrorResponse,
	inheritedTarget string,
) bool {
	if err == nil {
		return false
	}

	target := inheritedTarget
	if err.Target != nil {
		target = *err.Target
	}
	if err.Code != nil && strings.EqualFold(*err.Code, insufficientQuotaCode) &&
		(cognitiveServicesResource(target) ||
			(err.Message != nil && cognitiveServicesResource(*err.Message))) {
		return true
	}
	for _, detail := range err.Details {
		if armErrorResponseContainsCognitiveServicesQuota(detail, target) {
			return true
		}
	}
	return false
}

func cognitiveServicesResource(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), "'\"")
	resourceType, err := arm.ParseResourceType(value)
	return err == nil &&
		strings.EqualFold(resourceType.String(), cognitiveServicesAccountsResourceType)
}

// IsCancellation reports whether an operation was cancelled.
func IsCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if grpcStatus, ok := status.FromError(err); ok {
		return grpcStatus.Code() == codes.Canceled
	}
	return false
}

// IsPromptRequired detects a no-prompt host failure.
func IsPromptRequired(err error) bool {
	if err == nil {
		return false
	}
	if grpcStatus, ok := status.FromError(err); ok {
		return strings.Contains(
			strings.ToLower(grpcStatus.Message()),
			"prompt required",
		)
	}
	return strings.Contains(
		strings.ToLower(err.Error()),
		"prompt required",
	)
}

// Cancelled returns a structured user cancellation.
func Cancelled(message string) error {
	return User(CodeCancelled, message)
}
