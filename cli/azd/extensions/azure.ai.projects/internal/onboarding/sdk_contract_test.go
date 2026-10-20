// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type contractCredential struct{}

func (contractCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type contractTransport func(*http.Request) (*http.Response, error)

func (t contractTransport) Do(request *http.Request) (*http.Response, error) { return t(request) }

func contractARMOptions(transport contractTransport) *arm.ClientOptions {
	return &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
}

func contractARMResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
