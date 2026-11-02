// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package curestclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type operationPoller[T any] struct {
	client      *Client
	location    string
	done        bool
	failed      bool
	code        string
	status      string
	details     []OperationDiagnostic
	operationID string
	response    *http.Response
	payload     []byte
}

func newOperationPoller[T any](initial *http.Response, client *Client) (*runtime.Poller[T], error) {
	handler := &operationPoller[T]{client: client}
	if location := initial.Header.Get("Operation-Location"); location != "" {
		base := client.endpoint
		if initial.Request != nil {
			base = initial.Request.URL.String()
		}
		resolved, err := client.operationLocation(location, base)
		if err != nil {
			return nil, err
		}
		handler.location = resolved
		locationURL, err := url.Parse(resolved)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		handler.operationID = locationURL.Path[strings.LastIndex(locationURL.Path, "/")+1:]
		if handler.operationID == "" {
			return nil, ErrInvalidResponse
		}
	}
	if err := handler.accept(initial, false); err != nil {
		return nil, err
	}
	return runtime.NewPoller[T](initial, client.internal.Pipeline(), &runtime.NewPollerOptions[T]{Handler: handler})
}

func (p *operationPoller[T]) Done() bool {
	return p.done
}

func (p *operationPoller[T]) Poll(ctx context.Context) (*http.Response, error) {
	if p.done {
		return p.response, nil
	}
	req, err := runtime.NewRequest(ctx, http.MethodGet, p.location)
	if err != nil {
		return nil, err
	}
	req.Raw().Header.Set("Accept", "application/json")
	resp, err := p.client.send(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if err := p.accept(resp, true); err != nil {
		return nil, err
	}
	return resp, nil
}

func (p *operationPoller[T]) Result(_ context.Context, out *T) error {
	if !p.done {
		return errors.New("operation has not completed")
	}
	if p.failed {
		return newOperationError(p.response, p.status, OperationDiagnostic{Code: p.code, Details: p.details})
	}
	if err := json.Unmarshal(p.payload, out); err != nil {
		return errors.Join(ErrInvalidResponse, err)
	}
	return nil
}

func (p *operationPoller[T]) accept(resp *http.Response, polled bool) error {
	payload, err := runtime.Payload(resp)
	if err != nil {
		return err
	}
	p.response = resp
	p.payload = append(p.payload[:0], payload...)

	var state struct {
		ID         string              `json:"id"`
		Status     string              `json:"status"`
		AnalyzerID string              `json:"analyzerId"`
		Error      OperationDiagnostic `json:"error"`
	}
	if len(bytes.TrimSpace(payload)) != 0 {
		if err := json.Unmarshal(payload, &state); err != nil {
			return errors.Join(ErrInvalidResponse, err)
		}
	}
	if state.ID != "" && p.operationID != "" && state.ID != p.operationID {
		return ErrInvalidResponse
	}
	switch strings.ToLower(state.Status) {
	case "succeeded":
		p.done = true
	case "failed", "canceled", "cancelled":
		p.done = true
		p.failed = true
		p.code = state.Error.Code
		p.status = strings.ToLower(state.Status)
		p.details = state.Error.Details
	case "notstarted", "running":
	default:
		if state.AnalyzerID != "" && (p.location == "" || strings.EqualFold(state.Status, "ready")) {
			p.done = true
		} else if polled || state.AnalyzerID == "" || !strings.EqualFold(state.Status, "creating") {
			return ErrInvalidResponse
		}
	}
	if !p.done && p.location == "" {
		return ErrInvalidResponse
	}
	return nil
}

func newOperationError(resp *http.Response, status string, problem OperationDiagnostic) error {
	code := problem.Code
	if !serviceErrorCodePattern.MatchString(code) {
		code = ""
	}
	responseCode := code
	if responseCode == "" {
		responseCode = "OperationFailed"
	}
	err := safeResponseError(resp, responseCode)
	var responseError *azcore.ResponseError
	if !errors.As(err, &responseError) {
		return err
	}
	return &OperationError{
		Status: status, Code: code, Details: safeOperationDiagnostics(problem.Details), ResponseError: responseError,
	}
}

func safeOperationDiagnostics(values []OperationDiagnostic) []OperationDiagnostic {
	if values == nil {
		return nil
	}
	result := make([]OperationDiagnostic, len(values))
	for i, value := range values {
		code := value.Code
		if !serviceErrorCodePattern.MatchString(code) {
			code = "service_error"
		}
		result[i] = OperationDiagnostic{Code: code, Details: safeOperationDiagnostics(value.Details)}
	}
	return result
}

func (c *Client) operationLocation(value, baseURL string) (string, error) {
	location, err := url.Parse(value)
	if err != nil {
		return "", ErrInvalidResponse
	}
	base, err := url.Parse(c.endpoint)
	if err != nil {
		return "", err
	}
	resolutionBase, err := url.Parse(baseURL)
	if err != nil {
		return "", ErrInvalidResponse
	}
	location = resolutionBase.ResolveReference(location)
	if location.Scheme != base.Scheme || !strings.EqualFold(location.Host, base.Host) || location.User != nil {
		return "", errors.Join(ErrInvalidResponse, errors.New("operation location must remain on the resource endpoint"))
	}
	location.Host = base.Host
	return location.String(), nil
}

var _ runtime.PollingHandler[AnalyzeResponse] = (*operationPoller[AnalyzeResponse])(nil)
