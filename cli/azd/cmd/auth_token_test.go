// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

func TestAuthToken(t *testing.T) {
	wasCalled := false
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		wasCalled = true

		// Default value when explicit scopes are not provided to the command.
		require.ElementsMatch(t, []string{azure.ManagementScope}, options.Scopes)

		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})

	a := newAuthTokenAction(token, &output.JsonFormatter{}, buf, authTokenFlags{
		outputFormat: string(output.JsonFormat),
	})

	_, err := a.Run(context.Background())
	require.NoError(t, err)
	require.True(t, wasCalled, "GetToken was not called on the credential")

	var res contracts.AuthTokenResult

	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)
	require.Equal(t, "ABC123", res.Token)
	require.Equal(t, time.Unix(1669153000, 0).UTC(), res.ExpiresOn)
}

func TestAuthTokenCustomScopes(t *testing.T) {
	wasCalled := false
	scopes := []string{"scopeA", "scopeB"}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		wasCalled = true

		require.ElementsMatch(t, scopes, options.Scopes)

		return azcore.AccessToken{}, nil
	})

	a := newAuthTokenAction(token, &output.JsonFormatter{}, io.Discard, authTokenFlags{
		outputFormat: string(output.JsonFormat),
		scopes:       scopes,
	})

	_, err := a.Run(context.Background())
	require.NoError(t, err)
	require.True(t, wasCalled, "GetToken was not called on the credential")
}

func TestAuthTokenFailure(t *testing.T) {
	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		return azcore.AccessToken{}, errors.New("could not fetch token")
	})

	a := newAuthTokenAction(token, &output.JsonFormatter{}, io.Discard, authTokenFlags{
		outputFormat: string(output.JsonFormat),
	})

	_, err := a.Run(context.Background())
	require.ErrorContains(t, err, "could not fetch token")
}

// authTokenFn implements azcore.TokenCredential using the function itself as the implementation of GetToken.
type authTokenFn func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f authTokenFn) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return f(ctx, options)
}
