// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	environmentCollectionPath = "/rl_environments"
	foundryAPIVersion         = "2025-11-15-preview"
	foundryTokenScope         = "https://ai.azure.com/.default" //nolint:gosec // OAuth scope, not a credential
)

type rleClient struct {
	baseUrl    string
	credential azcore.TokenCredential
	httpClient *http.Client
}

var createRleClient = newRleClient

type v1EnvironmentRequest struct {
	Name         string `json:"name,omitempty"`
	AcrImagePath string `json:"acrImagePath"`
	VersionBump  string `json:"versionBump,omitempty"`
}

type environmentResource struct {
	Id                        string `json:"id"`
	ProjectId                 string `json:"projectId,omitempty"`
	Name                      string `json:"name,omitempty"`
	AcrImagePath              string `json:"acrImagePath,omitempty"`
	Version                   string `json:"version,omitempty"`
	CreatedAt                 string `json:"createdAtUtc,omitempty"`
	UpdatedAt                 string `json:"updatedAtUtc,omitempty"`
	VersionLabel              string `json:"versionLabel,omitempty"`
	DiskImageConversionStatus string `json:"diskImageConversionStatus,omitempty"`
	DiskImageConversionError  string `json:"diskImageConversionError,omitempty"`
}

type pagedEnvironmentResponse struct {
	Data                  []environmentResource `json:"data"`
	NextContinuationToken string                `json:"nextContinuationToken,omitempty"`
}

type createInstanceGroupRequest struct {
	MaxActiveInstances int `json:"maxActiveInstances"`
}

type instanceGroupResource struct {
	Id                 string `json:"id"`
	EnvironmentName    string `json:"environmentName"`
	EnvironmentVersion string `json:"environmentVersion"`
	MaxActiveInstances int    `json:"maxActiveInstances"`
}

type instanceResource struct {
	InstanceId      string `json:"instanceId"`
	InstanceGroupId string `json:"instanceGroupId"`
	BaseUrl         string `json:"baseUrl,omitempty"`
	Status          string `json:"status,omitempty"`
	Error           string `json:"error,omitempty"`
}

type rleHTTPError struct {
	statusCode int
	body       string
	details    *rleErrorBody
}

type rleErrorBody struct {
	Code    string        `json:"code,omitempty"`
	Message string        `json:"message,omitempty"`
	Error   *rleErrorBody `json:"error,omitempty"`
}

func (e *rleHTTPError) Error() string {
	return fmt.Sprintf("RLE service returned HTTP %d: %s", e.statusCode, e.message())
}

func (e *rleHTTPError) code() string {
	if e.details == nil {
		return ""
	}
	return strings.TrimSpace(e.details.primary().Code)
}

func (e *rleHTTPError) message() string {
	if e.details != nil {
		if message := strings.TrimSpace(e.details.primary().Message); message != "" {
			return message
		}
	}
	return strings.TrimSpace(e.body)
}

func newRleHTTPError(statusCode int, body []byte) *rleHTTPError {
	result := &rleHTTPError{
		statusCode: statusCode,
		body:       string(body),
	}
	var details rleErrorBody
	if err := json.Unmarshal(body, &details); err == nil {
		if primary := details.primary(); primary.Code != "" || primary.Message != "" {
			result.details = &details
		}
	}
	return result
}

func (b *rleErrorBody) primary() *rleErrorBody {
	current := b
	for current != nil && current.Error != nil {
		current = current.Error
	}
	if current == nil {
		return &rleErrorBody{}
	}
	return current
}

func serviceError(err error) error {
	result := &azdext.ServiceError{
		Message:     err.Error(),
		ServiceName: "rle-service",
		Suggestion: fmt.Sprintf(
			"Ensure the Foundry project endpoint in %s is reachable and enabled for RLE.",
			foundryProjectEndpointEnvVar,
		),
	}
	if httpErr, ok := errors.AsType[*rleHTTPError](err); ok {
		result.ErrorCode = httpErr.code()
		result.StatusCode = httpErr.statusCode
		switch {
		case httpErr.statusCode == http.StatusUnauthorized || httpErr.statusCode == http.StatusForbidden:
			result.Suggestion = "Verify your Azure sign-in and access to the Foundry project, then retry."
		case httpErr.statusCode == http.StatusTooManyRequests:
			result.Suggestion = "Wait for the RLE service retry window, then retry."
		case httpErr.statusCode >= http.StatusInternalServerError:
			result.Suggestion = "Retry later. If the problem persists, check the RLE service status and logs."
		}
	}
	return result
}

func newRleClient(endpoint string) (*rleClient, error) {
	normalizedEndpoint, err := normalizeFoundryProjectEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}

	return newRleClientWithCredential(normalizedEndpoint, credential), nil
}

func newRleClientWithCredential(endpoint string, credential azcore.TokenCredential) *rleClient {
	return &rleClient{
		baseUrl:    strings.TrimRight(endpoint, "/"),
		credential: credential,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *rleClient) createV1Environment(
	ctx context.Context,
	request v1EnvironmentRequest,
) (*environmentResource, error) {
	var result environmentResource
	if err := c.do(ctx, http.MethodPost, environmentCollectionPath, request, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) listEnvironments(
	ctx context.Context,
	continuationToken string,
	limit int,
) (*pagedEnvironmentResponse, error) {
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if continuationToken != "" {
		query.Set("continuationToken", continuationToken)
	}

	var result pagedEnvironmentResponse
	if err := c.do(ctx, http.MethodGet, environmentCollectionPath+"?"+query.Encode(), nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) listEnvironmentVersions(
	ctx context.Context,
	name string,
	continuationToken string,
	limit int,
) (*pagedEnvironmentResponse, error) {
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if continuationToken != "" {
		query.Set("continuationToken", continuationToken)
	}
	suffix := fmt.Sprintf("/%s/versions?%s", url.PathEscape(name), query.Encode())

	var result pagedEnvironmentResponse
	if err := c.do(ctx, http.MethodGet, environmentCollectionPath+suffix, nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) createInstanceGroup(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
) (*instanceGroupResource, error) {
	suffix := instanceGroupCollectionSuffix(environmentName, environmentVersion)
	request := createInstanceGroupRequest{MaxActiveInstances: 1}

	var result instanceGroupResource
	if err := c.do(ctx, http.MethodPost, environmentCollectionPath+suffix, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *rleClient) deleteInstanceGroup(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	instanceGroupId string,
) error {
	suffix := instanceGroupSuffix(environmentName, environmentVersion, instanceGroupId)
	return c.do(ctx, http.MethodDelete, environmentCollectionPath+suffix, nil, nil)
}

func (c *rleClient) createInstance(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	instanceGroupId string,
) (*instanceResource, error) {
	suffix := instanceCollectionSuffix(environmentName, environmentVersion, instanceGroupId)

	var result instanceResource
	if err := c.do(ctx, http.MethodPost, environmentCollectionPath+suffix, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *rleClient) getInstance(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	instanceGroupId string,
	instanceId string,
) (*instanceResource, error) {
	suffix := instanceSuffix(environmentName, environmentVersion, instanceGroupId, instanceId)

	var result instanceResource
	if err := c.do(ctx, http.MethodGet, environmentCollectionPath+suffix, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *rleClient) deleteInstance(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	instanceGroupId string,
	instanceId string,
) error {
	suffix := instanceSuffix(environmentName, environmentVersion, instanceGroupId, instanceId)
	return c.do(ctx, http.MethodDelete, environmentCollectionPath+suffix, nil, nil)
}

func instanceSuffix(
	environmentName string,
	environmentVersion string,
	instanceGroupId string,
	instanceId string,
) string {
	return instanceCollectionSuffix(environmentName, environmentVersion, instanceGroupId) +
		"/" + url.PathEscape(instanceId)
}

func instanceCollectionSuffix(
	environmentName string,
	environmentVersion string,
	instanceGroupId string,
) string {
	return instanceGroupSuffix(environmentName, environmentVersion, instanceGroupId) + "/instances"
}

func instanceGroupSuffix(environmentName string, environmentVersion string, instanceGroupId string) string {
	return instanceGroupCollectionSuffix(environmentName, environmentVersion) +
		"/" + url.PathEscape(instanceGroupId)
}

func instanceGroupCollectionSuffix(environmentName string, environmentVersion string) string {
	path := "/" + url.PathEscape(environmentName)
	if environmentVersion != "" {
		path += "/versions/" + url.PathEscape(environmentVersion)
	}
	return path + "/instance_groups"
}

func (c *rleClient) do(ctx context.Context, method string, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	requestUrl, err := url.Parse(c.baseUrl + path)
	if err != nil {
		return fmt.Errorf("create request URL: %w", err)
	}
	query := requestUrl.Query()
	query.Set("api-version", foundryAPIVersion)
	requestUrl.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, requestUrl.String(), reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return errors.New("RLE service authentication requires an HTTPS Foundry project endpoint")
	}
	authorization, err := c.authorizationHeader(ctx)
	if err != nil {
		return fmt.Errorf("authenticate to Foundry: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call RLE service %s: %w", c.baseUrl, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read RLE response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newRleHTTPError(resp.StatusCode, respBody)
	}

	if target == nil || len(respBody) == 0 {
		return nil
	}

	if err := json.Unmarshal(respBody, target); err != nil {
		return fmt.Errorf("decode RLE response: %w", err)
	}

	return nil
}

func (c *rleClient) authorizationHeader(ctx context.Context) (string, error) {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{foundryTokenScope},
	})
	if err != nil {
		return "", err
	}
	return "Bearer " + token.Token, nil
}

func nextPaginationCursor(seen map[string]struct{}, nextToken string, newCursorError func() error) (string, error) {
	if strings.TrimSpace(nextToken) == "" {
		return "", newCursorError()
	}
	if _, exists := seen[nextToken]; exists {
		return "", newCursorError()
	}
	seen[nextToken] = struct{}{}
	return nextToken, nil
}

func paginationSafetyLimitError(resourceName string, code string) error {
	return &azdext.LocalError{
		Message: fmt.Sprintf(
			"%s exceeded the %d-item safety limit.",
			resourceName,
			environmentListPageSize*environmentListMaxPages,
		),
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}
