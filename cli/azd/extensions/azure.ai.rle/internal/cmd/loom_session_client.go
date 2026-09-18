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
)

// The Loom Fine Tuning Sessions API is fronted by the same Foundry project endpoint as
// the RLE service. Routes, headers, and the async submit-then-poll pattern below are
// taken directly from the azure-ai-finetuning-sessions SDK
// (azure/ai/finetuning_sessions/_patch.py: FineTuningSession.create,
// save_weights_for_sampler, close; _base_headers) and from vienna's Loom rollout binding
// helper (src/azureml-api/src/RLE/CaptureProxy/benchmarks/loom-parity/{loom_binding.py,
// provision_rollout_binding.py}, added in PR 2308289). This is a Development/preview-only
// surface: do not fabricate session or checkpoint identifiers, and always close the
// session once its rollouts are done so the model allocation is released.
const (
	loomSessionsPath        = "/fine_tuning/sessions"
	loomAPIVersion          = "v1"
	loomFoundryFeatureValue = "FinetuningSessionsV1Preview"
	loomTokenScope          = "https://ai.azure.com/.default" //nolint:gosec // OAuth scope, not a credential
	loomPollMinInterval     = 1 * time.Second
	loomPollMaxInterval     = 30 * time.Second
)

type loomSessionClient struct {
	baseUrl    string
	credential azcore.TokenCredential
	httpClient *http.Client
}

var createLoomSessionClient = newLoomSessionClient

func newLoomSessionClient(endpoint string) (*loomSessionClient, error) {
	normalizedEndpoint, err := normalizeFoundryProjectEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}
	return newLoomSessionClientWithCredential(normalizedEndpoint, credential), nil
}

func newLoomSessionClientWithCredential(endpoint string, credential azcore.TokenCredential) *loomSessionClient {
	return &loomSessionClient{
		baseUrl:    strings.TrimRight(endpoint, "/"),
		credential: credential,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type loomCreateSessionRequest struct {
	Type       string          `json:"type"`
	BaseModel  string          `json:"base_model"`
	LoraConfig *loomLoraConfig `json:"lora_config,omitempty"`
}

type loomLoraConfig struct {
	Rank int `json:"rank,omitempty"`
}

type loomSaveSamplerWeightsRequest struct {
	Path  string `json:"path,omitempty"`
	SeqID *int   `json:"seq_id,omitempty"`
}

// loomOperationEnvelope is the {status, result, error} shape returned by the
// retrieve-status endpoint, GET /fine_tuning/sessions/{id}/request/{requestId}.
type loomOperationEnvelope struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *loomAPIError   `json:"error,omitempty"`
}

type loomAPIError struct {
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

type loomHTTPError struct {
	statusCode int
	body       string
}

func (e *loomHTTPError) Error() string {
	return fmt.Sprintf("Loom fine-tuning sessions API returned HTTP %d: %s", e.statusCode, strings.TrimSpace(e.body))
}

// canonicalLoomSessionID mirrors the SDK's _canonical_session_id: the "session_..." form
// used as the loom_session_id sent to Execute Rollout / Capture Proxy.
func canonicalLoomSessionID(raw string) string {
	if strings.HasPrefix(raw, "session_") {
		return raw
	}
	return "session_" + strings.TrimPrefix(raw, "model_")
}

// resourceLoomSessionID mirrors the SDK's _resource_session_id: the form used in Loom
// Sessions API request paths.
func resourceLoomSessionID(raw string) string {
	if strings.HasPrefix(raw, "session_") || strings.HasPrefix(raw, "model_") {
		return raw
	}
	return "model_" + raw
}

// createSession creates a Loom training session for baseModel and waits for the model
// load to complete, returning the canonical session ID. Mirrors
// FineTuningSession.create in the Sessions SDK.
func (c *loomSessionClient) createSession(
	ctx context.Context,
	baseModel string,
	loraRank int,
	timeout time.Duration,
) (string, error) {
	request := loomCreateSessionRequest{
		Type:      "training",
		BaseModel: baseModel,
	}
	if loraRank > 0 {
		request.LoraConfig = &loomLoraConfig{Rank: loraRank}
	}

	var created struct {
		SessionID string `json:"session_id"`
		RequestID string `json:"request_id"`
	}
	if err := c.do(ctx, http.MethodPost, loomSessionsPath, request, &created); err != nil {
		return "", err
	}
	if strings.TrimSpace(created.SessionID) == "" || strings.TrimSpace(created.RequestID) == "" {
		return "", errors.New("Loom did not return a session_id and request_id")
	}

	resourceSessionID := resourceLoomSessionID(created.SessionID)
	if err := c.pollUntilComplete(ctx, resourceSessionID, created.RequestID, timeout); err != nil {
		return "", err
	}
	return canonicalLoomSessionID(created.SessionID), nil
}

// saveWeightsForSampler pushes the session's current LoRA weights to the sampler and
// returns the sampler checkpoint ID. Passing an explicit checkpointName makes the
// checkpoint ID deterministic client-side, matching
// FineTuningSession.save_weights_for_sampler's `path or f"ss{...}_seq{...}"` formula
// with an explicit path.
func (c *loomSessionClient) saveWeightsForSampler(
	ctx context.Context,
	sessionID string,
	seqID int,
	checkpointName string,
	timeout time.Duration,
) (string, error) {
	resourceSessionID := resourceLoomSessionID(sessionID)
	request := loomSaveSamplerWeightsRequest{
		Path:  checkpointName,
		SeqID: &seqID,
	}

	var created struct {
		SessionID string `json:"session_id"`
		RequestID string `json:"request_id"`
	}
	path := fmt.Sprintf("%s/%s/checkpoint_sample", loomSessionsPath, url.PathEscape(resourceSessionID))
	if err := c.do(ctx, http.MethodPost, path, request, &created); err != nil {
		return "", err
	}
	if strings.TrimSpace(created.RequestID) == "" {
		return "", errors.New("Loom did not return a request_id for the sampler checkpoint")
	}

	pollSessionID := resourceSessionID
	if strings.TrimSpace(created.SessionID) != "" {
		pollSessionID = resourceLoomSessionID(created.SessionID)
	}
	if err := c.pollUntilComplete(ctx, pollSessionID, created.RequestID, timeout); err != nil {
		return "", err
	}
	return checkpointName, nil
}

// closeSession unloads the session from the GPU engine, releasing its model
// allocation. Errors are returned so callers can decide how to report a failed cleanup.
func (c *loomSessionClient) closeSession(ctx context.Context, sessionID string) error {
	resourceSessionID := resourceLoomSessionID(sessionID)
	path := fmt.Sprintf("%s/%s/complete", loomSessionsPath, url.PathEscape(resourceSessionID))
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

func (c *loomSessionClient) pollUntilComplete(
	ctx context.Context,
	resourceSessionID string,
	requestID string,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	backoff := loomPollMinInterval
	path := fmt.Sprintf(
		"%s/%s/request/%s",
		loomSessionsPath,
		url.PathEscape(resourceSessionID),
		url.PathEscape(requestID),
	)
	for {
		var envelope loomOperationEnvelope
		if err := c.do(ctx, http.MethodGet, path, nil, &envelope); err != nil {
			return err
		}
		switch envelope.Status {
		case "completed":
			return nil
		case "failed":
			if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
				return fmt.Errorf("Loom operation failed: %s", envelope.Error.Message)
			}
			return errors.New("Loom operation failed")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Loom operation did not complete within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > loomPollMaxInterval {
			backoff = loomPollMaxInterval
		}
	}
}

func (c *loomSessionClient) do(ctx context.Context, method string, path string, body any, target any) error {
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
	query.Set("api-version", loomAPIVersion)
	requestUrl.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, requestUrl.String(), reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return errors.New("Loom fine-tuning sessions API authentication requires an HTTPS Foundry project endpoint")
	}
	authorization, err := c.authorizationHeader(ctx)
	if err != nil {
		return fmt.Errorf("authenticate to Loom fine-tuning sessions API: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Foundry-Features", loomFoundryFeatureValue)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call Loom fine-tuning sessions API %s: %w", c.baseUrl, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read Loom fine-tuning sessions API response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &loomHTTPError{statusCode: resp.StatusCode, body: string(respBody)}
	}

	if target == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, target); err != nil {
		return fmt.Errorf("decode Loom fine-tuning sessions API response: %w", err)
	}
	return nil
}

func (c *loomSessionClient) authorizationHeader(ctx context.Context) (string, error) {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{loomTokenScope},
	})
	if err != nil {
		return "", err
	}
	return "Bearer " + token.Token, nil
}

// bearerToken returns the raw (unprefixed) access token, used for the aml-user-token
// header forwarded to Execute Rollout / Capture Proxy.
func (c *loomSessionClient) bearerToken(ctx context.Context) (string, error) {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{loomTokenScope},
	})
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

func loomServiceErrorFor(operation string, err error) error {
	message := fmt.Sprintf("%s: %v", operation, err)
	var httpErr *loomHTTPError
	if errors.As(err, &httpErr) {
		message = fmt.Sprintf("%s: Loom fine-tuning sessions API returned HTTP %d: %s",
			operation, httpErr.statusCode, strings.TrimSpace(httpErr.body))
	}
	return errors.New(message)
}
