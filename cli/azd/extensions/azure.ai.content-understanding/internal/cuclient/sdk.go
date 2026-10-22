// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	sdk "github.com/Azure/azure-sdk-for-go/sdk/contentunderstanding/azcontentunderstanding"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// SDKTransport adapts the prototype CU Go SDK to the host-neutral command contract.
type SDKTransport struct {
	client       *sdk.Client
	endpoint     string
	apiVersion   string
	pollInterval time.Duration
}

var _ Client = (*SDKTransport)(nil)

func NewSDKTransport(
	endpoint string, credential azcore.TokenCredential, options *TransportOptions,
) (*SDKTransport, error) {
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
	retryOptions := policy.RetryOptions{MaxRetries: 2, TryTimeout: settings.RequestTimeout}
	client, err := sdk.NewClient(
		strings.TrimSuffix(normalized, "/contentunderstanding"),
		safeTokenCredential{inner: credential},
		&sdk.ClientOptions{
			APIVersion: settings.APIVersion,
			ClientOptions: azcore.ClientOptions{
				Transport: &boundedErrorTransport{inner: transport},
				Retry:     retryOptions,
				PerCallPolicies: []policy.Policy{
					&sdkPolicy{endpoint: normalized, apiVersion: settings.APIVersion, retry: retryOptions},
					azsdk.NewMsClientRequestIdPolicy(),
					azsdk.NewMsCorrelationPolicy(),
					azsdk.NewUserAgentPolicy("azd-ext-azure-ai-content-understanding/" + settings.Version),
				},
			},
		},
	)
	if err != nil {
		return nil, sdkDomainError(err)
	}
	return &SDKTransport{
		client: client, endpoint: normalized, apiVersion: settings.APIVersion, pollInterval: time.Second,
	}, nil
}

func (c *SDKTransport) ListAnalyzers(ctx context.Context) ([]Analyzer, error) {
	pager := c.client.NewListAnalyzersPager()
	items := []Analyzer{}
	seen := map[string]bool{(url.Values{"api-version": {c.apiVersion}}).Encode(): true}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, sdkDomainError(err)
		}
		if page.Value == nil {
			return nil, malformedResponse()
		}
		for _, value := range page.Value {
			analyzer, err := parseAnalyzer(value.RawJSON, "")
			if err != nil {
				return nil, err
			}
			items = append(items, analyzer)
		}
		if page.NextLink != "" {
			path, query, err := serviceLink(c.endpoint, c.apiVersion, page.NextLink, "/analyzers")
			if err != nil {
				return nil, err
			}
			if path != "/analyzers" || seen[query.Encode()] {
				return nil, malformedResponse()
			}
			seen[query.Encode()] = true
		}
	}
	return items, nil
}

func (c *SDKTransport) GetAnalyzer(ctx context.Context, id string) (Analyzer, error) {
	if err := ValidateAnalyzerID(id); err != nil {
		return Analyzer{}, err
	}
	value, err := c.client.GetAnalyzer(ctx, id)
	if err != nil {
		return Analyzer{}, sdkDomainError(err)
	}
	return parseAnalyzer(value.RawJSON, id)
}

func (c *SDKTransport) CreateAnalyzer(
	ctx context.Context, request CreateAnalyzerRequest,
) (AnalyzerResponse, error) {
	if err := request.Validate(); err != nil {
		return AnalyzerResponse{}, err
	}
	poller, err := c.client.BeginCreateAnalyzer(ctx, request.AnalyzerID, request.Definition)
	if err != nil {
		return AnalyzerResponse{}, sdkOperationDomainError(err, "analyzer")
	}
	value, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: c.pollInterval})
	if err != nil {
		return AnalyzerResponse{}, sdkOperationDomainError(err, "analyzer")
	}
	return c.analyzerResponse(ctx, value, request.AnalyzerID)
}

func (c *SDKTransport) DeleteAnalyzer(ctx context.Context, id string) error {
	if err := ValidateAnalyzerID(id); err != nil {
		return err
	}
	return sdkDomainError(c.client.DeleteAnalyzer(ctx, id))
}

func (c *SDKTransport) GrantCopyAuthorization(
	ctx context.Context, request GrantCopyAuthorizationRequest,
) (CopyAuthorization, error) {
	if err := request.Validate(); err != nil {
		return CopyAuthorization{}, err
	}
	value, err := c.client.GrantCopyAuthorization(ctx, request.AnalyzerID, sdk.GrantCopyAuthorizationRequest{
		TargetAzureResourceID: request.TargetAzureResourceID,
		TargetRegion:          request.TargetRegion,
	})
	if err != nil {
		return CopyAuthorization{}, sdkDomainError(err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, value.ExpiresAt)
	if err != nil || value.TargetAzureResourceID != request.TargetAzureResourceID ||
		expiresAt.IsZero() {
		return CopyAuthorization{}, malformedResponse()
	}
	return CopyAuthorization{
		Source: value.Source, TargetAzureResourceID: value.TargetAzureResourceID, ExpiresAt: expiresAt,
	}, nil
}

func (c *SDKTransport) CopyAnalyzer(
	ctx context.Context, request CopyAnalyzerRequest,
) (AnalyzerResponse, error) {
	if err := request.Validate(); err != nil {
		return AnalyzerResponse{}, err
	}
	poller, err := c.client.BeginCopyAnalyzer(ctx, request.DestinationAnalyzerID, sdk.CopyAnalyzerRequest{
		SourceAnalyzerID:      request.SourceAnalyzerID,
		SourceAzureResourceID: request.SourceAzureResourceID,
		SourceRegion:          request.SourceRegion,
	})
	if err != nil {
		return AnalyzerResponse{}, sdkOperationDomainError(err, "analyzer")
	}
	value, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: c.pollInterval})
	if err != nil {
		return AnalyzerResponse{}, sdkOperationDomainError(err, "analyzer")
	}
	return c.analyzerResponse(ctx, value, request.DestinationAnalyzerID)
}

func (c *SDKTransport) GetDefaults(ctx context.Context) (Defaults, error) {
	value, err := c.client.GetDefaults(ctx)
	if err != nil {
		return Defaults{}, sdkDomainError(err)
	}
	return defaults(value)
}

func (c *SDKTransport) UpdateDefaults(
	ctx context.Context, request UpdateDefaultsRequest,
) (Defaults, error) {
	if err := request.Validate(); err != nil {
		return Defaults{}, err
	}
	mappings := make(map[string]*string, len(request.ModelDeployments))
	for model, deployment := range request.ModelDeployments {
		value := deployment
		mappings[model] = &value
	}
	result, err := c.client.UpdateDefaults(ctx, mappings)
	if err != nil {
		return Defaults{}, sdkDomainError(err)
	}
	return defaults(result)
}

func (c *SDKTransport) Analyze(ctx context.Context, request AnalyzeRequest) (AnalyzeResponse, error) {
	if err := request.Validate(); err != nil {
		return AnalyzeResponse{}, err
	}
	if request.Mode == AnalysisModeInline && c.apiVersion != PreviewAPIVersion {
		return AnalyzeResponse{}, validationError(
			"inline_requires_preview", "Inline analysis requires the tested preview API version.",
		)
	}
	contentType, err := validateContentType(request.Input.ContentType)
	if err != nil {
		return AnalyzeResponse{}, err
	}
	var value sdk.AnalyzeResponse
	if request.Input.Body != nil {
		body := streaming.NopCloser(request.Input.Body)
		if request.Mode == AnalysisModeInline {
			value, err = c.client.AnalyzeBinaryInline(ctx, request.AnalyzerID, body, contentType)
		} else {
			var poller *runtime.Poller[sdk.AnalyzeResponse]
			poller, err = c.client.BeginAnalyzeBinary(ctx, request.AnalyzerID, body, contentType)
			if err == nil {
				value, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: c.pollInterval})
			}
		}
	} else {
		input := sdk.AnalyzeRequest{Inputs: []sdk.AnalysisInput{{
			URL: request.Input.URL, MIMEType: contentType,
		}}}
		if request.Mode == AnalysisModeInline {
			value, err = c.client.AnalyzeInline(ctx, request.AnalyzerID, input)
		} else {
			var poller *runtime.Poller[sdk.AnalyzeResponse]
			poller, err = c.client.BeginAnalyze(ctx, request.AnalyzerID, input)
			if err == nil {
				value, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: c.pollInterval})
			}
		}
	}
	if err != nil {
		return AnalyzeResponse{}, sdkOperationDomainError(err, "analysis")
	}
	result, err := parseAnalysisResult(value.Result, request.AnalyzerID, c.apiVersion)
	if err != nil {
		return AnalyzeResponse{}, err
	}
	usage, err := parseUsage(value.Usage)
	if err != nil {
		return AnalyzeResponse{}, err
	}
	return AnalyzeResponse{Result: result, Usage: usage, JSON: value.RawJSON}, nil
}

func (c *SDKTransport) analyzerResponse(
	ctx context.Context, value sdk.AnalyzerResponse, expectedID string,
) (AnalyzerResponse, error) {
	usage, err := parseUsage(value.Usage)
	if err != nil {
		return AnalyzerResponse{}, err
	}
	var analyzer Analyzer
	if len(value.Analyzer.RawJSON) == 0 {
		analyzer, err = c.GetAnalyzer(ctx, expectedID)
	} else {
		analyzer, err = parseAnalyzer(value.Analyzer.RawJSON, expectedID)
	}
	if err != nil {
		return AnalyzerResponse{}, err
	}
	if strings.EqualFold(analyzer.Status, "failed") {
		return AnalyzerResponse{}, operationError("analyzer", "Failed", nil)
	}
	if !strings.EqualFold(analyzer.Status, "ready") {
		return AnalyzerResponse{}, malformedResponse()
	}
	return AnalyzerResponse{Analyzer: analyzer, Usage: usage}, nil
}

func defaults(value sdk.Defaults) (Defaults, error) {
	if err := (UpdateDefaultsRequest{ModelDeployments: value.ModelDeployments}).Validate(); err != nil {
		return Defaults{}, malformedResponse()
	}
	return Defaults{ModelDeployments: value.ModelDeployments, JSON: value.RawJSON}, nil
}

func parseUsage(raw json.RawMessage) (*Usage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value wireUsage
	if err := decodeWire(raw, &value); err != nil {
		return nil, err
	}
	return value.domain(), nil
}

func sdkOperationDomainError(err error, operation string) error {
	if failure, ok := errors.AsType[*sdk.OperationError](err); ok {
		domain := operationError(operation, failure.Status, &wireDiagnostic{
			Code: failure.Code, Details: operationDiagnosticDetails(failure.Details),
		})
		domain.StatusCode = failure.ResponseError.StatusCode
		return domain
	}
	return sdkDomainError(err)
}

func operationDiagnosticDetails(values []sdk.OperationDiagnostic) []wireDiagnostic {
	if values == nil {
		return nil
	}
	result := make([]wireDiagnostic, len(values))
	for i, value := range values {
		result[i] = wireDiagnostic{Code: value.Code, Details: operationDiagnosticDetails(value.Details)}
	}
	return result
}

type sdkPolicy struct {
	endpoint   string
	apiVersion string
	retry      policy.RetryOptions
}

func (p *sdkPolicy) Do(request *policy.Request) (*http.Response, error) {
	// Caller retry overrides must not weaken the extension's retry and timeout bounds.
	retry := p.retry
	if request.Raw().Method != http.MethodGet && request.Raw().Method != http.MethodHead {
		retry.MaxRetries = -1
	}
	request = request.WithContext(policy.WithRetryOptions(request.Raw().Context(), retry))
	response, err := request.Next()
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, err
	}
	if response.Request == nil {
		response.Request = request.Raw()
	}
	path := strings.TrimPrefix(request.Raw().URL.Path, "/contentunderstanding")
	asyncAnalysis := request.Raw().Method == http.MethodPost &&
		(strings.HasSuffix(path, ":analyze") || strings.HasSuffix(path, ":analyzeBinary"))
	analyzerWrite := request.Raw().Method == http.MethodPut && strings.HasPrefix(path, "/analyzers/") ||
		request.Raw().Method == http.MethodPost && strings.HasSuffix(path, ":copy")
	if !asyncAnalysis && !analyzerWrite {
		return response, nil
	}
	location := response.Header.Get("Operation-Location")
	if location == "" && !asyncAnalysis {
		return response, nil
	}
	operationPath, _, err := serviceLink(p.endpoint, p.apiVersion, location, path)
	if err != nil {
		return nil, err
	}
	prefix := "/analyzerResults/"
	if analyzerWrite {
		prefix = strings.TrimSuffix(path, ":copy") + "/operations/"
	}
	id, found := strings.CutPrefix(operationPath, prefix)
	if !found || id == "" || strings.Contains(id, "/") {
		return nil, malformedResponse()
	}
	return response, nil
}

func sdkDomainError(err error) error {
	if err == nil {
		return nil
	}
	if domain, ok := errors.AsType[*Error](err); ok {
		return domain
	}
	if errors.Is(err, sdk.ErrInvalidResponse) {
		return malformedResponse()
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		retryAfterHeader := ""
		if response.RawResponse != nil {
			retryAfterHeader = response.RawResponse.Header.Get("Retry-After")
		}
		return serviceResponseError(response.StatusCode, response.ErrorCode, retryAfterHeader)
	}
	return transportError(err)
}

type safeTokenCredential struct {
	inner azcore.TokenCredential
}

func (c safeTokenCredential) GetToken(
	ctx context.Context, options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	token, err := c.inner.GetToken(ctx, options)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return azcore.AccessToken{}, transportError(err)
		}
		return azcore.AccessToken{}, &Error{
			Kind: ErrorAuthentication, Code: "authentication_failed", Message: "Azure authentication failed.",
			Suggestion: "Run `azd auth login` with the intended account and retry.",
		}
	}
	if token.Token == "" {
		return azcore.AccessToken{}, &Error{
			Kind: ErrorAuthentication, Code: "empty_access_token",
			Message:    "Azure authentication returned no access token.",
			Suggestion: "Run `azd auth login` and retry.",
		}
	}
	return token, nil
}

var _ azcore.TokenCredential = safeTokenCredential{}
