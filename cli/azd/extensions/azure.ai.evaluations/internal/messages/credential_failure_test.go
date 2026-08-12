// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RequestFailed rewrites a credential failure into "run `azd auth login`". That
// is the right answer for a token that could not be minted and the wrong answer
// for anything else, so what counts as one has to be narrow.
func TestRequestFailedOnlyClaimsAuthForRealCredentialFailures(t *testing.T) {
	t.Run("a credential failure is rewritten", func(t *testing.T) {
		// What AzureDeveloperCLICredential returns when `azd auth token` exits
		// non-zero: the shape seen live as "exit status 1".
		err := RequestFailed(errors.New(
			"AzureDeveloperCLICredential: exit status 1"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "azd auth login")
	})

	// The regression this test exists for. "failed to acquire a token" used to
	// be matched anywhere in the text, so an unrelated failure that happened to
	// contain the phrase was reported as an expired login.
	t.Run("an unrelated error keeping that phrase is left alone", func(t *testing.T) {
		err := RequestFailed(errors.New(
			"the pool failed to acquire a token bucket lease"))

		require.Error(t, err)
		assert.NotContains(t, err.Error(), "azd auth login",
			"a lease is not a login")
		assert.Contains(t, err.Error(), "token bucket lease",
			"and the original failure still has to be readable")
	})

	t.Run("an ordinary transport failure is passed through", func(t *testing.T) {
		err := RequestFailed(errors.New("connection reset by peer"))

		require.Error(t, err)
		assert.NotContains(t, err.Error(), "azd auth login")
		assert.Contains(t, err.Error(), "connection reset by peer")
	})

	// Matching the SDK's type rather than its wording means a reworded message
	// still classifies, and a lookalike string does not.
	t.Run("the SDK's own type classifies whatever it says", func(t *testing.T) {
		var typed error = &azidentity.AuthenticationFailedError{}
		err := RequestFailed(fmt.Errorf("getting a token: %w", typed))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "azd auth login")
	})

	t.Run("nil stays nil-ish", func(t *testing.T) {
		assert.False(t, isCredentialFailure(nil))
	})
}

// A 401 or 403 is the service refusing a token it did read, which is a
// different fix from a token that was never minted.
func TestServiceRefusedOnlyRewritesUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := ServiceRefused(status, errors.New("nope"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "azd auth login", "status %d", status)
	}

	err := ServiceRefused(http.StatusInternalServerError, errors.New("boom"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "azd auth login",
		"a 500 is not something a fresh login fixes")
}
