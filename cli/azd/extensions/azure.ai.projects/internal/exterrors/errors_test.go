// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceFromAzure_NonCognitiveServicesQuotaUsesGenericGuidance(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(`{
			"error": {
				"code": "InvalidTemplateDeployment",
				"details": [{
					"code": "InsufficientQuota",
					"target": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/registry",
					"message": "Insufficient quota."
				}]
			}
		}`)),
		Request: &http.Request{
			Host: "management.azure.com",
			URL:  &url.URL{Scheme: "https", Host: "management.azure.com"},
		},
	}
	err := &azcore.ResponseError{
		ErrorCode:   "InvalidTemplateDeployment",
		StatusCode:  http.StatusBadRequest,
		RawResponse: response,
	}

	result := ServiceFromAzure(err, OpArmDeploymentCreate)

	serviceErr, ok := errors.AsType[*azdext.ServiceError](result)
	require.True(t, ok)
	assert.Contains(t, serviceErr.Suggestion, "affected resource provider")
	assert.NotContains(t, serviceErr.Suggestion, "az cognitiveservices usage list")
	assert.NotContains(t, serviceErr.Suggestion, "AZURE_AI_PROJECT_ID")
	assert.NotContains(t, serviceErr.Suggestion, "az vm list-usage")
}

func TestServiceFromAzure_CognitiveServicesQuotaUsesFoundryGuidance(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(`{
			"error": {
				"code": "InvalidTemplateDeployment",
				"details": [{
					"code": "InsufficientQuota",
					"target": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai-account",
					"message": "Insufficient quota."
				}]
			}
		}`)),
		Request: &http.Request{
			Host: "management.azure.com",
			URL:  &url.URL{Scheme: "https", Host: "management.azure.com"},
		},
	}
	err := &azcore.ResponseError{
		ErrorCode:   "InvalidTemplateDeployment",
		StatusCode:  http.StatusBadRequest,
		RawResponse: response,
	}

	result := ServiceFromAzure(err, OpArmDeploymentCreate)

	serviceErr, ok := errors.AsType[*azdext.ServiceError](result)
	require.True(t, ok)
	assert.Contains(t, serviceErr.Suggestion, "az cognitiveservices usage list --location <region>")
	assert.Contains(t, serviceErr.Suggestion, "AZURE_AI_PROJECT_ID")
	assert.NotContains(t, serviceErr.Suggestion, "az vm list-usage")
}

func TestServiceFromAzure_CognitiveServicesChildQuotaUsesGenericGuidance(t *testing.T) {
	target := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/ai-account/deployments/model"
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(`{
			"error": {
				"code": "InvalidTemplateDeployment",
				"details": [{
					"code": "InsufficientQuota",
					"target": "` + target + `",
					"message": "Insufficient quota."
				}]
			}
		}`)),
		Request: &http.Request{
			Host: "management.azure.com",
			URL:  &url.URL{Scheme: "https", Host: "management.azure.com"},
		},
	}
	err := &azcore.ResponseError{
		ErrorCode:   "InvalidTemplateDeployment",
		StatusCode:  http.StatusBadRequest,
		RawResponse: response,
	}

	result := ServiceFromAzure(err, OpArmDeploymentCreate)

	serviceErr, ok := errors.AsType[*azdext.ServiceError](result)
	require.True(t, ok)
	assert.Contains(t, serviceErr.Suggestion, "affected resource provider")
	assert.NotContains(t, serviceErr.Suggestion, "az cognitiveservices usage list")
	assert.NotContains(t, serviceErr.Suggestion, "AZURE_AI_PROJECT_ID")
}

func TestServiceFromAzure_UnrelatedErrorHasNoSuggestion(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"InvalidTemplate"}}`)),
		Request: &http.Request{
			URL: &url.URL{Scheme: "https", Host: "management.azure.com"},
		},
	}
	err := &azcore.ResponseError{
		ErrorCode:   "InvalidTemplate",
		StatusCode:  http.StatusBadRequest,
		RawResponse: response,
	}

	result := ServiceFromAzure(err, OpArmDeploymentCreate)

	serviceErr, ok := errors.AsType[*azdext.ServiceError](result)
	require.True(t, ok)
	assert.Empty(t, serviceErr.Suggestion)
}

func TestServiceFromAzure_QuotaCodeInResourceNameHasNoSuggestion(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(`{
			"error": {
				"code": "InvalidTemplate",
				"target": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/InsufficientQuota",
				"message": "The resource name is not valid."
			}
		}`)),
		Request: &http.Request{
			URL: &url.URL{
				Scheme: "https",
				Host:   "management.azure.com",
				Path:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/InsufficientQuota",
			},
		},
	}
	err := &azcore.ResponseError{
		ErrorCode:   "InvalidTemplate",
		StatusCode:  http.StatusBadRequest,
		RawResponse: response,
	}

	result := ServiceFromAzure(err, OpArmDeploymentCreate)

	serviceErr, ok := errors.AsType[*azdext.ServiceError](result)
	require.True(t, ok)
	assert.Empty(t, serviceErr.Suggestion)
}
