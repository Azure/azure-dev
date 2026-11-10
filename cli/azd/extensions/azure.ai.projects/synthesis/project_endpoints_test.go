// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

type endpointTestCredential struct{}

func (endpointTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type endpointTestTransport func(*http.Request) (*http.Response, error)

func (f endpointTestTransport) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestLookupFoundryProjectEndpoints(t *testing.T) {
	t.Parallel()

	const customEndpoint = "https://custom-dns.services.ai.azure.com/api/projects/proj"
	for _, tt := range []struct {
		name, projectProperties, accountProperties, endpoint, openAI, accountName string
	}{
		{
			name:              "advertised endpoint and custom subdomain",
			projectProperties: `{"endpoints":{"AI Foundry API":"` + customEndpoint + `/"}}`,
			accountProperties: `{"customSubDomainName":"custom-dns"}`,
			endpoint:          customEndpoint, openAI: "https://custom-dns.openai.azure.com/",
		},
		{
			name:              "advertised endpoint without account subdomain",
			projectProperties: `{"endpoints":{"AI Foundry API":"` + customEndpoint + `"}}`,
			accountProperties: `{}`,
			endpoint:          customEndpoint, openAI: "https://custom-dns.openai.azure.com/",
		},
		{
			name:              "missing endpoint uses account subdomain",
			projectProperties: `{"endpoints":{}}`, accountProperties: `{"customSubDomainName":"custom-dns"}`,
			endpoint: customEndpoint, openAI: "https://custom-dns.openai.azure.com/",
		},
		{
			name:              "advertised endpoint does not require a DNS-compatible resource name",
			projectProperties: `{"endpoints":{"AI Foundry API":"` + customEndpoint + `"}}`,
			accountProperties: `{}`, accountName: strings.Repeat("a", 64),
			endpoint: customEndpoint, openAI: "https://custom-dns.openai.azure.com/",
		},
		{
			name:              "nil properties use resource names",
			projectProperties: `null`, accountProperties: `null`,
			endpoint: "https://acct.services.ai.azure.com/api/projects/proj", openAI: "https://acct.openai.azure.com/",
		},
		{
			name:              "nil endpoints and subdomain use resource names",
			projectProperties: `{"endpoints":null}`, accountProperties: `{"customSubDomainName":null}`,
			endpoint: "https://acct.services.ai.azure.com/api/projects/proj", openAI: "https://acct.openai.azure.com/",
		},
		{
			name:              "nil endpoint entry uses resource names",
			projectProperties: `{"endpoints":{"AI Foundry API":null}}`, accountProperties: `{}`,
			endpoint: "https://acct.services.ai.azure.com/api/projects/proj", openAI: "https://acct.openai.azure.com/",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			projectID := testProjectID
			accountName := "acct"
			if tt.accountName != "" {
				accountName = tt.accountName
				projectID = strings.Replace(projectID, "/accounts/acct/", "/accounts/"+accountName+"/", 1)
			}
			id, err := arm.ParseResourceID(projectID)
			require.NoError(t, err)
			var paths []string
			options := &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: endpointTestTransport(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, "management.azure.com", request.URL.Host)
					paths = append(paths, request.URL.Path)
					properties := tt.projectProperties
					if request.URL.Path == id.Parent.String() {
						properties = tt.accountProperties
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"properties":` + properties + `}`)), Request: request,
					}, nil
				}),
			}}
			metadata, err := LookupFoundryProject(t.Context(), id, endpointTestCredential{}, options)
			require.NoError(t, err)
			require.Equal(t, []string{id.String(), id.Parent.String()}, paths)
			require.Equal(t, tt.endpoint, metadata.Endpoint)
			require.Equal(t, tt.openAI, metadata.OpenAIEndpoint)
			require.Equal(t, accountName, id.Parent.Name)
		})
	}
}

func TestLookupFoundryProjectDoesNotHideFailures(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, advertised, subdomain string
		failAccount                 bool
		serviceError                error
		status                      int
	}{
		{name: "invalid advertised host", advertised: "https://example.com/api/projects/proj"},
		{name: "invalid advertised scheme", advertised: "http://acct.services.ai.azure.com/api/projects/proj"},
		{name: "wrong project", advertised: "https://acct.services.ai.azure.com/api/projects/other"},
		{name: "invalid subdomain", subdomain: "custom.example.com"},
		{name: "project transport failure", serviceError: errors.New("project request failed")},
		{name: "account transport failure", failAccount: true, serviceError: errors.New("account request failed")},
		{name: "project HTTP forbidden", status: http.StatusForbidden},
		{name: "account HTTP not found", failAccount: true, status: http.StatusNotFound},
		{name: "cancellation", serviceError: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, err := arm.ParseResourceID(testProjectID)
			require.NoError(t, err)
			options := &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: endpointTestTransport(func(request *http.Request) (*http.Response, error) {
					account := request.URL.Path == id.Parent.String()
					if tt.serviceError != nil && account == tt.failAccount {
						return nil, tt.serviceError
					}
					if tt.status != 0 && account == tt.failAccount {
						return &http.Response{
							StatusCode: tt.status, Header: http.Header{"Content-Type": {"application/json"}},
							Body: io.NopCloser(strings.NewReader(`{"error":{"code":"AccessDenied"}}`)), Request: request,
						}, nil
					}
					body := `{"properties":{}}`
					if account && tt.subdomain != "" {
						body = `{"properties":{"customSubDomainName":"` + tt.subdomain + `"}}`
					} else if !account && tt.advertised != "" {
						body = `{"properties":{"endpoints":{"AI Foundry API":"` + tt.advertised + `"}}}`
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(body)), Request: request,
					}, nil
				}),
			}}
			metadata, err := LookupFoundryProject(t.Context(), id, endpointTestCredential{}, options)
			require.Error(t, err)
			require.Nil(t, metadata)
			if tt.serviceError != nil {
				require.ErrorIs(t, err, tt.serviceError)
			}
			if tt.status != 0 {
				response, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				require.Equal(t, tt.status, response.StatusCode)
			}
		})
	}
}

func TestValidateFoundryProjectEndpoint(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"", "https://example.com/api/projects/proj", "http://acct.services.ai.azure.com/api/projects/proj",
		"https://acct.services.ai.azure.com:443/api/projects/proj", "//acct.services.ai.azure.com/api/projects/proj",
		"https://acct.services.ai.azure.com:/api/projects/proj",
		"https://acct.services.ai.azure.com.evil.test/api/projects/proj", "https://.services.ai.azure.com/api/projects/proj",
		"https://acct.services.ai.azure.com/api/projects/", "https://acct.services.ai.azure.com/api/projects/proj/child",
		"https://acct.services.ai.azure.com/api/projects/..", "https://acct.services.ai.azure.com/api/projects/a%2Fb",
		"https://user:password@acct.services.ai.azure.com/api/projects/proj?sig=secret#fragment",
		"https://acct.services.ai.azure.com/api/projects/proj?sig=secret",
		"https://acct.services.ai.azure.com/api/projects/proj#secret",
		"https://user:password@acct.services.ai.azure.com/%zz",
	} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			normalized, err := ValidateFoundryProjectEndpoint(endpoint)
			require.Error(t, err)
			require.Empty(t, normalized)
			for _, sensitive := range []string{"user:", "password", "sig=", "secret", "#fragment"} {
				require.NotContains(t, err.Error(), sensitive)
			}
		})
	}
	normalized, err := ValidateFoundryProjectEndpoint(" HTTPS://Custom-DNS.services.ai.azure.com/api/projects/proj/ ")
	require.NoError(t, err)
	require.Equal(t, "https://custom-dns.services.ai.azure.com/api/projects/proj", normalized)
}
