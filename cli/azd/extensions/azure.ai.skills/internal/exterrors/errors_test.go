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
	"github.com/stretchr/testify/require"
)

func TestValidation_BuildsLocalError(t *testing.T) {
	err := Validation("code-x", "boom", "do y")
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, "code-x", le.Code)
	require.Equal(t, "boom", le.Message)
	require.Equal(t, "do y", le.Suggestion)
	require.Equal(t, azdext.LocalErrorCategoryValidation, le.Category)
}

func TestDependency_BuildsLocalError(t *testing.T) {
	err := Dependency("code-y", "missing dep", "install z")
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, "code-y", le.Code)
	require.Equal(t, azdext.LocalErrorCategoryDependency, le.Category)
}

func TestAuth_BuildsLocalError(t *testing.T) {
	err := Auth("code-z", "unauthorized", "run azd auth login")
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, "code-z", le.Code)
	require.Equal(t, azdext.LocalErrorCategoryAuth, le.Category)
}

func TestServiceFromAzure_WrapsResponseError(t *testing.T) {
	reqURL, err := url.Parse("https://example.com/skills/my-skill")
	require.NoError(t, err)
	respErr := &azcore.ResponseError{
		ErrorCode:  "NotFound",
		StatusCode: http.StatusNotFound,
		RawResponse: &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    &http.Request{Method: http.MethodGet, URL: reqURL},
		},
	}
	wrapped := ServiceFromAzure(respErr, "OpGetSkill")
	var se *azdext.ServiceError
	require.True(t, errors.As(wrapped, &se))
	require.Equal(t, "NotFound", se.ErrorCode)
	require.Equal(t, http.StatusNotFound, se.StatusCode)
	require.Equal(t, "OpGetSkill", se.ServiceName)
}

func TestServiceFromAzure_PassThroughForNonAzcoreError(t *testing.T) {
	original := errors.New("network down")
	err := ServiceFromAzure(original, "OpAny")
	require.Same(t, original, err, "non-azcore errors must propagate unchanged")
}

func TestServiceFromAzure_NilError(t *testing.T) {
	require.NoError(t, ServiceFromAzure(nil, "OpAny"))
}
