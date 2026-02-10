// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

// AI error metadata constants used in gRPC ErrorInfo for AI model/prompt APIs.
const (
	AiErrorDomain = "azd.ai"
)

// AI error reason codes used in gRPC ErrorInfo.Reason.
const (
	AiErrorReasonMissingSubscription  = "AI_MISSING_SUBSCRIPTION"
	AiErrorReasonLocationRequired     = "AI_LOCATION_REQUIRED"
	AiErrorReasonQuotaLocation        = "AI_QUOTA_LOCATION_REQUIRED"
	AiErrorReasonModelNotFound        = "AI_MODEL_NOT_FOUND"
	AiErrorReasonNoModelsMatch        = "AI_NO_MODELS_MATCH"
	AiErrorReasonNoDeploymentMatch    = "AI_NO_DEPLOYMENT_MATCH"
	AiErrorReasonNoValidSkus          = "AI_NO_VALID_SKUS"
	AiErrorReasonNoLocationsWithQuota = "AI_NO_LOCATIONS_WITH_QUOTA"
	AiErrorReasonInvalidCapacity      = "AI_INVALID_CAPACITY"
	AiErrorReasonInteractiveRequired  = "AI_INTERACTIVE_REQUIRED"
)
