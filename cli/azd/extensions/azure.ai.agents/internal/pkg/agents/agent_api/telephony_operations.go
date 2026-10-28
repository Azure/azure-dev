// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/google/uuid"
)

const (
	TelephonyProviderTeamsPhoneExtensibility = "teams_phone_extensibility"
	TelephonyProviderTwilio                  = "twilio"
	telephonyAPIVersion                      = "v1"
	maxTelephonyResponseSize                 = 4 * 1024 * 1024
	maxTelephonyErrorResponseSize            = 64 * 1024
)

type telephonyResponseLimitTransport struct {
	next policy.Transporter
}

func (t *telephonyResponseLimitTransport) Do(req *http.Request) (*http.Response, error) {
	resp, err := t.next.Do(req)
	if err != nil || resp == nil || resp.Body == nil || !isCanonicalTelephonyPath(req.URL.Path) {
		return resp, err
	}
	limit := int64(maxTelephonyResponseSize + 1)
	if resp.StatusCode >= http.StatusBadRequest {
		limit = int64(maxTelephonyErrorResponseSize + 1)
	}
	resp.Body = &limitedResponseBody{Reader: io.LimitReader(resp.Body, limit), closer: resp.Body}
	return resp, nil
}

type limitedResponseBody struct {
	io.Reader
	closer io.Closer
}

func (b *limitedResponseBody) Close() error {
	return b.closer.Close()
}

func isCanonicalTelephonyPath(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+1 < len(segments); i++ {
		if segments[i] != "telephony" {
			continue
		}
		if segments[i+1] == "bindings" || segments[i+1] == "transfer_targets" {
			return true
		}
	}
	return false
}

// TelephonyWriteOutcomeUnknownError indicates that a write request may have
// committed even though the client could not confirm the final service result.
type TelephonyWriteOutcomeUnknownError struct {
	Err error
}

func (e *TelephonyWriteOutcomeUnknownError) Error() string {
	return "telephony write may have committed, but the outcome could not be confirmed"
}

func (e *TelephonyWriteOutcomeUnknownError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var (
	telephonyPhoneNumberPattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	telephonyTargetNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
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
		if !*page.HasMore {
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
	if binding.ID != bindingID {
		return nil, fmt.Errorf("telephony response ID does not match the requested binding")
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
	if request == nil || (reflect.ValueOf(request).Kind() == reflect.Pointer && reflect.ValueOf(request).IsNil()) {
		return nil, fmt.Errorf("binding request is required")
	}
	switch typed := request.(type) {
	case *CreateTwilioTelephonyBindingRequest:
		if typed.ConnectionName == "" || typed.PhoneNumber == "" {
			return nil, fmt.Errorf("Twilio connection name and phone number are required")
		}
		if err := validateConnectionName(typed.ConnectionName); err != nil {
			return nil, err
		}
		if !telephonyPhoneNumberPattern.MatchString(typed.PhoneNumber) {
			return nil, fmt.Errorf("Twilio phone number must be a valid E.164 number")
		}
		if err := validateTelephonyLabel(typed.Label); err != nil {
			return nil, err
		}
	case *CreateTeamsPhoneTelephonyBindingRequest:
		if typed.ConnectionName == "" || typed.ResourceAccountObjectID == "" {
			return nil, fmt.Errorf("Teams connection name and resource account object ID are required")
		}
		if err := validateConnectionName(typed.ConnectionName); err != nil {
			return nil, err
		}
		if _, err := uuid.Parse(typed.ResourceAccountObjectID); err != nil {
			return nil, fmt.Errorf("Teams resource account object ID must be a UUID")
		}
		if utf8.RuneCountInString(typed.PhoneNumber) > 64 {
			return nil, fmt.Errorf("Teams display phone number cannot exceed 64 characters")
		}
		if !utf8.ValidString(typed.PhoneNumber) {
			return nil, fmt.Errorf("Teams phone number must be valid UTF-8")
		}
		if err := validateTelephonyLabel(typed.Label); err != nil {
			return nil, err
		}
	}
	if repeatability.RequestID == "" {
		return nil, fmt.Errorf("repeatability request ID is required")
	}
	if _, err := uuid.Parse(repeatability.RequestID); err != nil {
		return nil, fmt.Errorf("repeatability request ID must be a UUID")
	}
	if repeatability.FirstSent.IsZero() {
		return nil, fmt.Errorf("repeatability first sent time is required")
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
		return nil, telephonyWriteOutcomeUnknown(err)
	}
	if err := validateCreateResponseIdentity(&binding, request); err != nil {
		return nil, telephonyWriteOutcomeUnknown(err)
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
	if request.Status != nil &&
		*request.Status != TelephonyBindingStatusActive &&
		*request.Status != TelephonyBindingStatusSuspended {
		return nil, fmt.Errorf("status must be active or suspended")
	}
	if request.ConnectionName != nil {
		if err := validateConnectionName(*request.ConnectionName); err != nil {
			return nil, err
		}
	}
	if request.Label != nil && request.Label.Value != nil {
		if err := validateTelephonyLabel(*request.Label.Value); err != nil {
			return nil, err
		}
	}
	if request.PhoneNumber != nil && request.PhoneNumber.Value != nil {
		value := *request.PhoneNumber.Value
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("phone number must be valid UTF-8")
		}
		if utf8.RuneCountInString(value) > 64 {
			return nil, fmt.Errorf("phone number cannot exceed 64 characters")
		}
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
		return nil, telephonyWriteOutcomeUnknown(err)
	}
	if binding.ID != bindingID {
		return nil, telephonyWriteOutcomeUnknown(
			fmt.Errorf("telephony response ID does not match the requested binding"))
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
	if err := validateTelephonyTransferTargets(&targets, "response", false); err != nil {
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
	if err := validateTelephonyTransferTargets(&request, "request", true); err != nil {
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
		return nil, telephonyWriteOutcomeUnknown(fmt.Errorf("telephony response is missing required ETag"))
	}
	if err := validateTelephonyTransferTargets(&targets, "response", false); err != nil {
		return nil, telephonyWriteOutcomeUnknown(err)
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
	if options.Provider != "" &&
		options.Provider != TelephonyProviderTwilio &&
		options.Provider != TelephonyProviderTeamsPhoneExtensibility {
		return nil, fmt.Errorf("provider must be twilio or teams_phone_extensibility")
	}
	if options.Status != "" &&
		options.Status != TelephonyBindingStatusActive &&
		options.Status != TelephonyBindingStatusSuspended {
		return nil, fmt.Errorf("status must be active or suspended")
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
	if isDotPathSegment(agentName) || isDotPathSegment(resourceID) {
		return "", fmt.Errorf("agent name and resource ID cannot be dot path segments")
	}
	endpoint := strings.TrimRight(c.endpoint, "/") +
		"/agents/" + escapeTelephonyPathSegment(agentName) +
		"/telephony/" + collection
	if resourceID != "" {
		endpoint += "/" + escapeTelephonyPathSegment(resourceID)
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("api-version", telephonyAPIVersion)

	// Telephony calls use explicit caller retries. This avoids hidden write replay
	// and prevents the SDK retry policy from draining untrusted response bodies.
	ctx = runtime.WithRetryOptions(ctx, policy.RetryOptions{MaxRetries: -1})
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

	resp, err := c.telephonyPipeline.Do(req)
	if err != nil {
		if method != http.MethodGet {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", telephonyWriteOutcomeUnknown(ctxErr)
			}
			return "", telephonyWriteOutcomeUnknown(err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("sending telephony request failed")
	}
	defer resp.Body.Close()
	expected := http.StatusOK
	if method == http.MethodPost {
		expected = http.StatusCreated
	} else if method == http.MethodDelete {
		expected = http.StatusNoContent
	}
	if resp.StatusCode != expected {
		_, _ = io.Copy(io.Discard, resp.Body)
		err := &azcore.ResponseError{
			StatusCode: resp.StatusCode,
			RawResponse: &http.Response{
				StatusCode: resp.StatusCode,
				Status:     http.StatusText(resp.StatusCode),
				Header:     http.Header{"X-Ms-Request-Id": resp.Header.Values("X-Ms-Request-Id")},
				Body:       http.NoBody,
			},
		}
		if method != http.MethodGet && isUnknownWriteStatus(resp.StatusCode) {
			return "", telephonyWriteOutcomeUnknown(err)
		}
		return "", err
	}
	if result != nil {
		payload, err := io.ReadAll(io.LimitReader(resp.Body, maxTelephonyResponseSize+1))
		if err != nil {
			if method != http.MethodGet {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return "", telephonyWriteOutcomeUnknown(ctxErr)
				}
				return "", telephonyWriteOutcomeUnknown(err)
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			return "", fmt.Errorf("reading telephony response body failed")
		}
		if len(payload) > maxTelephonyResponseSize {
			err = fmt.Errorf("telephony response body exceeds the size limit")
			if method != http.MethodGet {
				return "", telephonyWriteOutcomeUnknown(err)
			}
			return "", err
		}
		if !utf8.Valid(payload) {
			err = fmt.Errorf("telephony response body is not valid UTF-8")
			if method != http.MethodGet {
				return "", telephonyWriteOutcomeUnknown(err)
			}
			return "", err
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		if err := decoder.Decode(result); err != nil {
			if errors.Is(err, io.EOF) {
				err = fmt.Errorf("telephony response body is required")
				if method != http.MethodGet {
					return "", telephonyWriteOutcomeUnknown(err)
				}
				return "", err
			}
			err = fmt.Errorf("invalid telephony response JSON: %w", err)
			if method != http.MethodGet {
				return "", telephonyWriteOutcomeUnknown(err)
			}
			return "", err
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			err = fmt.Errorf("invalid telephony response JSON: multiple values")
			if method != http.MethodGet {
				return "", telephonyWriteOutcomeUnknown(err)
			}
			return "", err
		}
	}
	return resp.Header.Get("ETag"), nil
}

func isDotPathSegment(value string) bool {
	return value == "." || value == ".."
}

func validateTelephonyBindingPage(page *TelephonyBindingPage) error {
	if page.Data == nil {
		return fmt.Errorf("telephony binding page is missing required data")
	}
	if page.HasMore == nil {
		return fmt.Errorf("telephony binding page is missing required has_more")
	}
	for i := range page.Data {
		if err := validateTelephonyBinding(&page.Data[i], true); err != nil {
			return fmt.Errorf("invalid telephony binding at index %d: %w", i, err)
		}
	}
	if *page.HasMore && page.LastID == "" {
		return fmt.Errorf("telephony binding page with has_more=true is missing last_id")
	}
	return nil
}

func validateTelephonyBinding(binding *GATelephonyBinding, requireETag bool) error {
	if binding.ID == "" || binding.Provider == "" || binding.ConnectionName == "" || binding.IncomingCallURL == "" {
		return fmt.Errorf("telephony response is missing required binding fields")
	}
	if requireETag && binding.ETag == "" {
		return fmt.Errorf("telephony response is missing required ETag")
	}
	if binding.Status == "" {
		return fmt.Errorf("telephony response is missing required status")
	}
	if !isHTTPURL(binding.IncomingCallURL) {
		return fmt.Errorf("telephony response contains invalid incoming_call_url")
	}
	switch binding.Provider {
	case TelephonyProviderTwilio:
		if binding.PhoneNumber == "" {
			return fmt.Errorf("Twilio telephony response is missing phone_number")
		}
		if !telephonyPhoneNumberPattern.MatchString(binding.PhoneNumber) {
			return fmt.Errorf("Twilio telephony response contains invalid phone_number")
		}
	case TelephonyProviderTeamsPhoneExtensibility:
		if binding.ResourceAccountObjectID == "" {
			return fmt.Errorf("Teams telephony response is missing resource_account_object_id")
		}
		if _, err := uuid.Parse(binding.ResourceAccountObjectID); err != nil {
			return fmt.Errorf("Teams telephony response contains invalid resource_account_object_id")
		}
	}
	return nil
}

func telephonyWriteOutcomeUnknown(err error) error {
	return &TelephonyWriteOutcomeUnknownError{Err: err}
}

func validateTelephonyTransferTargets(
	targets *TelephonyTransferTargets,
	source string,
	strictKinds bool,
) error {
	if targets.TransferTargets == nil {
		return fmt.Errorf("telephony %s is missing required transfer_targets", source)
	}
	names := map[string]struct{}{}
	for i, target := range targets.TransferTargets {
		if !utf8.ValidString(target.Name) || !utf8.ValidString(target.Description) {
			return fmt.Errorf("invalid telephony %s transfer target at index %d: strings must be valid UTF-8", source, i)
		}
		if target.Name == "" || target.Description == "" || target.Destination.Kind == "" {
			return fmt.Errorf("invalid telephony %s transfer target at index %d: required fields are missing", source, i)
		}
		if !telephonyTargetNamePattern.MatchString(target.Name) {
			return fmt.Errorf("invalid telephony %s transfer target at index %d: invalid name", source, i)
		}
		if _, exists := names[target.Name]; exists {
			return fmt.Errorf("invalid telephony %s transfer targets: duplicate name", source)
		}
		names[target.Name] = struct{}{}
		if utf8.RuneCountInString(target.Description) > 1024 {
			return fmt.Errorf("invalid telephony %s transfer target at index %d: description is too long", source, i)
		}
		switch target.Destination.Kind {
		case TelephonyTransferDestinationPSTN:
			if target.Destination.Value == "" {
				return fmt.Errorf(
					"invalid telephony %s transfer target at index %d: required fields are missing",
					source,
					i,
				)
			}
			if !telephonyPhoneNumberPattern.MatchString(target.Destination.Value) {
				return fmt.Errorf("invalid telephony %s transfer target at index %d: invalid PSTN number", source, i)
			}
		case TelephonyTransferDestinationTeams:
			if target.Destination.Value == "" {
				return fmt.Errorf(
					"invalid telephony %s transfer target at index %d: required fields are missing",
					source,
					i,
				)
			}
			if !utf8.ValidString(target.Destination.Value) {
				return fmt.Errorf(
					"invalid telephony %s transfer target at index %d: Teams value is not valid UTF-8",
					source,
					i,
				)
			}
			if utf8.RuneCountInString(target.Destination.Value) > 256 {
				return fmt.Errorf("invalid telephony %s transfer target at index %d: Teams value is too long", source, i)
			}
		case TelephonyTransferDestinationSIP:
			if target.Destination.Value == "" {
				return fmt.Errorf(
					"invalid telephony %s transfer target at index %d: required fields are missing",
					source,
					i,
				)
			}
			if !isSIPUri(target.Destination.Value) {
				return fmt.Errorf("invalid telephony %s transfer target at index %d: invalid SIP URI", source, i)
			}
		default:
			if strictKinds {
				return fmt.Errorf(
					"invalid telephony %s transfer target at index %d: unsupported destination kind",
					source,
					i,
				)
			}
		}
	}
	return nil
}

// ValidateTelephonyTransferTargetsForWrite validates a complete transfer-target
// singleton using the same rules as ReplaceTelephonyTransferTargets.
func ValidateTelephonyTransferTargetsForWrite(targets []TelephonyTransferTarget) error {
	return validateTelephonyTransferTargets(&TelephonyTransferTargets{TransferTargets: targets}, "request", true)
}

func validateConnectionName(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("connection name must be valid UTF-8")
	}
	if count := utf8.RuneCountInString(value); count < 1 || count > 256 {
		return fmt.Errorf("connection name must be between 1 and 256 characters")
	}
	return nil
}

func validateTelephonyLabel(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("label must be valid UTF-8")
	}
	if utf8.RuneCountInString(value) > 128 {
		return fmt.Errorf("label cannot exceed 128 characters")
	}
	return nil
}

func isHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Host != ""
}

func isSIPUri(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	if strings.ContainsAny(value, " \t\r\n") || hasMalformedPercentEscape(value) {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed == nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "sip" || scheme == "sips"
}

func hasMalformedPercentEscape(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			continue
		}
		if i+2 >= len(value) || !isHex(value[i+1]) || !isHex(value[i+2]) {
			return true
		}
		i += 2
	}
	return false
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func isUnknownWriteStatus(status int) bool {
	return (status >= 200 && status < 300) ||
		status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= 500
}

func telephonyCreateRequestProvider(request TelephonyBindingCreateRequest) string {
	switch request.(type) {
	case *CreateTwilioTelephonyBindingRequest:
		return TelephonyProviderTwilio
	case *CreateTeamsPhoneTelephonyBindingRequest:
		return TelephonyProviderTeamsPhoneExtensibility
	default:
		return ""
	}
}

func validateCreateResponseIdentity(
	binding *GATelephonyBinding,
	request TelephonyBindingCreateRequest,
) error {
	if binding.Provider != telephonyCreateRequestProvider(request) {
		return fmt.Errorf("telephony create response provider does not match the request")
	}
	switch typed := request.(type) {
	case *CreateTwilioTelephonyBindingRequest:
		if binding.ConnectionName != typed.ConnectionName || binding.PhoneNumber != typed.PhoneNumber {
			return fmt.Errorf("Twilio create response identity does not match the request")
		}
	case *CreateTeamsPhoneTelephonyBindingRequest:
		requestID, requestErr := uuid.Parse(typed.ResourceAccountObjectID)
		responseID, responseErr := uuid.Parse(binding.ResourceAccountObjectID)
		if binding.ConnectionName != typed.ConnectionName || requestErr != nil || responseErr != nil ||
			requestID != responseID {
			return fmt.Errorf("Teams create response identity does not match the request")
		}
	}
	return nil
}
