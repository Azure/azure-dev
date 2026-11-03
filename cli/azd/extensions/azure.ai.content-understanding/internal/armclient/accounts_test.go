// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package armclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const accountID = "/subscriptions/sub/resourceGroups/group/providers/Microsoft.CognitiveServices/accounts/account"
const listURL = "https://management.azure.com/subscriptions/sub/providers/Microsoft.CognitiveServices/accounts"

type credentialFunc func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f credentialFunc) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return f(ctx, options)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func resolverFor(t *testing.T, handler transportFunc) *Resolver {
	t.Helper()
	resolver, err := NewResolver(credentialFunc(func(
		_ context.Context, options policy.TokenRequestOptions,
	) (azcore.AccessToken, error) {
		assert.Equal(t, []string{cloud.AzurePublic.Services[cloud.ResourceManager].Audience + "/.default"}, options.Scopes)
		return azcore.AccessToken{Token: "arm-test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
	}), transportFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, "management.azure.com", request.URL.Host)
		assert.Equal(t, accountsAPIVersion, request.URL.Query().Get("api-version"))
		assert.Equal(t, "Bearer arm-test-token", request.Header.Get("Authorization"))
		return handler(request)
	}))
	require.NoError(t, err)
	return resolver
}

func response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Request: request,
		Header: http.Header{"Retry-After-Ms": []string{"1"}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

func accountWire(id, name, endpoint string) string {
	raw, _ := json.Marshal(map[string]any{
		"id": id, "name": name, "location": "westus",
		"properties": map[string]any{"endpoint": endpoint},
	})
	return string(raw)
}

func TestAccountSelectorScopeAndValidation(t *testing.T) {
	fallback := config.ResourceScope{SubscriptionID: "ambient-sub", ResourceGroup: "ambient-group"}
	selector, err := ParseSelector(accountID, "", "", fallback)
	require.NoError(t, err)
	assert.Equal(t, "sub", selector.Scope.SubscriptionID)
	assert.Equal(t, "group", selector.Scope.ResourceGroup)
	selector, err = ParseSelector("https://ACCOUNT.services.ai.azure.com/", "explicit-sub", "explicit-group", fallback)
	require.NoError(t, err)
	assert.Equal(t, "https://account.services.ai.azure.com/contentunderstanding", selector.Endpoint)
	assert.Equal(t, "explicit-sub", selector.Scope.SubscriptionID)
	for _, test := range []struct{ resource, subscription, group string }{
		{"", "", ""}, {accountID, "conflicting", ""}, {accountID, "", "conflicting"},
		{"http://account.services.ai.azure.com", "", ""}, {"https://user:secret@account.services.ai.azure.com", "", ""},
		{"https://account.services.ai.azure.com/?key=secret", "", ""}, {"../account", "", ""},
		{"account", "../sub", ""}, {"account", "..", ""}, {"account", "", "."},
		{"account?secret", "", ""}, {accountID + "/keys", "", ""},
	} {
		t.Run(test.resource+test.subscription+test.group, func(t *testing.T) {
			_, err := ParseSelector(test.resource, test.subscription, test.group, fallback)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestAccountResolutionDirectAndEndpointPreferences(t *testing.T) {
	for _, test := range []struct {
		name, resource, subscription, group, properties, expected string
	}{
		{"full ID", accountID, "", "", `{"endpoint":"https://account.services.ai.azure.com"}`,
			"https://account.services.ai.azure.com/contentunderstanding"},
		{"name and group", "ACCOUNT", "sub", "group", `{}`, "https://account.services.ai.azure.com/contentunderstanding"},
		{"preferred", accountID, "", "", `{"endpoint":"https://base.services.ai.azure.com","endpoints":` +
			`{"Content Understanding":"https://cu.services.ai.azure.com",` +
			`"ContentUnderstanding":"https://other.services.ai.azure.com"}}`,
			"https://cu.services.ai.azure.com/contentunderstanding"},
		{"alternate", accountID, "", "", `{"endpoints":{"ContentUnderstanding":"https://alternate.services.ai.azure.com"}}`,
			"https://alternate.services.ai.azure.com/contentunderstanding"},
		{"OpenAI", accountID, "", "", `{"endpoints":{"OpenAI":"https://openai.services.ai.azure.com"}}`,
			"https://openai.services.ai.azure.com/contentunderstanding"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			resolver := resolverFor(t, func(request *http.Request) (*http.Response, error) {
				calls++
				assert.True(t, strings.EqualFold(accountID, request.URL.Path))
				return response(request, 200, `{"id":"`+accountID+`","name":"account","location":"westus","properties":`+
					test.properties+`}`), nil
			})
			got, err := resolver.Resolve(t.Context(), test.resource, test.subscription, test.group, config.ResourceScope{})
			require.NoError(t, err)
			assert.Equal(t, Account{ID: accountID, Endpoint: test.expected, Region: "westus"}, got)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestAccountResolutionPagingFilteringAndAmbiguity(t *testing.T) {
	for _, mode := range []string{"endpoint", "name", "group filter", "ambiguous", "missing"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			resolver := resolverFor(t, func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "/subscriptions/sub/providers/Microsoft.CognitiveServices/accounts", request.URL.Path)
				if calls == 1 {
					other := accountWire(
						strings.Replace(accountID, "/group/", "/other/", 1), "account",
						"https://other.services.ai.azure.com")
					return response(request, 200, `{"value":[`+other+`],"nextLink":"`+listURL+
						`?api-version=`+accountsAPIVersion+`&$skiptoken=next"}`), nil
				}
				account := accountWire(accountID, "account", "https://account.services.ai.azure.com")
				return response(request, 200, `{"value":[`+account+`]}`), nil
			})
			resource, group := "account", ""
			if mode == "endpoint" || mode == "group filter" {
				resource = "https://account.services.ai.azure.com"
			}
			if mode == "group filter" {
				group = "group"
			}
			if mode == "missing" {
				resource = "absent"
			}
			if mode == "name" {
				resource = "https://other.services.ai.azure.com"
			}
			got, err := resolver.Resolve(t.Context(), resource, "sub", group, config.ResourceScope{})
			switch mode {
			case "ambiguous":
				require.Error(t, err)
				domain, ok := errors.AsType[*cuclient.Error](err)
				require.True(t, ok)
				assert.Equal(t, "ambiguous_account", domain.Code)
			case "missing":
				require.Error(t, err)
				domain, ok := errors.AsType[*cuclient.Error](err)
				require.True(t, ok)
				assert.Equal(t, cuclient.ErrorNotFound, domain.Kind)
			default:
				require.NoError(t, err)
				assert.Contains(t, got.Endpoint, ".services.ai.azure.com/contentunderstanding")
			}
			assert.Equal(t, 2, calls)
		})
	}
}

func TestAccountResolutionRejectsUnsafePaging(t *testing.T) {
	for _, next := range []string{
		"https://attacker.example/?secret", "http://management.azure.com/path",
		listURL + "?api-version=wrong", listURL + "?api-version=" + accountsAPIVersion + "&api-version=wrong",
		strings.Replace(listURL, "/sub/", "/another/", 1) + "?api-version=" + accountsAPIVersion,
		listURL + "/../accounts?api-version=" + accountsAPIVersion,
		listURL + "?api-version=" + accountsAPIVersion + "#fragment",
		listURL + "?api-version=" + accountsAPIVersion,
	} {
		t.Run(next, func(t *testing.T) {
			calls := 0
			resolver := resolverFor(t, func(request *http.Request) (*http.Response, error) {
				calls++
				body, err := json.Marshal(map[string]any{"value": []any{}, "nextLink": next})
				require.NoError(t, err)
				return response(request, 200, string(body)), nil
			})
			_, err := resolver.Resolve(t.Context(), "account", "sub", "", config.ResourceScope{})
			require.Error(t, err)
			assert.Equal(t, 1, calls)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestAccountResolutionMalformedAndServiceFailures(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"value":null}`, `{"value":[null]}`, `{"value":[{"id":"bad"}]}`,
		`{"value":[{"id":"` + accountID + `","name":"wrong","location":"westus"}]}`,
		`{"value":[{"id":"` + accountID + `","name":"account","location":""}]}`,
		`{"value":[{"id":"` + accountID + `","name":"account","location":"westus",` +
			`"properties":{"endpoint":"https://account.services.ai.azure.com?secret"}}]}`,
		`{"value":[` + accountWire(strings.Replace(accountID, "/sub/", "/other-sub/", 1), "account", "") + `]}`,
	} {
		t.Run(body, func(t *testing.T) {
			resolver := resolverFor(t, func(request *http.Request) (*http.Response, error) {
				return response(request, 200, body), nil
			})
			_, err := resolver.Resolve(t.Context(), "account", "sub", "", config.ResourceScope{})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
	for statusCode, kind := range map[int]cuclient.ErrorKind{
		401: cuclient.ErrorAuthentication, 403: cuclient.ErrorAuthentication, 404: cuclient.ErrorNotFound,
		429: cuclient.ErrorThrottled, 500: cuclient.ErrorService, 302: cuclient.ErrorService,
	} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			resolver := resolverFor(t, func(request *http.Request) (*http.Response, error) {
				return response(request, statusCode, "private backend secret"), nil
			})
			_, err := resolver.Resolve(t.Context(), "account", "sub", "", config.ResourceScope{})
			require.Error(t, err)
			domain, ok := errors.AsType[*cuclient.Error](err)
			require.True(t, ok)
			assert.Equal(t, kind, domain.Kind)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestAccountResolutionCancellationCredentialAndScopeFailures(t *testing.T) {
	_, err := NewResolver(nil, nil)
	require.Error(t, err)
	resolver := resolverFor(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected request")
		return nil, nil
	})
	_, err = resolver.Resolve(t.Context(), "account", "", "", config.ResourceScope{})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = resolver.Resolve(ctx, "account", "sub", "", config.ResourceScope{})
	require.ErrorIs(t, err, context.Canceled)
	resolver, err = NewResolver(credentialFunc(func(
		context.Context, policy.TokenRequestOptions,
	) (azcore.AccessToken, error) {
		return azcore.AccessToken{}, errors.New("private credential secret")
	}), transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport must not be called after credential failure")
		return nil, nil
	}))
	require.NoError(t, err)
	_, err = resolver.Resolve(t.Context(), "account", "sub", "", config.ResourceScope{})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
	domain, ok := errors.AsType[*cuclient.Error](err)
	require.True(t, ok)
	assert.Equal(t, cuclient.ErrorAuthentication, domain.Kind)
}

type failingBody struct{ closed bool }

func (b *failingBody) Read([]byte) (int, error) { return 0, errors.New("private body secret") }
func (b *failingBody) Close() error             { b.closed = true; return nil }

func TestAccountDiscoveryResponseBoundsAndCancellation(t *testing.T) {
	for _, mode := range []string{"oversized", "body failure", "cancel during body", "transport failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			body := &failingBody{}
			resolver := resolverFor(t, func(request *http.Request) (*http.Response, error) {
				if mode == "transport failure" {
					return nil, context.DeadlineExceeded
				}
				result := response(request, 200, strings.Repeat(" ", responseLimit+2))
				if mode == "body failure" || mode == "cancel during body" {
					result.Body = body
				}
				if mode == "cancel during body" {
					cancel()
				}
				return result, nil
			})
			_, err := resolver.Resolve(ctx, "account", "sub", "", config.ResourceScope{})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret")
			if mode == "body failure" || mode == "cancel during body" {
				assert.True(t, body.closed)
			}
			if mode == "cancel during body" {
				require.ErrorIs(t, err, context.Canceled)
			}
			if mode == "transport failure" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
	resolver, err := NewResolver(credentialFunc(func(
		context.Context, policy.TokenRequestOptions,
	) (azcore.AccessToken, error) {
		return azcore.AccessToken{}, context.Canceled
	}), nil)
	require.NoError(t, err)
	_, err = resolver.Resolve(t.Context(), "account", "sub", "", config.ResourceScope{})
	require.ErrorIs(t, err, context.Canceled)
}
