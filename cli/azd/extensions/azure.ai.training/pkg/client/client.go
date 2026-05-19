// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	DefaultAPIVersion = "2026-01-15-preview"
	DatasetAPIVersion = "v1"
	DataPlaneScope    = "https://ai.azure.com/.default"
	ARMScope          = "https://management.azure.com/.default"
)

// Client is an HTTP client for Azure AI Foundry project APIs.
type Client struct {
	baseURL    string
	subPath    string
	apiVersion string
	credential azcore.TokenCredential
	httpClient *http.Client
	debugBody  bool
}

// SetDebugBody enables logging of request bodies.
func (c *Client) SetDebugBody(enabled bool) {
	c.debugBody = enabled
}

// NewClient creates a new client from a project endpoint URL.
// Endpoint format: https://{account}.services.ai.azure.com/api/projects/{project}
// Also supports: https://{account}.cognitiveservices.azure.com/api/projects/{project}
func NewClient(projectEndpoint string, credential azcore.TokenCredential) (*Client, error) {
	parsedURL, err := url.Parse(projectEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid project endpoint URL: %w", err)
	}

	// Enforce HTTPS to prevent sending bearer tokens over plaintext
	if !strings.EqualFold(parsedURL.Scheme, "https") {
		return nil, fmt.Errorf("invalid project endpoint URL: scheme must be https")
	}

	// Reject URLs with embedded credentials
	if parsedURL.User != nil {
		return nil, fmt.Errorf("invalid project endpoint URL: userinfo is not allowed")
	}

	hostname := parsedURL.Hostname()
	if hostname == "" {
		return nil, fmt.Errorf("invalid project endpoint URL: missing hostname")
	}

	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(pathParts) != 3 || pathParts[0] != "api" || pathParts[1] != "projects" || pathParts[2] == "" {
		return nil, fmt.Errorf("invalid project endpoint URL: expected format https://{account}.services.ai.azure.com/api/projects/{project}")
	}

	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	subPath := "/" + strings.Join(pathParts[:3], "/")

	return &Client{
		baseURL:    baseURL,
		subPath:    subPath,
		apiVersion: DefaultAPIVersion,
		credential: credential,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// doDataPlaneWithVersion executes an authenticated HTTP request with a specific API version.
func (c *Client) doDataPlaneWithVersion(ctx context.Context, method, path, apiVersion string, body any, queryParams ...string) (*http.Response, error) {
	var reqURL strings.Builder
	reqURL.WriteString(fmt.Sprintf("%s%s/%s?api-version=%s", c.baseURL, c.subPath, path, apiVersion))
	for i := 0; i+1 < len(queryParams); i += 2 {
		reqURL.WriteString(fmt.Sprintf("&%s=%s", queryParams[i], url.QueryEscape(queryParams[i+1])))
	}

	if c.debugBody {
		fmt.Fprintf(os.Stderr, "[DEBUG] %s %s\n", method, reqURL.String())
	}

	var bodyBytes []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		if c.debugBody {
			fmt.Fprintf(os.Stderr, "[DEBUG] Request body: %s\n", string(data))
		}
		bodyBytes = data
	}

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req, bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// do executes req with a small retry policy. It retries on transient network
// errors and HTTP 429 / 502 / 503 / 504 responses with exponential backoff and
// jitter, honoring Retry-After on 429 / 503 responses. bodyBytes, when non-nil,
// is used to reset the request body between attempts. Max 3 attempts.
func (c *Client) do(req *http.Request, bodyBytes []byte) (*http.Response, error) {
	const maxAttempts = 3
	const baseDelay = 500 * time.Millisecond
	const maxDelay = 8 * time.Second

	var lastResp *http.Response
	var lastErr error
	ctx := req.Context()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// TEMP: unconditional API trace for manual testing — remove before merge.
		fmt.Fprintf(os.Stderr, "\x1b[38;5;208m[API] %s %s (attempt %d/%d)\x1b[0m\n", req.Method, req.URL.String(), attempt, maxAttempts)
		start := time.Now()

		// #nosec G107 G704 -- req.URL is constructed by this client from configured baseURL and known paths
		resp, err := c.httpClient.Do(req)
		lastResp, lastErr = resp, err

		// TEMP: unconditional API trace for manual testing — remove before merge.
		if err != nil {
			fmt.Fprintf(os.Stderr, "\x1b[38;5;208m[API] -> ERROR %v (%s)\x1b[0m\n", err, time.Since(start).Round(time.Millisecond))
		} else {
			fmt.Fprintf(os.Stderr, "\x1b[38;5;208m[API] -> %d %s (%s)\x1b[0m\n", resp.StatusCode, http.StatusText(resp.StatusCode), time.Since(start).Round(time.Millisecond))
		}

		if err == nil && !isRetriableStatus(resp.StatusCode) {
			return resp, nil
		}
		if err != nil && !isRetriableNetError(err) {
			return nil, err
		}
		if attempt == maxAttempts {
			break
		}

		delay := backoffDelay(attempt, baseDelay, maxDelay)
		if resp != nil {
			if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
				delay = ra
			}
			// Drain and close so the connection can be reused.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
			_ = resp.Body.Close()
			lastResp = nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return lastResp, nil
}

func isRetriableStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

func isRetriableNetError(err error) bool {
	// Don't retry on context cancellation/timeout; those are caller-driven.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// All other transport-level errors (DNS, reset, EOF, transient TLS) are
	// worth one more attempt.
	return true
}

func backoffDelay(attempt int, base, max time.Duration) time.Duration {
	// Exponential: base * 2^(attempt-1)
	delay := base << (attempt - 1)
	if delay <= 0 || delay > max {
		delay = max
	}
	// Full jitter in [delay/2, delay].
	// #nosec G404 -- non-cryptographic jitter for retry backoff
	jitter := time.Duration(rand.Int63n(int64(delay) / 2))
	return delay/2 + jitter
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// doDataPlane executes an authenticated HTTP request against the data plane.
func (c *Client) doDataPlane(ctx context.Context, method, path string, body any, queryParams ...string) (*http.Response, error) {
	return c.doDataPlaneWithVersion(ctx, method, path, c.apiVersion, body, queryParams...)
}

// getAbsoluteDataPlane issues an authenticated GET against an absolute URL
// (such as a Location header returned by a long-running operation). The host
// must match the configured data-plane base URL host to avoid leaking the
// bearer token to a foreign endpoint.
func (c *Client) getAbsoluteDataPlane(ctx context.Context, rawURL string) (*http.Response, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid operation URL: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("invalid operation URL: scheme must be https")
	}
	baseParsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid client base URL: %w", err)
	}
	if !strings.EqualFold(parsed.Host, baseParsed.Host) {
		return nil, fmt.Errorf("refusing to follow operation URL on foreign host %q (expected %q)",
			parsed.Host, baseParsed.Host)
	}

	if c.debugBody {
		fmt.Fprintf(os.Stderr, "[DEBUG] GET %s\n", parsed.String())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.do(req, nil)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
}

// addAuth adds a bearer token to the request.
func (c *Client) addAuth(ctx context.Context, req *http.Request, scope string) error {
	token, err := c.getToken(ctx, scope)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// getToken returns a bearer access token for the given scope.
func (c *Client) getToken(ctx context.Context, scope string) (string, error) {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return "", fmt.Errorf(
			"authentication failed; check your network connection or run 'azd auth login' and retry: %w", err)
	}
	return token.Token, nil
}

// maxErrorBodyBytes caps how much of an error response body we read into memory.
// Error envelopes are tiny JSON documents; 64 KiB gives generous headroom while
// protecting against a misbehaving server returning an unbounded body.
const maxErrorBodyBytes = 64 * 1024

// HandleError reads the error body and returns a formatted error.
func (c *Client) HandleError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))

	var apiErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	parsed := json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != ""

	// 5xx → service-side issue. The server-provided code/message ("ServiceError",
	// "InternalServerError") is rarely actionable for end-users, so prefer a
	// retry hint. Keep server detail in -v / debug paths.
	if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
		if parsed && c.debugBody {
			return fmt.Errorf("the service is currently unavailable (HTTP %d), please retry in a moment: %s",
				resp.StatusCode, apiErr.Error.Message)
		}
		return fmt.Errorf("the service is currently unavailable (HTTP %d), please retry in a moment",
			resp.StatusCode)
	}

	if parsed {
		return fmt.Errorf("API error (%d): %s - %s", resp.StatusCode, apiErr.Error.Code, apiErr.Error.Message)
	}

	return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
}
