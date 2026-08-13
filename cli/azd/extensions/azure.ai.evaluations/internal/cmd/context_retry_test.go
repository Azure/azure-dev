// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingCred fails its first failUntil calls, then succeeds.
type countingCred struct {
	calls    int
	failFor  int
	lastOpts policy.TokenRequestOptions
}

func (c *countingCred) GetToken(
	_ context.Context, opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.calls++
	c.lastOpts = opts
	if c.calls <= c.failFor {
		return azcore.AccessToken{}, errors.New("AzureDeveloperCLICredential: exit status 1")
	}
	return azcore.AccessToken{Token: "token"}, nil
}

// The failure this retries carries no cause: azidentity kills the azd
// subprocess at a fixed 10s and discards its stderr. Retrying is the only way
// to tell a slow token from a broken login.
func TestAzdTokenRetryRecoversFromOneFailure(t *testing.T) {
	inner := &countingCred{failFor: 1}

	tok, err := azdTokenRetry{inner: inner}.GetToken(
		t.Context(), policy.TokenRequestOptions{Scopes: []string{"scope"}})

	require.NoError(t, err, "a token that succeeds on the second attempt must not fail the command")
	assert.Equal(t, "token", tok.Token)
	assert.Equal(t, 2, inner.calls, "exactly one retry")
	assert.Equal(t, []string{"scope"}, inner.lastOpts.Scopes, "the retry keeps the caller's scopes")
}

func TestAzdTokenRetryDoesNotRetryASuccess(t *testing.T) {
	inner := &countingCred{}
	_, err := azdTokenRetry{inner: inner}.GetToken(t.Context(), policy.TokenRequestOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, inner.calls, "a working token costs one call")
}

// Retrying must not paper over a genuine failure, and must stop at one.
func TestAzdTokenRetryGivesUpAfterTheSecondFailure(t *testing.T) {
	inner := &countingCred{failFor: 99}
	_, err := azdTokenRetry{inner: inner}.GetToken(t.Context(), policy.TokenRequestOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 1", "the original cause survives")
	assert.Equal(t, 2, inner.calls, "no more than one retry")
}

// A cancelled context is the user pressing Ctrl+C or a deadline expiring;
// retrying there would just fail again more slowly.
func TestAzdTokenRetryDoesNotRetryACancelledContext(t *testing.T) {
	inner := &countingCred{failFor: 99}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := azdTokenRetry{inner: inner}.GetToken(ctx, policy.TokenRequestOptions{})
	require.Error(t, err)
	assert.Equal(t, 1, inner.calls, "a cancelled context is not retried")
}
