// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type closingBody struct {
	io.Reader
	close func()
}

func (b closingBody) Close() error {
	b.close()
	return nil
}

func TestResponseEOFHonorsAttemptDeadlineBeforeJSONDecoding(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com", nil)
	require.NoError(t, err)
	closed := false
	_, err = readResponse(&http.Response{
		StatusCode: http.StatusOK, Request: request,
		Body: closingBody{Reader: strings.NewReader(`{"status":`), close: func() { closed = true }},
	}, http.StatusOK)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, closed)
}

func TestResponseCloseCancellationDoesNotInvalidateCompletedBody(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	require.NoError(t, err)
	body, err := readResponse(&http.Response{
		StatusCode: http.StatusOK, Request: request,
		Body: closingBody{Reader: strings.NewReader(`{"complete":true}`), close: cancel},
	}, http.StatusOK)
	require.NoError(t, err)
	assert.JSONEq(t, `{"complete":true}`, string(body))
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}
