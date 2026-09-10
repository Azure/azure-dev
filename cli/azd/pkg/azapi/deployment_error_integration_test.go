// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipeline_DeploymentErrorLine_DeepNested(t *testing.T) {
	// Simulates a real ARM error:
	// DeploymentFailed > ResourceDeploymentFailure > FlagMustBeSetForRestore
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed",`+
			`"message":"At least one resource deployment operation failed.",`+
			`"details":[{"code":"ResourceDeploymentFailure",`+
			`"message":"The resource operation completed with terminal provisioning state 'Failed'.",`+
			`"details":[{"code":"FlagMustBeSetForRestore",`+
			`"message":"Existing soft-deleted vault with the same name."}]}]}}`,
		DeploymentOperationDeploy,
	)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		deployErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType:  "DeploymentErrorLine",
				Properties: map[string]string{"Code": "FlagMustBeSetForRestore"},
				Message:    "A soft-deleted resource is blocking deployment.",
				Suggestion: "Run 'azd down --purge' to permanently remove it.",
			},
		},
	)

	require.NotNil(t, result,
		"Should match FlagMustBeSetForRestore 3 levels deep")
	assert.Equal(t, "A soft-deleted resource is blocking deployment.",
		result.Message)
}

func TestPipeline_DeploymentErrorLine_QuotaNestedInDeployment(t *testing.T) {
	// DeploymentFailed > InsufficientQuota
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed",`+
			`"details":[{"code":"InsufficientQuota",`+
			`"message":"Operation results in exceeding approved quota."}]}}`,
		DeploymentOperationDeploy,
	)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		deployErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType:  "DeploymentErrorLine",
				Properties: map[string]string{"Code": "InsufficientQuota"},
				Message:    "Quota insufficient.",
				Suggestion: "Request a quota increase.",
			},
		},
	)

	require.NotNil(t, result,
		"Should match InsufficientQuota nested under DeploymentFailed")
	assert.Equal(t, "Quota insufficient.", result.Message)
}

func TestSuggestions_InsufficientQuota_CognitiveServicesAccount(t *testing.T) {
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota","message":"Cannot create/update/move resource 'ai-account'."}]}}`,
		DeploymentOperationDeploy,
	)
	require.NotNil(t, deployErr.Details)
	require.Len(t, deployErr.Details.Inner, 1)
	require.Len(t, deployErr.Details.Inner[0].Inner, 1)
	deployErr.Details.Inner[0].Inner[0].ResourceType = "Microsoft.CognitiveServices/accounts"

	result := errorhandler.NewErrorHandlerPipeline(nil).Process(t.Context(), deployErr)

	require.NotNil(t, result)
	assert.Contains(t, result.Suggestion, "az cognitiveservices usage list --location <region>")
	assert.NotContains(t, result.Suggestion, "az vm list-usage")
}

func TestSuggestions_InsufficientQuota_GenericDoesNotUseVMUsage(t *testing.T) {
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota","message":"Cannot create/update/move resource 'storage'."}]}}`,
		DeploymentOperationDeploy,
	)

	result := errorhandler.NewErrorHandlerPipeline(nil).Process(t.Context(), deployErr)

	require.NotNil(t, result)
	assert.NotContains(t, result.Suggestion, "az vm list-usage")
	assert.NotContains(t, result.Suggestion, "az cognitiveservices usage list")
	assert.Contains(t, result.Suggestion, "affected resource provider")
}

func TestPipeline_DeploymentErrorLine_ConflictWithKeyword(t *testing.T) {
	// DeploymentFailed > Conflict with "soft-deleted" in message
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed",`+
			`"details":[{"code":"Conflict",`+
			`"message":"A soft-deleted resource is blocking this."}]}}`,
		DeploymentOperationDeploy,
	)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		deployErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType:  "DeploymentErrorLine",
				Regex:      true,
				Properties: map[string]string{"Code": "Conflict"},
				Patterns:   []string{"(?i)soft.?delete"},
				Message:    "Soft-delete conflict.",
				Suggestion: "Purge the resource.",
			},
		},
	)

	require.NotNil(t, result,
		"Should match Conflict + soft-delete keyword in nested error")
	assert.Equal(t, "Soft-delete conflict.", result.Message)
}

func TestPipeline_DeploymentErrorLine_NoMatchWrongCode(t *testing.T) {
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed",`+
			`"details":[{"code":"SomeOtherError",`+
			`"message":"Something else went wrong."}]}}`,
		DeploymentOperationDeploy,
	)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		deployErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType: "DeploymentErrorLine",
				Properties: map[string]string{
					"Code": "FlagMustBeSetForRestore",
				},
				Message:    "Soft-deleted resource.",
				Suggestion: "Purge it.",
			},
		},
	)

	assert.Nil(t, result, "Should not match when code differs")
}

func TestPipeline_DeploymentErrorLine_MultipleRulesFirstWins(t *testing.T) {
	// Error tree has both InsufficientQuota and AuthorizationFailed
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed",`+
			`"details":[`+
			`{"code":"InsufficientQuota","message":"quota exceeded"},`+
			`{"code":"AuthorizationFailed","message":"no permissions"}`+
			`]}}`,
		DeploymentOperationDeploy,
	)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		deployErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType: "DeploymentErrorLine",
				Properties: map[string]string{
					"Code": "InsufficientQuota",
				},
				Message:    "Quota error.",
				Suggestion: "Request increase.",
			},
			{
				ErrorType: "DeploymentErrorLine",
				Properties: map[string]string{
					"Code": "AuthorizationFailed",
				},
				Message:    "Auth error.",
				Suggestion: "Check permissions.",
			},
		},
	)

	require.NotNil(t, result)
	assert.Equal(t, "Quota error.", result.Message,
		"First matching rule should win")
}

func TestPipeline_DeploymentErrorLine_WrappedInStandardError(t *testing.T) {
	// AzureDeploymentError wrapped in fmt.Errorf
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed",`+
			`"details":[{"code":"SkuNotAvailable",`+
			`"message":"The requested size is not available."}]}}`,
		DeploymentOperationDeploy,
	)

	wrappedErr := fmt.Errorf("provisioning failed: %w", deployErr)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		wrappedErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType:  "DeploymentErrorLine",
				Properties: map[string]string{"Code": "SkuNotAvailable"},
				Message:    "SKU not available.",
				Suggestion: "Try a different region.",
			},
		},
	)

	require.NotNil(t, result,
		"Should find DeploymentErrorLine even when wrapped")
	assert.Equal(t, "SKU not available.", result.Message)
}

func TestPipeline_DeploymentErrorLine_FourLevelsDeep(t *testing.T) {
	// 4 levels: DeploymentFailed > ResourceDeploymentFailure >
	//           DeploymentFailed > ValidationError
	deployErr := NewAzureDeploymentError(
		"Deployment Failed",
		`{"error":{"code":"DeploymentFailed",`+
			`"details":[{"code":"ResourceDeploymentFailure",`+
			`"details":[{"code":"DeploymentFailed",`+
			`"details":[{"code":"ValidationError",`+
			`"message":"The template is invalid."}]}]}]}}`,
		DeploymentOperationDeploy,
	)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		deployErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType: "DeploymentErrorLine",
				Properties: map[string]string{
					"Code": "ValidationError",
				},
				Message:    "Template validation failed.",
				Suggestion: "Check your Bicep files.",
			},
		},
	)

	require.NotNil(t, result,
		"Should match ValidationError 4 levels deep")
	assert.Equal(t, "Template validation failed.", result.Message)
}

func TestPipeline_DeploymentErrorLine_LocationNotAvailableForResourceType(t *testing.T) {
	// Real validation error for Static Web Apps in unsupported region
	deployErr := NewAzureDeploymentError(
		"Validation Error Details",
		`{"error":{`+
			`"code":"LocationNotAvailableForResourceType",`+
			`"message":"The provided location 'eastus' is not available `+
			`for resource type 'Microsoft.Web/staticSites'. `+
			`List of available regions for the resource type is `+
			`'westus2,centralus,eastus2,westeurope,eastasia'."}}`,
		DeploymentOperationValidate,
	)

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		t.Context(),
		deployErr,
		[]errorhandler.ErrorSuggestionRule{
			{
				ErrorType: "DeploymentErrorLine",
				Properties: map[string]string{
					"Code": "LocationNotAvailableForResourceType",
				},
				Message:    "Resource not available in region.",
				Suggestion: "Change region.",
			},
		},
	)

	require.NotNil(t, result,
		"Should match LocationNotAvailableForResourceType")
	assert.Equal(t, "Resource not available in region.", result.Message)
}

// A failed what-if returns HTTP 200 with the failure in the payload, so the
// preview path builds its error from an armresources.ErrorResponse rather than
// from an HTTP error. The actionable cause sits two levels deep, under a
// top-level code that getErrorsFromMap does NOT blank out (unlike
// DeploymentFailed), so the pipeline must keep searching past the outer node.
// Regression test for https://github.com/Azure/azure-dev/issues/9011.
func TestPipeline_PreviewErrorResponse_NestedQuota(t *testing.T) {
	const whatIfBody = `{
	  "status": "Failed",
	  "error": {
	    "code": "InvalidTemplateDeployment",
	    "message": "The template deployment 'dev-1' is not valid according to the validation procedure. ` +
		`The following resource provider(s) - 'Microsoft.Storage/storageAccounts' reported preflight ` +
		`validation errors. See inner errors for details.",
	    "details": [{
	      "code": "PreflightValidationCheckFailed",
	      "message": "Preflight validation failed. Please refer to the details for the specific errors.",
	      "details": [{
	        "code": "SubscriptionIsOverQuotaForSku",
	        "target": "stdev1",
	        "message": "Subscription has reached the maximum number of storage accounts allowed in 'eastus'."
	      }]
	    }]
	  }
	}`

	var whatIfResult armresources.WhatIfOperationResult
	require.NoError(t, json.Unmarshal([]byte(whatIfBody), &whatIfResult))
	require.NotNil(t, whatIfResult.Error)

	deployErr := NewAzureDeploymentErrorFromResponse(whatIfResult.Error, DeploymentOperationPreview)

	// Every level of the ARM error tree must be rendered, especially the leaf cause.
	// Asserting the codes in order locks the nesting: a regression that stops
	// descending would drop trailing entries rather than fail a substring check.
	rendered := deployErr.Error()
	var codes []string
	for line := range strings.SplitSeq(strings.TrimSpace(rendered), "\n") {
		if code, _, found := strings.Cut(line, ":"); found {
			codes = append(codes, strings.TrimSpace(code))
		}
	}
	assert.Equal(t, []string{
		"Preview Error Details",
		"InvalidTemplateDeployment",
		"PreflightValidationCheckFailed",
		"SubscriptionIsOverQuotaForSku",
	}, codes)
	assert.Contains(t, rendered, "maximum number of storage accounts")

	// Reproduce the production wrapping chain: provisioning.Manager.Preview
	// followed by wrapProvisionError's default branch.
	wrapped := fmt.Errorf("deployment failed: %w",
		fmt.Errorf("error deploying infrastructure: %w", deployErr))

	// Exercise the real embedded error_suggestions.yaml rules, not inline ones.
	result := errorhandler.NewErrorHandlerPipeline(nil).Process(t.Context(), wrapped)

	require.NotNil(t, result,
		"Should match SubscriptionIsOverQuotaForSku nested under InvalidTemplateDeployment")
	assert.Equal(t, "Your subscription quota for this SKU is exceeded.", result.Message)
	assert.Contains(t, result.Suggestion, "Request a quota increase")
}

// Property matching is a case-insensitive substring check, so the rule keyed on
// "InvalidTemplate" also matches ARM's "InvalidTemplateDeployment". Its advice —
// run 'azd provision --preview' — is correct for a deploy but absurd for a
// preview, which is the command already running. The preview guard rule keys on
// AzureDeploymentError.Operation to suppress it there, and must leave every
// other operation untouched.
func TestSuggestions_PreviewNeverAdvisesRunningPreview(t *testing.T) {
	// Cause with no dedicated rule, so matching falls through to the generic
	// template rules where the bad advice lives.
	const armError = `{
	  "code": "InvalidTemplateDeployment",
	  "message": "The template deployment is not valid. See inner errors for details.",
	  "details": [{
	    "code": "PreflightValidationCheckFailed",
	    "message": "Preflight validation failed.",
	    "details": [{
	      "code": "StorageAccountAlreadyTaken",
	      "message": "The storage account named storage is already taken."
	    }]
	  }]
	}`

	tests := []struct {
		name string
		op   DeploymentOperation
		// wantMessage identifies which rule won, since both rules produce advice.
		wantMessage    string
		wantSelfAdvice bool
	}{
		{
			name:           "preview is suppressed",
			op:             DeploymentOperationPreview,
			wantMessage:    "Azure validation rejected the deployment template.",
			wantSelfAdvice: false,
		},
		{
			// Not self-referential, so the original advice must survive unchanged.
			name:           "deploy keeps the advice",
			op:             DeploymentOperationDeploy,
			wantMessage:    "The deployment template contains errors.",
			wantSelfAdvice: true,
		},
		{
			// Validate runs inside provision, not preview, so the guard must not
			// widen to it just because it is also a non-deploy operation.
			name:           "validate keeps the advice",
			op:             DeploymentOperationValidate,
			wantMessage:    "The deployment template contains errors.",
			wantSelfAdvice: true,
		},
	}

	pipeline := errorhandler.NewErrorHandlerPipeline(nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Preview marshals the ErrorResponse directly; the other paths receive
			// the same tree nested under an "error" key. Mirror both real shapes.
			body := armError
			if tt.op != DeploymentOperationPreview {
				body = `{"error":` + armError + `}`
			}

			deployErr := NewAzureDeploymentError(deploymentErrorTitle(tt.op), body, tt.op)
			wrapped := fmt.Errorf("deployment failed: %w",
				fmt.Errorf("error deploying infrastructure: %w", deployErr))

			result := pipeline.Process(t.Context(), wrapped)

			require.NotNil(t, result, "every operation should still get a suggestion")
			assert.Equal(t, tt.wantMessage, result.Message)

			if tt.wantSelfAdvice {
				assert.Contains(t, result.Suggestion, "azd provision --preview")
			} else {
				assert.NotContains(t, result.Suggestion, "--preview",
					"must not tell a --preview run to run --preview")
			}
		})
	}
}

// The preview guard matches on operation alone, so ordering is the only thing
// keeping it from swallowing every preview failure: it must sit after the
// specific error-code rules, which are evaluated first and win. Locking that
// here because the ordering constraint is invisible at the YAML call site.
// This is the issue #9011 behaviour and must not regress.
func TestSuggestions_PreviewGuardDoesNotMaskSpecificCauses(t *testing.T) {
	const armError = `{
	  "code": "InvalidTemplateDeployment",
	  "message": "The template deployment is not valid. See inner errors for details.",
	  "details": [{
	    "code": "PreflightValidationCheckFailed",
	    "message": "Preflight validation failed.",
	    "details": [{
	      "code": "SubscriptionIsOverQuotaForSku",
	      "message": "Subscription has reached the maximum number of storage accounts."
	    }]
	  }]
	}`

	deployErr := NewAzureDeploymentError(
		deploymentErrorTitle(DeploymentOperationPreview), armError, DeploymentOperationPreview)
	wrapped := fmt.Errorf("deployment failed: %w",
		fmt.Errorf("error deploying infrastructure: %w", deployErr))

	result := errorhandler.NewErrorHandlerPipeline(nil).Process(t.Context(), wrapped)

	require.NotNil(t, result)
	assert.Equal(t, "Your subscription quota for this SKU is exceeded.", result.Message,
		"specific quota rule must outrank the generic preview guard")
}
