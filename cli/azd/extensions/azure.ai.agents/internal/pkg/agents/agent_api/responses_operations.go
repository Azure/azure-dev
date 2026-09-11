// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

func (c *AgentClient) responsesURL(pathSuffix string) string {
	return strings.TrimRight(c.endpoint, "/") + "/openai/v1/responses" + pathSuffix
}

// CreateResponse invokes an agent through the OpenAI Responses API.
func (c *AgentClient) CreateResponse(
	ctx context.Context,
	requestBody []byte,
	headers map[string]string,
) ([]byte, http.Header, error) {
	req, err := c.newResponseRequest(ctx, http.MethodPost, "", requestBody, headers)
	if err != nil {
		return nil, nil, err
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		responseErr := runtime.NewResponseError(resp)
		_ = resp.Body.Close()
		return nil, nil, responseErr
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, resp.Header.Clone(), nil
}

// CreateResponseStream invokes an agent and returns its Server-Sent Events stream.
// The caller must close the returned body.
func (c *AgentClient) CreateResponseStream(
	ctx context.Context,
	requestBody []byte,
	headers map[string]string,
) (io.ReadCloser, http.Header, error) {
	return c.CreateResponseStreamAt(ctx, c.responsesURL(""), requestBody, headers)
}

// CreateResponseStreamAt invokes an explicit Responses endpoint and returns its SSE stream.
func (c *AgentClient) CreateResponseStreamAt(
	ctx context.Context,
	endpoint string,
	requestBody []byte,
	headers map[string]string,
) (io.ReadCloser, http.Header, error) {
	req, err := c.newResponseRequestAt(ctx, http.MethodPost, endpoint, requestBody, headers)
	if err != nil {
		return nil, nil, err
	}
	if req.Raw().Header.Get("Accept") == "" {
		req.Raw().Header.Set("Accept", "text/event-stream")
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		return nil, nil, runtime.NewResponseError(resp)
	}
	return resp.Body, resp.Header.Clone(), nil
}

// GetResponse retrieves a stored response by ID.
func (c *AgentClient) GetResponse(ctx context.Context, responseID string) ([]byte, http.Header, error) {
	return c.doResponseRequest(ctx, http.MethodGet, responseID, []int{http.StatusOK})
}

// CancelResponse cancels an in-flight response.
func (c *AgentClient) CancelResponse(ctx context.Context, responseID string) ([]byte, http.Header, error) {
	return c.doResponseRequest(ctx, http.MethodPost, responseID+"/cancel", []int{http.StatusOK, http.StatusAccepted})
}

// DeleteResponse deletes a stored response.
func (c *AgentClient) DeleteResponse(ctx context.Context, responseID string) error {
	if strings.TrimSpace(responseID) == "" {
		return fmt.Errorf("responseID is required")
	}

	req, err := c.newResponseRequest(ctx, http.MethodDelete, responseID, nil, nil)
	if err != nil {
		return err
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusNoContent) {
		return runtime.NewResponseError(resp)
	}
	return nil
}

func (c *AgentClient) doResponseRequest(
	ctx context.Context,
	method string,
	pathSuffix string,
	successCodes []int,
) ([]byte, http.Header, error) {
	if strings.TrimSpace(pathSuffix) == "" {
		return nil, nil, fmt.Errorf("responseID is required")
	}

	req, err := c.newResponseRequest(ctx, method, pathSuffix, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, successCodes...) {
		return nil, nil, runtime.NewResponseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, resp.Header.Clone(), nil
}

func (c *AgentClient) newResponseRequest(
	ctx context.Context,
	method string,
	pathSuffix string,
	body []byte,
	headers map[string]string,
) (*policy.Request, error) {
	path := ""
	if pathSuffix != "" {
		path = "/" + url.PathEscape(strings.TrimSuffix(pathSuffix, "/cancel"))
		if strings.HasSuffix(pathSuffix, "/cancel") {
			path += "/cancel"
		}
	}
	return c.newResponseRequestAt(ctx, method, c.responsesURL(path), body, headers)
}

func (c *AgentClient) newResponseRequestAt(
	ctx context.Context,
	method string,
	endpoint string,
	body []byte,
	headers map[string]string,
) (*policy.Request, error) {
	req, err := runtime.NewRequest(ctx, method, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	for key, value := range headers {
		req.Raw().Header.Set(key, value)
	}
	if body != nil {
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(body)), "application/json"); err != nil {
			return nil, fmt.Errorf("failed to set request body: %w", err)
		}
	}
	return req, nil
}
