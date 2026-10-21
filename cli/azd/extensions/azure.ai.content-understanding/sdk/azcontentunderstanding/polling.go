// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcontentunderstanding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type operationPoller[T any] struct {
	client    *Client
	location  string
	validated bool
	done      bool
	failed    bool
	code      string
	response  *http.Response
	payload   []byte
}

func newOperationPoller[T any](initial *http.Response, client *Client) (*runtime.Poller[T], error) {
	handler := &operationPoller[T]{client: client}
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
	location := p.location
	if !p.validated {
		var err error
		location, err = p.client.operationLocation(location)
		if err != nil {
			return nil, err
		}
		p.location = location
		p.validated = true
	}
	req, err := runtime.NewRequest(ctx, http.MethodGet, location)
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
		return safeResponseError(p.response, p.code)
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
		Status     string `json:"status"`
		AnalyzerID string `json:"analyzerId"`
		Error      struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if len(bytes.TrimSpace(payload)) != 0 {
		if err := json.Unmarshal(payload, &state); err != nil {
			return errors.Join(ErrInvalidResponse, err)
		}
	}
	location := resp.Header.Get("Operation-Location")
	if p.location == "" && location != "" {
		p.location = location
	}
	switch strings.ToLower(state.Status) {
	case "succeeded":
		p.done = true
	case "failed", "canceled", "cancelled":
		p.done = true
		p.failed = true
		p.code = state.Error.Code
	case "notstarted", "running":
	default:
		if state.AnalyzerID != "" && (p.location == "" || strings.EqualFold(state.Status, "ready")) {
			p.done = true
		} else if polled || (p.location == "" && state.Status != "") {
			return ErrInvalidResponse
		}
	}
	if !p.done && p.location == "" {
		return ErrInvalidResponse
	}
	return nil
}

func (c *Client) operationLocation(value string) (string, error) {
	location, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(c.endpoint)
	if err != nil {
		return "", err
	}
	location = base.ResolveReference(location)
	if location.Scheme != base.Scheme || !strings.EqualFold(location.Host, base.Host) || location.User != nil {
		return "", errors.New("operation location must remain on the resource endpoint")
	}
	return location.String(), nil
}

var _ runtime.PollingHandler[AnalyzeResponse] = (*operationPoller[AnalyzeResponse])(nil)
