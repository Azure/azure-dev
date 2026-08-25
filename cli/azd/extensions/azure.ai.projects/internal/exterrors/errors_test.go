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

func TestServiceFromAzure_NestedInsufficientQuota(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(`{
			"error": {
				"code": "InvalidTemplateDeployment",
				"details": [{
					"code": "InsufficientQuota",
					"message": "Cannot create/update/move resource 'ai-account'."
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
