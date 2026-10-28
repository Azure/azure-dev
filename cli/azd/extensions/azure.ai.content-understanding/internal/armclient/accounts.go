// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package armclient provides ARM account discovery and scoped model management for CU workflows.
package armclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const accountsAPIVersion = "2024-10-01"
const responseLimit = 8 * 1024 * 1024

var scopeSegment = regexp.MustCompile(`^[a-zA-Z0-9_.()-]{1,128}$`)

type Account struct {
	ID       string
	Endpoint string
	Region   string
}

type Selector struct {
	Scope    config.ResourceScope
	Endpoint string
}

func ParseSelector(resource, subscription, group string, fallback config.ResourceScope) (Selector, error) {
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return Selector{}, invalid("missing_resource_selector",
			"Specify an account name, ARM account ID, or HTTPS endpoint.")
	}
	var endpoint string
	if strings.Contains(resource, "://") {
		var err error
		endpoint, err = cuclient.NormalizeEndpoint(resource)
		if err != nil {
			return Selector{}, err
		}
		resource = ""
	}
	scope, err := config.ResolveResourceScope(resource, subscription, group, fallback)
	if err != nil {
		return Selector{}, err
	}
	for _, part := range []string{scope.SubscriptionID, scope.ResourceGroup, scope.ResourceName} {
		if part == "." || part == ".." || (part != "" && !scopeSegment.MatchString(part)) {
			return Selector{}, invalid("invalid_resource_scope",
				"Account names and scope selectors must be valid Azure segments.")
		}
	}
	return Selector{Scope: scope, Endpoint: endpoint}, nil
}

type Resolver struct {
	client *arm.Client
}

func NewResolver(credential azcore.TokenCredential, transport policy.Transporter) (*Resolver, error) {
	if credential == nil {
		return nil, invalid("missing_credential", "ARM discovery requires an azd-backed Entra credential.")
	}
	if transport == nil {
		transport = &http.Client{
			Timeout: 2 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	client, err := arm.NewClient("azd-cu-arm", "v0.0.1", discoveryCredential{inner: credential}, &arm.ClientOptions{
		DisableRPRegistration: true,
		ClientOptions: policy.ClientOptions{
			Transport: boundedTransport{inner: transport},
			Retry:     policy.RetryOptions{MaxRetries: 2, TryTimeout: 2 * time.Minute},
		},
	})
	if err != nil {
		return nil, failure("arm_client_failed", "The ARM discovery client could not be initialized.", err)
	}
	return &Resolver{client: client}, nil
}

func (r *Resolver) Resolve(
	ctx context.Context, resource, subscription, group string, fallback config.ResourceScope,
) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	selector, err := ParseSelector(resource, subscription, group, fallback)
	if err != nil {
		return Account{}, err
	}
	if selector.Scope.SubscriptionID == "" {
		return Account{}, invalid("missing_subscription",
			"ARM discovery requires an explicit or selected-environment subscription.")
	}
	path := "/subscriptions/" + selector.Scope.SubscriptionID + "/providers/Microsoft.CognitiveServices/accounts"
	if selector.Scope.ResourceID != "" {
		path = selector.Scope.ResourceID
	} else if selector.Scope.ResourceGroup != "" && selector.Scope.ResourceName != "" {
		path = "/subscriptions/" + selector.Scope.SubscriptionID + "/resourceGroups/" + selector.Scope.ResourceGroup +
			"/providers/Microsoft.CognitiveServices/accounts/" + selector.Scope.ResourceName
	}
	direct := selector.Scope.ResourceID != "" || (selector.Scope.ResourceGroup != "" && selector.Scope.ResourceName != "")
	target := r.client.Endpoint() + path + "?api-version=" + accountsAPIVersion
	seen := map[string]bool{}
	var matches []Account
	for target != "" {
		if seen[target] || len(seen) >= 1000 {
			return Account{}, failure("invalid_arm_paging", "ARM account discovery returned invalid pagination.", nil)
		}
		seen[target] = true
		data, err := r.get(ctx, target)
		if err != nil {
			return Account{}, err
		}
		var entries []json.RawMessage
		var next string
		if direct {
			entries = []json.RawMessage{data}
		} else {
			var page struct {
				Value    []json.RawMessage `json:"value"`
				NextLink string            `json:"nextLink"`
			}
			if json.Unmarshal(data, &page) != nil || page.Value == nil {
				return Account{}, failure("invalid_arm_response", "ARM account discovery returned an invalid response.", nil)
			}
			entries, next = page.Value, page.NextLink
		}
		for _, raw := range entries {
			account, scope, err := parseAccount(raw)
			if err != nil {
				return Account{}, err
			}
			if !strings.EqualFold(scope.SubscriptionID, selector.Scope.SubscriptionID) {
				return Account{}, failure("invalid_arm_scope",
					"ARM discovery returned an account outside the subscription.", nil)
			}
			if selector.Scope.ResourceGroup != "" && !strings.EqualFold(scope.ResourceGroup, selector.Scope.ResourceGroup) {
				continue
			}
			if selector.Scope.ResourceName != "" && !strings.EqualFold(scope.ResourceName, selector.Scope.ResourceName) {
				continue
			}
			if selector.Endpoint != "" && !strings.EqualFold(account.Endpoint, selector.Endpoint) {
				continue
			}
			if selector.Scope.ResourceID != "" && !strings.EqualFold(account.ID, selector.Scope.ResourceID) {
				continue
			}
			matches = append(matches, account)
		}
		if next == "" {
			break
		}
		target, err = r.continuation(next, path)
		if err != nil {
			return Account{}, err
		}
	}
	if len(matches) == 0 {
		return Account{}, &cuclient.Error{
			Kind: cuclient.ErrorNotFound, Code: "account_not_found",
			Message:    "No CU account matched the requested selector.",
			Suggestion: "Check the resource selector and subscription; use an ARM account ID for an exact match.",
		}
	}
	if len(matches) != 1 {
		return Account{}, &cuclient.Error{
			Kind: cuclient.ErrorConflict, Code: "ambiguous_account", Message: "More than one account matched the selector.",
			Suggestion: "Specify the full ARM account ID or narrow the resource group.",
		}
	}
	return matches[0], nil
}

func (r *Resolver) continuation(link, path string) (string, error) {
	base, _ := url.Parse(r.client.Endpoint())
	next, err := url.Parse(link)
	if err != nil || next.Scheme != base.Scheme || !strings.EqualFold(next.Host, base.Host) ||
		next.User != nil || next.Fragment != "" || next.RawPath != "" || next.Path != path ||
		next.Query().Get("api-version") != accountsAPIVersion || len(next.Query()["api-version"]) != 1 {
		return "", failure("invalid_arm_paging", "ARM pagination must remain on the same account-list operation.", nil)
	}
	return next.String(), nil
}

func (r *Resolver) get(ctx context.Context, target string) ([]byte, error) {
	ctx = policy.WithRetryOptions(ctx, policy.RetryOptions{MaxRetries: 2, TryTimeout: 2 * time.Minute})
	request, err := runtime.NewRequest(ctx, http.MethodGet, target)
	if err != nil {
		return nil, failure("arm_request_failed", "The ARM discovery request could not be constructed.", err)
	}
	runtime.SkipBodyDownload(request)
	response, err := r.client.Pipeline().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("arm_discovery_failed", "ARM account discovery could not complete.", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		kind := cuclient.ErrorService
		switch response.StatusCode {
		case 401, 403:
			kind = cuclient.ErrorAuthentication
		case 404:
			kind = cuclient.ErrorNotFound
		case 429:
			kind = cuclient.ErrorThrottled
		}
		return nil, &cuclient.Error{
			Kind: kind, Code: "arm_discovery_failed", StatusCode: response.StatusCode,
			Message:    "The ARM account discovery request failed.",
			Suggestion: "Check the subscription and ARM read permission; run azd auth login with the intended identity.",
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil || len(data) > responseLimit {
		return nil, failure("invalid_arm_response",
			"ARM account discovery returned an unreadable or oversized response.", err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return data, nil
}

type boundedTransport struct{ inner policy.Transporter }

func (t boundedTransport) Do(request *http.Request) (*http.Response, error) {
	response, err := t.inner.Do(request)
	if err == nil {
		err = request.Context().Err()
	}
	if err != nil {
		if response != nil && response.Body != nil {
			err = errors.Join(err, response.Body.Close())
		}
		return nil, err
	}
	if response != nil && response.Body != nil {
		response.Body = &boundedBody{Reader: io.LimitReader(response.Body, responseLimit+1), Closer: response.Body}
	}
	return response, err
}

type boundedBody struct {
	io.Reader
	io.Closer
}

type discoveryCredential struct{ inner azcore.TokenCredential }

func (c discoveryCredential) GetToken(
	ctx context.Context, options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	token, err := c.inner.GetToken(ctx, options)
	if err == nil {
		return token, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return azcore.AccessToken{}, err
	}
	return azcore.AccessToken{}, &cuclient.Error{
		Kind: cuclient.ErrorAuthentication, Code: "arm_authentication_failed",
		Message:    "An Entra token for ARM account discovery could not be acquired.",
		Suggestion: "Run azd auth login with the intended identity and configured tenant.", Cause: err,
	}
}

func parseAccount(raw json.RawMessage) (Account, config.ResourceScope, error) {
	var wire struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Location   string `json:"location"`
		Properties struct {
			Endpoint  string            `json:"endpoint"`
			Endpoints map[string]string `json:"endpoints"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return Account{}, config.ResourceScope{}, failure("invalid_arm_response", "ARM returned an invalid account.", nil)
	}
	selector, err := ParseSelector(wire.ID, "", "", config.ResourceScope{})
	scope := selector.Scope
	if err != nil || scope.ResourceID == "" || !strings.EqualFold(wire.Name, scope.ResourceName) ||
		!scopeSegment.MatchString(wire.Location) {
		return Account{}, config.ResourceScope{}, failure("invalid_arm_response",
			"ARM returned invalid account coordinates.", nil)
	}
	endpoint := wire.Properties.Endpoint
	for _, key := range []string{"Content Understanding", "ContentUnderstanding", "OpenAI"} {
		if wire.Properties.Endpoints[key] != "" {
			endpoint = wire.Properties.Endpoints[key]
			break
		}
	}
	if endpoint == "" {
		// Azure accounts without endpoint properties use their documented account DNS name.
		if !scopeSegment.MatchString(wire.Name) {
			return Account{}, config.ResourceScope{}, failure("invalid_arm_response",
				"ARM returned an invalid account name.", nil)
		}
		endpoint = "https://" + wire.Name + ".services.ai.azure.com"
	}
	normalized, err := cuclient.NormalizeEndpoint(endpoint)
	if err != nil {
		return Account{}, config.ResourceScope{}, failure("invalid_arm_response",
			"ARM returned an invalid CU endpoint.", nil)
	}
	return Account{ID: wire.ID, Endpoint: normalized, Region: wire.Location}, scope, nil
}

func invalid(code, message string) error {
	return &cuclient.Error{Kind: cuclient.ErrorValidation, Code: code, Message: message}
}

func failure(code, message string, cause error) error {
	if errors.Is(cause, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if domain, ok := errors.AsType[*cuclient.Error](cause); ok && domain.Kind == cuclient.ErrorAuthentication {
		return domain
	}
	return &cuclient.Error{Kind: cuclient.ErrorService, Code: code, Message: message, Cause: cause}
}
