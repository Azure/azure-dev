// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package curestclient provides the Content Understanding REST client used by the azd extension.
package curestclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

const (
	moduleName = "azure.ai.content-understanding/cu-rest-client"
	version    = "v0.1.0"
	// APIVersionGA is the stable service API version.
	APIVersionGA = "2025-11-01"
	// APIVersionPreview supports inline analysis.
	APIVersionPreview = "2026-06-01-preview"
)

const maxErrorBodyBytes = 64 * 1024

var serviceErrorCodePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// NormalizeAPIVersion accepts the GA version or a date-formatted preview version.
func NormalizeAPIVersion(value string) (string, error) {
	if value == "" {
		return APIVersionGA, nil
	}
	if value == APIVersionGA {
		return value, nil
	}
	if date, ok := strings.CutSuffix(value, "-preview"); ok && len(date) == len(time.DateOnly) {
		if _, err := time.Parse(time.DateOnly, date); err == nil {
			return value, nil
		}
	}
	return "", errors.New("API version must be 2025-11-01 or a valid YYYY-MM-DD-preview date")
}

// ClientOptions configures a Client.
type ClientOptions struct {
	azcore.ClientOptions
	// APIVersion overrides the embedded ClientOptions.APIVersion and defaults to APIVersionGA.
	APIVersion string
	// Audience overrides the default https://cognitiveservices.azure.com audience.
	Audience string
}

// Client calls the Content Understanding data-plane service.
// Methods are safe for concurrent use. Pollers and pagers are not.
type Client struct {
	internal     *azcore.Client
	endpoint     string
	apiVersion   string
	retryOptions policy.RetryOptions
}

// NewClient creates a client using a caller-provided Microsoft Entra credential.
func NewClient(endpoint string, credential azcore.TokenCredential, options *ClientOptions) (*Client, error) {
	if credential == nil {
		return nil, errors.New("credential is required")
	}
	audience := "https://cognitiveservices.azure.com"
	if options != nil && options.Audience != "" {
		audience = strings.TrimRight(options.Audience, "/")
	}
	return newClient(endpoint, &tokenCredentialPolicy{
		credential: credential,
		scopes:     []string{audience + "/.default"},
	}, options)
}

// NewClientWithKeyCredential creates a client using a resource key.
func NewClientWithKeyCredential(endpoint string, credential *azcore.KeyCredential, options *ClientOptions) (*Client, error) {
	if credential == nil {
		return nil, errors.New("key credential is required")
	}
	return newClient(endpoint, runtime.NewKeyCredentialPolicy(credential, "Ocp-Apim-Subscription-Key", nil), options)
}

func newClient(endpoint string, auth policy.Policy, options *ClientOptions) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("endpoint must be an absolute HTTPS resource URL " +
			"without a path, query, credentials or fragment")
	}
	opts := ClientOptions{}
	if options != nil {
		opts = *options
	}
	if opts.APIVersion == "" {
		opts.APIVersion = opts.ClientOptions.APIVersion
	}
	opts.APIVersion, err = NormalizeAPIVersion(opts.APIVersion)
	if err != nil {
		return nil, err
	}
	opts.ClientOptions.APIVersion = opts.APIVersion
	client, err := azcore.NewClient(moduleName, version, runtime.PipelineOptions{
		APIVersion: runtime.APIVersionOptions{Name: "api-version"},
		PerCall: []policy.Policy{
			runtime.NewRequestIDPolicy(),
			&resourceCredentialPolicy{host: u.Host, auth: auth},
		},
	}, &opts.ClientOptions)
	if err != nil {
		return nil, err
	}
	return &Client{
		internal: client, endpoint: strings.TrimRight(u.String(), "/"),
		apiVersion: opts.APIVersion, retryOptions: opts.Retry,
	}, nil
}

type tokenCredentialPolicy struct {
	credential azcore.TokenCredential
	scopes     []string
}

func (p *tokenCredentialPolicy) Do(req *policy.Request) (*http.Response, error) {
	if err := req.Raw().Context().Err(); err != nil {
		return nil, err
	}
	token, err := p.credential.GetToken(req.Raw().Context(), policy.TokenRequestOptions{
		EnableCAE: true,
		Scopes:    p.scopes,
	})
	if err != nil {
		return nil, err
	}
	if token.Token == "" {
		return nil, errors.New("credential returned an empty access token")
	}
	req.Raw().Header.Set("Authorization", "Bearer "+token.Token)
	return req.Next()
}

type resourceCredentialPolicy struct {
	host string
	auth policy.Policy
}

func (p *resourceCredentialPolicy) Do(req *policy.Request) (*http.Response, error) {
	u := req.Raw().URL
	if u.Scheme != "https" || !strings.EqualFold(u.Host, p.host) || u.User != nil {
		return nil, errors.New("service continuation URL must remain on the HTTPS resource endpoint")
	}
	return p.auth.Do(req)
}

func analyzerPath(analyzerID string) (string, error) {
	if analyzerID == "" {
		return "", errors.New("analyzerID is required")
	}
	return "/analyzers/" + url.PathEscape(analyzerID), nil
}

func (c *Client) noWriteRetry(ctx context.Context) context.Context {
	options := c.retryOptions
	options.MaxRetries = -1
	return policy.WithRetryOptions(ctx, options)
}

func (c *Client) request(
	ctx context.Context, method, path string, query url.Values, body []byte, contentType string,
) (*policy.Request, error) {
	u, err := url.Parse(c.endpoint + "/contentunderstanding" + path)
	if err != nil {
		return nil, err
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("api-version", c.apiVersion)
	u.RawQuery = query.Encode()
	req, err := runtime.NewRequest(ctx, method, u.String())
	if err != nil {
		return nil, err
	}
	req.Raw().Header.Set("Accept", "application/json")
	if body != nil {
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(body)), contentType); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func (c *Client) send(req *policy.Request, codes ...int) (*http.Response, error) {
	resp, err := c.internal.Pipeline().Do(req)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(resp, codes...) {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil, ErrInvalidResponse
		}
		return nil, safeResponseError(resp, "")
	}
	return resp, nil
}

func safeResponseError(resp *http.Response, explicitCode string) error {
	code := explicitCode
	if code == "" {
		code = resp.Header.Get("x-ms-error-code")
	}
	if code == "" && resp.Body != nil {
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
		closeErr := resp.Body.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		if len(payload) <= maxErrorBodyBytes {
			code = deepestErrorCode(payload)
		}
	}
	if !serviceErrorCodePattern.MatchString(code) {
		code = fmt.Sprintf("http_%d", resp.StatusCode)
	}
	safe := &http.Response{
		Status: resp.Status, StatusCode: resp.StatusCode, Proto: resp.Proto,
		ProtoMajor: resp.ProtoMajor, ProtoMinor: resp.ProtoMinor,
		Header: resp.Header.Clone(), Body: http.NoBody, ContentLength: 0,
	}
	return &azcore.ResponseError{ErrorCode: code, StatusCode: resp.StatusCode, RawResponse: safe}
}

func deepestErrorCode(payload []byte) string {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.Error) == 0 {
		return ""
	}
	current := envelope.Error
	code := ""
	for len(current) != 0 {
		var item struct {
			Code       string          `json:"code"`
			InnerError json.RawMessage `json:"innererror"`
		}
		if json.Unmarshal(current, &item) != nil {
			break
		}
		if serviceErrorCodePattern.MatchString(item.Code) {
			code = item.Code
		}
		current = item.InnerError
	}
	return code
}

func decodeResponse[T any](resp *http.Response) (T, error) {
	var result T
	payload, err := runtime.Payload(resp)
	if err != nil {
		return result, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(payload), []byte("{")) {
		return result, fmt.Errorf("%w: response must be a JSON object", ErrInvalidResponse)
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return result, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	return result, nil
}

func (c *Client) jsonRequest(
	ctx context.Context, method, path string, query url.Values, body any, contentType string,
) (*policy.Request, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding Content Understanding request: %w", err)
		}
	}
	return c.request(ctx, method, path, query, payload, contentType)
}
