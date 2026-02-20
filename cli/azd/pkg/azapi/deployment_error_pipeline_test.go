// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"testing"

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
		context.Background(),
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
		context.Background(),
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
		context.Background(),
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
		context.Background(),
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
		context.Background(),
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
		context.Background(),
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
		context.Background(),
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
