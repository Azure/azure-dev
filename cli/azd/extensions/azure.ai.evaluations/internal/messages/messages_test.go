// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// suggestionOf reads the way out azd renders beneath the message. It is a
// structured field, not part of Error(), so asserting on the text alone would
// pass whether or not the suggestion survived.
func suggestionOf(t *testing.T, err error) string {
	t.Helper()
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local, "expected a structured local error")
	return local.Suggestion
}

// A stale login fails when the credential tries to mint a token, not as a 401,
// and the SDK's text for it mentions neither azd nor logging in. This was seen
// live: "HTTP request failed: AzureDeveloperCLICredential: exit status 1".
func TestRequestFailed_ClassifiesAStaleLogin(t *testing.T) {
	err := RequestFailed(errors.New("AzureDeveloperCLICredential: exit status 1"))

	require.Error(t, err)
	assert.Contains(t, suggestionOf(t, err), "azd auth login",
		"a token failure has to name the way out")
	assert.Contains(t, err.Error(), "exit status 1", "the cause still has to be visible")
}

// Anything that is not a credential problem keeps the cause it came with.
func TestRequestFailed_PassesOtherFailuresThrough(t *testing.T) {
	cause := errors.New("dial tcp: connection refused")

	err := RequestFailed(cause)

	require.Error(t, err)
	assert.ErrorIs(t, err, cause, "the cause has to survive wrapping")
	assert.NotContains(t, err.Error(), "azd auth login")
}

func TestServiceRefused_ClassifiesUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := ServiceRefused(status, errors.New("the service said no"))

		require.Error(t, err)
		assert.Contains(t, suggestionOf(t, err), "azd auth login",
			"HTTP %d has to name the way out", status)
	}
}

// A 404 is not an auth problem and must not be reported as one.
func TestServiceRefused_LeavesOtherStatusesAlone(t *testing.T) {
	cause := errors.New("not found")

	err := ServiceRefused(http.StatusNotFound, cause)

	assert.Same(t, cause, err, "a non-auth status is returned untouched")
}

// The wait ending is not the run failing, so the line says which stopped.
func TestWaitBudgetSpent_SaysTheRunIsStillGoing(t *testing.T) {
	line := WaitBudgetSpent("evalrun_1", 0)

	assert.Contains(t, line, "evalrun_1")
	assert.Contains(t, strings.ToLower(line), "still going")
}

// An interrupted wait has to name the run, or it is lost to whoever stopped it.
func TestWaitInterrupted_NamesTheRunAndTheWayBack(t *testing.T) {
	err := WaitInterrupted("evalrun_2", errors.New("context canceled"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "evalrun_2")
	assert.Contains(t, err.Error(), "azd ai eval run show")
}
