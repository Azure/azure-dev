// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/google/uuid"
)

const (
	TelephonyProviderTeamsPhoneExtensibility = "teams_phone_extensibility"
	TelephonyProviderTwilio                  = "twilio"
)

// ListTelephonyBindings retrieves one page from the canonical v1 binding collection.
func (c *AgentClient) ListTelephonyBindings(
	ctx context.Context,
	agentName string,
	options TelephonyBindingListOptions,
) (*TelephonyBindingPage, error) {
	query, err := telephonyBindingListQuery(options)
	if err != nil {
		return nil, err
	}
	var page TelephonyBindingPage
	if _, err := c.gaTelephonyRequest(
		ctx, http.MethodGet, agentName, "bindings", "", query, nil, "", "", &page,
	); err != nil {
		return nil, err
	}
	if err := validateTelephonyBindingPage(&page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ListAllTelephonyBindings follows the canonical last_id to after cursor until
// the service reports that no additional pages remain.
func (c *AgentClient) ListAllTelephonyBindings(
	ctx context.Context,
	agentName string,
	options TelephonyBindingListOptions,
) ([]GATelephonyBinding, error) {
	if options.Before != "" {
		return nil, fmt.Errorf("before is not supported when listing all telephony bindings")
	}
	bindings := []GATelephonyBinding{}
	seenCursors := map[string]struct{}{}
	for {
		page, err := c.ListTelephonyBindings(ctx, agentName, options)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, page.Data...)
		if !page.HasMore {
			return bindings, nil
		}
		if _, exists := seenCursors[page.LastID]; exists || page.LastID == options.After {
			return nil, fmt.Errorf("telephony binding pagination returned a repeated last_id cursor")
		}
		seenCursors[page.LastID] = struct{}{}
		options.After = page.LastID
	}
}

// GetGATelephonyBinding retrieves a canonical v1 binding by its opaque service ID.
func (c *AgentClient) GetGATelephonyBinding(
	ctx context.Context,
	agentName string,
	bindingID string,
) (*GATelephonyBinding, error) {
	if bindingID == "" {
		return nil, fmt.Errorf("binding ID is required")
	}
	var binding GATelephonyBinding
	etag, err := c.gaTelephonyRequest(
		ctx, http.MethodGet, agentName, "bindings", bindingID, nil, nil, "", "", &binding,
	)
	if err != nil {
		return nil, err
	}
	binding.ETag = etag
	if err := validateTelephonyBinding(&binding, true); err != nil {
		return nil, err
	}
	return &binding, nil
}

// CreateGATelephonyBinding creates a canonical v1 provider-specific binding.
func (c *AgentClient) CreateGATelephonyBinding(
	ctx context.Context,
	agentName string,
	request TelephonyBindingCreateRequest,
	repeatability TelephonyRepeatabilityOptions,
) (*GATelephonyBinding, error) {
	if request == nil || (reflect.ValueOf(request).Kind() == reflect.Ptr && reflect.ValueOf(request).IsNil()) {
		return nil, fmt.Errorf("binding request is required")
	}
	if repeatability.RequestID == "" {
		repeatability.RequestID = uuid.NewString()
	}
	if repeatability.FirstSent.IsZero() {
		repeatability.FirstSent = time.Now().UTC()
	}
	headers := http.Header{}
	headers.Set("Repeatability-Request-ID", repeatability.RequestID)
	headers.Set("Repeatability-First-Sent", repeatability.FirstSent.UTC().Format(http.TimeFormat))
	var binding GATelephonyBinding
	etag, err := c.gaTelephonyRequest(
		ctx, http.MethodPost, agentName, "bindings", "", nil, request, "application/json", "", &binding, headers,
	)
	if err != nil {
		return nil, err
	}
	binding.ETag = etag
	if err := validateTelephonyBinding(&binding, true); err != nil {
		return nil, err
	}
	return &binding, nil
}

// UpdateGATelephonyBinding updates mutable fields using optimistic concurrency.
func (c *AgentClient) UpdateGATelephonyBinding(
	ctx context.Context,
	agentName string,
	bindingID string,
	request UpdateTelephonyBindingRequest,
	ifMatch string,
) (*GATelephonyBinding, error) {
	if bindingID == "" {
		return nil, fmt.Errorf("binding ID is required")
	}
	if ifMatch == "" {
		return nil, fmt.Errorf("If-Match is required")
	}
	if request.Status != nil && *request.Status != TelephonyBindingStatusActive && *request.Status != TelephonyBindingStatusSuspended {
		return nil, fmt.Errorf("status must be active or suspended")
	}
	var binding GATelephonyBinding
	etag, err := c.gaTelephonyRequest(
		ctx, http.MethodPatch, agentName, "bindings", bindingID, nil, request,
		"application/merge-patch+json", ifMatch, &binding,
	)
	if err != nil {
		return nil, err
	}
	binding.ETag = etag
	if err := validateTelephonyBinding(&binding, true); err != nil {
		return nil, err
	}
	return &binding, nil
}

// DeleteGATelephonyBinding deletes a binding using optimistic concurrency.
func (c *AgentClient) DeleteGATelephonyBinding(
	ctx context.Context,
	agentName string,
	bindingID string,
	ifMatch string,
) error {
	if bindingID == "" {
		return fmt.Errorf("binding ID is required")
	}
	if ifMatch == "" {
		return fmt.Errorf("If-Match is required")
	}
	_, err := c.gaTelephonyRequest(
		ctx, http.MethodDelete, agentName, "bindings", bindingID, nil, nil, "", ifMatch, nil,
	)
	return err
}

// GetTelephonyTransferTargets retrieves the canonical v1 transfer-target singleton.
func (c *AgentClient) GetTelephonyTransferTargets(
	ctx context.Context,
	agentName string,
) (*TelephonyTransferTargets, error) {
	var targets TelephonyTransferTargets
	etag, err := c.gaTelephonyRequest(
		ctx, http.MethodGet, agentName, "transfer_targets", "", nil, nil, "", "", &targets,
	)
	if err != nil {
		return nil, err
	}
	targets.ETag = etag
	if targets.ETag == "" {
		return nil, fmt.Errorf("telephony response is missing required ETag")
	}
	if err := validateTelephonyTransferTargets(&targets); err != nil {
		return nil, err
	}
	return &targets, nil
}

// ReplaceTelephonyTransferTargets atomically replaces the canonical v1 singleton.
func (c *AgentClient) ReplaceTelephonyTransferTargets(
	ctx context.Context,
	agentName string,
	request TelephonyTransferTargets,
	ifMatch string,
) (*TelephonyTransferTargets, error) {
	if ifMatch == "" {
		return nil, fmt.Errorf("If-Match is required")
	}
	if err := validateTelephonyTransferTargets(&request); err != nil {
		return nil, err
	}
	request.ETag = ""
	var targets TelephonyTransferTargets
	etag, err := c.gaTelephonyRequest(
		ctx, http.MethodPut, agentName, "transfer_targets", "", nil, request, "application/json", ifMatch, &targets,
	)
	if err != nil {
		return nil, err
	}
	targets.ETag = etag
	if targets.ETag == "" {
		return nil, fmt.Errorf("telephony response is missing required ETag")
	}
	if err := validateTelephonyTransferTargets(&targets); err != nil {
		return nil, err
	}
	return &targets, nil
}

func telephonyBindingListQuery(options TelephonyBindingListOptions) (url.Values, error) {
	if options.Limit < 0 || options.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100, or omitted")
	}
	if options.Order != "" && options.Order != "asc" && options.Order != "desc" {
		return nil, fmt.Errorf("order must be asc or desc")
	}
	if options.After != "" && options.Before != "" {
		return nil, fmt.Errorf("after and before cannot both be set")
	}
	query := url.Values{}
	if options.Provider != "" {
		query.Set("provider", options.Provider)
	}
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.Limit != 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	if options.After != "" {
		query.Set("after", options.After)
	}
	if options.Before != "" {
		query.Set("before", options.Before)
	}
	return query, nil
}

func (c *AgentClient) gaTelephonyRequest(
	ctx context.Context,
	method string,
	agentName string,
	collection string,
	resourceID string,
	query url.Values,
	body any,
	contentType string,
	ifMatch string,
	result any,
	extraHeaders ...http.Header,
) (string, error) {
	if agentName == "" {
		return "", fmt.Errorf("agent name is required")
	}
	endpoint := strings.TrimRight(c.endpoint, "/") + "/agents/" + escapeTelephonyPathSegment(agentName) + "/telephony/" + collection
	if resourceID != "" {
		endpoint += "/" + escapeTelephonyPathSegment(resourceID)
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("api-version", AgentEndpointAPIVersion)

	// Conditional writes must not be replayed automatically after a lost response.
	if method == http.MethodPatch || method == http.MethodDelete || method == http.MethodPut {
		ctx = runtime.WithRetryOptions(ctx, policy.RetryOptions{MaxRetries: -1})
	}
	req, err := runtime.NewRequest(ctx, method, endpoint+"?"+query.Encode())
	if err != nil {
		return "", fmt.Errorf("creating telephony request: %w", err)
	}
	if ifMatch != "" {
		req.Raw().Header.Set("If-Match", ifMatch)
	}
	for _, headers := range extraHeaders {
		for name, values := range headers {
			for _, value := range values {
				req.Raw().Header.Add(name, value)
			}
		}
	}
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("encoding telephony request: %w", err)
		}
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), contentType); err != nil {
			return "", fmt.Errorf("setting telephony request body: %w", err)
		}
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending telephony request: %w", err)
	}
	defer resp.Body.Close()
	expected := http.StatusOK
	if method == http.MethodPost {
		expected = http.StatusCreated
	} else if method == http.MethodDelete {
		expected = http.StatusNoContent
	}
	if resp.StatusCode != expected {
		return "", runtime.NewResponseError(resp)
	}
	if result != nil {
		decoder := json.NewDecoder(resp.Body)
		if err := decoder.Decode(result); err != nil {
			if err == io.EOF {
				return "", fmt.Errorf("telephony response body is required")
			}
			return "", fmt.Errorf("invalid telephony response JSON: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return "", fmt.Errorf("invalid telephony response JSON: multiple values")
		}
	}
	return resp.Header.Get("ETag"), nil
}

func validateTelephonyBindingPage(page *TelephonyBindingPage) error {
	if page.Data == nil {
		return fmt.Errorf("telephony binding page is missing required data")
	}
	for i := range page.Data {
		if err := validateTelephonyBinding(&page.Data[i], true); err != nil {
			return fmt.Errorf("invalid telephony binding at index %d: %w", i, err)
		}
	}
	if page.HasMore && page.LastID == "" {
		return fmt.Errorf("telephony binding page with has_more=true is missing last_id")
	}
	return nil
}

func validateTelephonyBinding(binding *GATelephonyBinding, requireETag bool) error {
	if binding.ID == "" || binding.Provider == "" || binding.ConnectionName == "" ||
		binding.Status == "" || binding.IncomingCallURL == "" {
		return fmt.Errorf("telephony response is missing required binding fields")
	}
	if requireETag && binding.ETag == "" {
		return fmt.Errorf("telephony response is missing required ETag")
	}
	if binding.Status != TelephonyBindingStatusActive && binding.Status != TelephonyBindingStatusSuspended {
		return fmt.Errorf("telephony response contains invalid status %q", binding.Status)
	}
	if binding.Provider == TelephonyProviderTwilio && binding.PhoneNumber == "" {
		return fmt.Errorf("Twilio telephony response is missing phone_number")
	}
	if binding.Provider == TelephonyProviderTeamsPhoneExtensibility && binding.ResourceAccountObjectID == "" {
		return fmt.Errorf("Teams telephony response is missing resource_account_object_id")
	}
	return nil
}

func validateTelephonyTransferTargets(targets *TelephonyTransferTargets) error {
	if targets.TransferTargets == nil {
		return fmt.Errorf("telephony response is missing required transfer_targets")
	}
	for i, target := range targets.TransferTargets {
		if target.Name == "" || target.Description == "" || target.Destination.Value == "" {
			return fmt.Errorf("invalid telephony transfer target at index %d: required fields are missing", i)
		}
		switch target.Destination.Kind {
		case TelephonyTransferDestinationPSTN, TelephonyTransferDestinationTeams, TelephonyTransferDestinationSIP:
		default:
			return fmt.Errorf("invalid telephony transfer target at index %d: unsupported destination kind %q", i, target.Destination.Kind)
		}
	}
	return nil
}
