// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

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
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

const maxErrorBodyBytes = 64 * 1024

var serviceCodePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// RESTTransport implements Client over the internal CU HTTP pipeline.
type RESTTransport struct {
	endpoint      string
	apiVersion    string
	pollInterval  time.Duration
	readRetry     policy.RetryOptions
	writeRetry    policy.RetryOptions
	readPipeline  runtime.Pipeline
	writePipeline runtime.Pipeline
}

func NewRESTTransport(
	endpoint string, credential azcore.TokenCredential, options *TransportOptions,
) (*RESTTransport, error) {
	normalized, err := NormalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, validationError("missing_credential", "An azd-backed Azure credential is required.")
	}
	settings, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	transport := settings.Transport
	if transport == nil {
		transport = newHTTPTransport(settings.RequestTimeout)
	}
	readRetry := policy.RetryOptions{MaxRetries: 2, TryTimeout: settings.RequestTimeout}
	writeRetry := readRetry
	writeRetry.MaxRetries = -1
	build := func(retry policy.RetryOptions) runtime.Pipeline {
		return runtime.NewPipeline("azd-cu", "v0.0.1", runtime.PipelineOptions{}, &policy.ClientOptions{
			Transport: &boundedErrorTransport{inner: transport},
			Retry:     retry,
			PerCallPolicies: []policy.Policy{
				&credentialPolicy{credential: credential},
				azsdk.NewMsClientRequestIdPolicy(),
				azsdk.NewMsCorrelationPolicy(),
				azsdk.NewUserAgentPolicy("azd-ext-azure-ai-content-understanding/" + settings.Version),
			},
		})
	}
	return &RESTTransport{
		endpoint: normalized, apiVersion: settings.APIVersion,
		pollInterval: time.Second, readRetry: readRetry, writeRetry: writeRetry,
		readPipeline: build(readRetry), writePipeline: build(writeRetry),
	}, nil
}

func newHTTPTransport(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// request accepts only service-relative paths. A continuation or operation URL
// must be separately validated before it can be converted to one of these paths.
func (c *RESTTransport) request(
	ctx context.Context, method, path string, query url.Values, body io.ReadSeekCloser, contentType string,
) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, transportError(err)
	}
	relative, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || relative.IsAbs() || relative.Host != "" ||
		relative.RawQuery != "" || relative.Fragment != "" || relative.RawPath != "" {
		return nil, validationError("invalid_request_path", "Use a service-relative CU operation path.")
	}
	for segment := range strings.SplitSeq(relative.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, validationError("invalid_request_path", "The CU operation path contains a traversal segment.")
		}
	}
	target, err := url.Parse(c.endpoint + relative.Path)
	if err != nil {
		return nil, validationError("invalid_request_path", "The CU operation path is invalid.")
	}
	values := url.Values{}
	for key, items := range query {
		values[key] = append([]string(nil), items...)
	}
	values.Set("api-version", c.apiVersion)
	target.RawQuery = values.Encode()
	pipeline, retry := c.writePipeline, c.writeRetry
	if method == http.MethodGet || method == http.MethodHead {
		pipeline, retry = c.readPipeline, c.readRetry
	}
	// azcore accepts context-level retry overrides. Do not let a host context
	// silently weaken CU's retry or per-attempt timeout guarantees.
	ctx = policy.WithRetryOptions(ctx, retry)
	request, err := runtime.NewRequest(ctx, method, target.String())
	if err != nil {
		return nil, validationError("invalid_request", "The CU HTTP request could not be constructed.")
	}
	request.Raw().Header.Set("Accept", "application/json")
	if body != nil {
		if contentType == "" {
			return nil, validationError("missing_content_type", "Specify the content type for the request body.")
		}
		if err := request.SetBody(body, contentType); err != nil {
			return nil, transportError(err)
		}
	}
	runtime.SkipBodyDownload(request)
	response, err := pipeline.Do(request)
	if err != nil {
		return nil, transportError(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, responseError(response)
	}
	return response, nil
}

type credentialPolicy struct {
	credential azcore.TokenCredential
}

func (p *credentialPolicy) Do(request *policy.Request) (*http.Response, error) {
	// Acquire once per operation. In particular, a 401 challenge must never
	// cause the bearer policy to replay a non-idempotent write.
	token, err := p.credential.GetToken(
		request.Raw().Context(), policy.TokenRequestOptions{Scopes: []string{cognitiveServicesScope}},
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, transportError(err)
		}
		return nil, &Error{
			Kind: ErrorAuthentication, Code: "authentication_failed", Message: "Azure authentication failed.",
			Suggestion: "Run `azd auth login` with the intended account and retry.",
		}
	}
	if token.Token == "" {
		return nil, &Error{
			Kind: ErrorAuthentication, Code: "empty_access_token", Message: "Azure authentication returned no access token.",
			Suggestion: "Run `azd auth login` and retry.",
		}
	}
	request.Raw().Header.Set("Authorization", "Bearer "+token.Token)
	return request.Next()
}

// azcore downloads error bodies even when SkipBodyDownload is set. Bound them
// at the transport boundary, before its downloader and error policies see them.
type boundedErrorTransport struct {
	inner policy.Transporter
}

func (t *boundedErrorTransport) Do(request *http.Request) (*http.Response, error) {
	response, err := t.inner.Do(request)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, &Error{Kind: ErrorService, Code: "missing_response", Message: "The HTTP transport returned no response."}
	}
	if response.Body == nil {
		response.Body = http.NoBody
	}
	if response.StatusCode >= 300 && response.Body != nil {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return nil, transportError(errors.Join(readErr, closeErr))
		}
		if len(body) > maxErrorBodyBytes {
			body = nil
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
	}
	return response, nil
}

func responseError(response *http.Response) error {
	defer response.Body.Close()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	// Malformed and oversized service error bodies retain HTTP classification,
	// but never substitute raw server text for a user-facing message.
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, maxErrorBodyBytes)).Decode(&envelope)
	code := response.Header.Get("x-ms-error-code")
	if code == "" && decodeErr == nil {
		code = envelope.Error.Code
	}
	if !serviceCodePattern.MatchString(code) {
		code = fmt.Sprintf("http_%d", response.StatusCode)
	}
	kind := ErrorService
	suggestion := "Check the service configuration and retry."
	switch response.StatusCode {
	case http.StatusBadRequest:
		kind = ErrorValidation
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = ErrorAuthentication
		suggestion = "Check the azd account, selected tenant, and access to the CU resource."
	case http.StatusNotFound:
		kind = ErrorNotFound
	case http.StatusConflict:
		kind = ErrorConflict
	case http.StatusRequestEntityTooLarge:
		kind = ErrorLimit
	case http.StatusTooManyRequests:
		kind = ErrorThrottled
		suggestion = "Wait for the service retry interval before trying again."
	}
	return &Error{
		Kind: kind, Code: code, StatusCode: response.StatusCode,
		Message:    fmt.Sprintf("The Content Understanding request failed (HTTP %d).", response.StatusCode),
		Suggestion: suggestion, RetryAfter: retryAfter(response.Header.Get("Retry-After"), time.Now()),
	}
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(min(seconds, 86400)) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(0, min(date.Sub(now), 24*time.Hour))
	}
	return 0
}

func transportError(err error) error {
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Kind: ErrorCancelled, Code: "canceled", Message: "The request was cancelled.", Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: ErrorTimeout, Code: "deadline_exceeded", Message: "The request timed out.", Cause: err}
	}
	return &Error{
		Kind: ErrorService, Code: "transport_failed", Message: "The CU service request could not be completed.",
		Suggestion: "Check connectivity and retry.", Cause: err,
	}
}
