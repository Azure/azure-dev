// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

const stateStoresPreviewFeature = "StateStores=V1Preview"

// StateStoreWriteOutcomeUnknownError indicates that a write request did not
// produce a trustworthy service result. The service might have committed it.
type StateStoreWriteOutcomeUnknownError struct {
	Err error
}

func (e *StateStoreWriteOutcomeUnknownError) Error() string {
	if e == nil || e.Err == nil {
		return "State Store write outcome could not be confirmed"
	}
	return e.Err.Error()
}

func (e *StateStoreWriteOutcomeUnknownError) Unwrap() error {
	return e.Err
}

// ErrStateStoreValueTooLarge indicates that a value exceeds the documented preview size limit.
var ErrStateStoreValueTooLarge = fmt.Errorf(
	"item value exceeds the serialized JSON limit of %d bytes (1 MiB)", MaxStateStoreValueBytes,
)

// ValidateStateStoreValue validates an opaque JSON object's type and serialized size
// without converting its numbers.
func ValidateStateStoreValue(value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if !json.Valid(trimmed) || len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("item value must be a JSON object")
	}
	// Match the compaction and HTML escaping used by json.Marshal in stateStoreRequest.
	// The service limit applies to the value, not the surrounding request or tags.
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding item value: %w", err)
	}
	if len(encoded) > MaxStateStoreValueBytes {
		return ErrStateStoreValueTooLarge
	}
	return nil
}

// ListStateStores retrieves one page of stores for an agent.
func (c *AgentClient) ListStateStores(
	ctx context.Context, agentName string, options StateStoreListOptions,
) (*StateStorePage[StateStore], error) {
	query, err := stateStoreListQuery(options)
	if err != nil {
		return nil, err
	}
	var result StateStorePage[StateStore]
	if err := c.stateStoreRequest(ctx, http.MethodGet, agentName, "", "", "", query, nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetStateStore retrieves an existing store by its unencoded logical name.
func (c *AgentClient) GetStateStore(ctx context.Context, agentName, store string) (*StateStore, error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	var result StateStore
	if err := c.stateStoreRequest(ctx, http.MethodGet, agentName, store, "", "", nil, nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListStateStoreItemKeys retrieves one page of item keys and metadata, not values.
func (c *AgentClient) ListStateStoreItemKeys(
	ctx context.Context, agentName, store string, options StateStoreListOptions,
) (*StateStorePage[StateStoreItem], error) {
	if store == "" {
		return nil, fmt.Errorf("store name is required")
	}
	query, err := stateStoreListQuery(options)
	if err != nil {
		return nil, err
	}
	var result StateStorePage[StateStoreItem]
	if err := c.stateStoreRequest(
		ctx, http.MethodGet, agentName, store, "items:keys", "", query, nil, "", &result,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetStateStoreItem retrieves an item's full JSON value and ETag.
func (c *AgentClient) GetStateStoreItem(ctx context.Context, agentName, store, key string) (*StateStoreItem, error) {
	var result StateStoreItem
	if err := c.stateStoreRequest(ctx, http.MethodGet, agentName, store, "items", key, nil, nil, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetStateStoreItem creates or replaces an item using one PUT, optionally conditional on an ETag.
func (c *AgentClient) SetStateStoreItem(
	ctx context.Context, agentName, store, key string, value SetStateStoreItemRequest, ifMatch string,
) (*StateStoreItem, error) {
	if err := ValidateStateStoreValue(value.Value); err != nil {
		return nil, err
	}
	var result StateStoreItem
	if err := c.stateStoreRequest(
		ctx, http.MethodPut, agentName, store, "items", key, nil, value, ifMatch, &result,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteStateStoreItem deletes an item, optionally conditional on an ETag.
func (c *AgentClient) DeleteStateStoreItem(
	ctx context.Context, agentName, store, key, ifMatch string,
) (*DeletedStateStoreItem, error) {
	var result DeletedStateStoreItem
	if err := c.stateStoreRequest(
		ctx, http.MethodDelete, agentName, store, "items", key, nil, nil, ifMatch, &result,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

func stateStoreListQuery(options StateStoreListOptions) (url.Values, error) {
	if options.Limit < 0 || options.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100, or omitted")
	}
	if options.Order != "" && options.Order != "asc" && options.Order != "desc" {
		return nil, fmt.Errorf("order must be asc or desc")
	}
	query := url.Values{}
	if options.Limit != 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	if options.After != "" {
		query.Set("after", options.After)
	}
	return query, nil
}

func (c *AgentClient) stateStoreRequest(
	ctx context.Context, method, agentName, store, collection, key string,
	query url.Values, body any, ifMatch string, result any,
) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if collection != "" && store == "" {
		return fmt.Errorf("store name is required")
	}
	if collection == "items" && key == "" {
		return fmt.Errorf("item key is required")
	}

	endpoint := strings.TrimRight(c.endpoint, "/") + "/agents/" + url.PathEscape(agentName) + "/endpoint/state_stores"
	if store != "" {
		endpoint += "/" + base64.RawURLEncoding.EncodeToString([]byte(store))
	}
	if collection != "" {
		endpoint += "/" + collection
	}
	if key != "" {
		endpoint += "/" + base64.RawURLEncoding.EncodeToString([]byte(key))
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("api-version", AgentEndpointAPIVersion)

	// Replaying a successful write after a lost response could overwrite another writer
	// or turn a successful conditional write into a misleading precondition failure.
	if method != http.MethodGet {
		ctx = runtime.WithRetryOptions(ctx, policy.RetryOptions{MaxRetries: -1})
	}
	req, err := runtime.NewRequest(ctx, method, endpoint+"?"+query.Encode())
	if err != nil {
		return fmt.Errorf("creating state store request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", stateStoresPreviewFeature)
	if ifMatch != "" {
		req.Raw().Header.Set("If-Match", ifMatch)
	}
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding state store request: %w", err)
		}
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(data)), "application/json"); err != nil {
			return fmt.Errorf("setting state store request body: %w", err)
		}
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		err = fmt.Errorf("sending state store request: %w", err)
		if method != http.MethodGet {
			return &StateStoreWriteOutcomeUnknownError{Err: err}
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && !(method == http.MethodPut && resp.StatusCode == http.StatusCreated) {
		// Service error bodies may echo item values. Do not pass them to NewResponseError,
		// which also logs the body independently of the pipeline's IncludeBody setting.
		return &azcore.ResponseError{
			StatusCode: resp.StatusCode,
			RawResponse: &http.Response{
				StatusCode: resp.StatusCode,
				Status:     http.StatusText(resp.StatusCode),
				Header:     http.Header{"X-Ms-Request-Id": resp.Header.Values("X-Ms-Request-Id")},
				Body:       http.NoBody,
			},
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A JSON decoding error can include customer-authored property names.
		err = fmt.Errorf("invalid state store response JSON")
		if method != http.MethodGet {
			return &StateStoreWriteOutcomeUnknownError{Err: err}
		}
		return err
	}
	return nil
}
