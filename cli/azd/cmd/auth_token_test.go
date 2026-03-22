// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

const managementScope = "https://management.azure.com//.default"

func TestAuthToken(t *testing.T) {
	wasCalled := false
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		wasCalled = true

		// Default value when explicit scopes are not provided to the command.
		require.ElementsMatch(t, []string{managementScope}, options.Scopes)

		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})

	a := newAuthTokenAction(
		credentialProviderForTokenFn(token),
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionResolver{},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.NoError(t, err)
	require.True(t, wasCalled, "GetToken was not called on the credential")

	var res contracts.AuthTokenResult

	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)
	require.Equal(t, "ABC123", res.Token)
	require.Equal(t, time.Unix(1669153000, 0).UTC(), time.Time(res.ExpiresOn))
}

func TestAuthToken_DefaultUnformattedOutput(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		require.ElementsMatch(t, []string{managementScope}, options.Scopes)

		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})

	a := newAuthTokenAction(
		credentialProviderForTokenFn(token),
		&output.NoneFormatter{},
		buf,
		&authTokenFlags{},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionResolver{},
		cloud.AzurePublic(),
	)

	_, err := a.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t, "ABC123\n", buf.String())
}

func TestAuthTokenSysEnv(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		require.ElementsMatch(t, []string{managementScope}, options.Scopes)
		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})

	t.Setenv(environment.SubscriptionIdEnvVarName, "sub-in-sys-env")
	expectedTenant := "mocked-tenant"

	a := newAuthTokenAction(
		func(ctx context.Context, options *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
			require.Equal(t, expectedTenant, options.TenantID)
			return credentialProviderForTokenFn(token)(ctx, options)
		},
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionResolver{
			TenantId: expectedTenant,
		},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.NoError(t, err)

	var res contracts.AuthTokenResult

	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)
	require.Equal(t, "ABC123", res.Token)
	require.Equal(t, time.Unix(1669153000, 0).UTC(), time.Time(res.ExpiresOn))
}

func TestAuthTokenSysEnvError(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		require.ElementsMatch(t, []string{managementScope}, options.Scopes)
		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})

	expectedSubId := "sub-in-sys-env"
	t.Setenv(environment.SubscriptionIdEnvVarName, expectedSubId)
	expectedTenant := ""

	expectedError := "error from tenant resolver"
	a := newAuthTokenAction(
		func(ctx context.Context, options *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
			require.Equal(t, expectedTenant, options.TenantID)
			return credentialProviderForTokenFn(token)(ctx, options)
		},
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{
			global: &internal.GlobalCommandOptions{
				EnableDebugLogging: true,
			},
		},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionResolver{
			Err: errors.New(expectedError),
		},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.ErrorContains(
		t,
		err,
		fmt.Sprintf(
			"getting the subscription from system environment (%s): %s",
			environment.SubscriptionIdEnvVarName,
			expectedError),
	)
}

func TestAuthTokenAzdEnvError(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		require.ElementsMatch(t, []string{managementScope}, options.Scopes)
		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})
	expectedError := "error from tenant resolver"
	expectedSubId := "sub-in-sys-env"
	expectedTenant := ""
	expectedEnvName := "env33"
	a := newAuthTokenAction(
		func(ctx context.Context, options *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
			require.Equal(t, expectedTenant, options.TenantID)
			return credentialProviderForTokenFn(token)(ctx, options)
		},
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{},
		func(ctx context.Context) (*environment.Environment, error) {
			return environment.NewWithValues(expectedEnvName, map[string]string{
				environment.SubscriptionIdEnvVarName: expectedSubId,
			}), nil
		},
		&mockSubscriptionResolver{
			Err: errors.New(expectedError),
		},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.ErrorContains(
		t,
		err,
		fmt.Sprintf(
			"getting the subscription from azd environment (%s): %s",
			expectedEnvName,
			expectedError),
	)
}

func TestAuthTokenAzdEnv(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		require.ElementsMatch(t, []string{managementScope}, options.Scopes)
		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})
	expectedTenant := "mocked-tenant"
	a := newAuthTokenAction(
		func(ctx context.Context, options *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
			require.Equal(t, expectedTenant, options.TenantID)
			return credentialProviderForTokenFn(token)(ctx, options)
		},
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{},
		func(ctx context.Context) (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{
				environment.SubscriptionIdEnvVarName: "sub-id",
			}), nil
		},
		&mockSubscriptionResolver{
			TenantId: expectedTenant,
		},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.NoError(t, err)

	var res contracts.AuthTokenResult

	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)
	require.Equal(t, "ABC123", res.Token)
	require.Equal(t, time.Unix(1669153000, 0).UTC(), time.Time(res.ExpiresOn))
}

func TestAuthTokenAzdEnvWithEmpty(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		require.ElementsMatch(t, []string{managementScope}, options.Scopes)
		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})
	expectedTenant := ""
	a := newAuthTokenAction(
		func(ctx context.Context, options *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
			require.Equal(t, expectedTenant, options.TenantID)
			return credentialProviderForTokenFn(token)(ctx, options)
		},
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{},
		func(ctx context.Context) (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{
				environment.SubscriptionIdEnvVarName: "",
			}), nil
		},
		&mockSubscriptionResolver{
			TenantId: expectedTenant,
		},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.NoError(t, err)

	var res contracts.AuthTokenResult

	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)
	require.Equal(t, "ABC123", res.Token)
	require.Equal(t, time.Unix(1669153000, 0).UTC(), time.Time(res.ExpiresOn))
}

func TestAuthTokenCustomScopes(t *testing.T) {
	wasCalled := false
	scopes := []string{"scopeA", "scopeB"}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		wasCalled = true

		require.ElementsMatch(t, scopes, options.Scopes)

		return azcore.AccessToken{}, nil
	})

	a := newAuthTokenAction(
		credentialProviderForTokenFn(token),
		&output.JsonFormatter{},
		io.Discard,
		&authTokenFlags{
			scopes: scopes,
		},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionResolver{},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.NoError(t, err)
	require.True(t, wasCalled, "GetToken was not called on the credential")
}

func TestAuthTokenFailure(t *testing.T) {
	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		return azcore.AccessToken{}, errors.New("could not fetch token")
	})

	a := newAuthTokenAction(
		credentialProviderForTokenFn(token),
		&output.JsonFormatter{},
		io.Discard,
		&authTokenFlags{},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionResolver{},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.ErrorContains(t, err, "could not fetch token")
}

func TestAuthTokenCheckSuccess(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		return azcore.AccessToken{
			Token:     "ABC123",
			ExpiresOn: time.Unix(1669153000, 0).UTC(),
		}, nil
	})

	a := newAuthTokenAction(
		credentialProviderForTokenFn(token),
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{check: true},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionTenantResolver{},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.NoError(t, err)
	// --check produces no output
	require.Empty(t, buf.String())
}

func TestAuthTokenCheckFailure(t *testing.T) {
	buf := &bytes.Buffer{}

	token := authTokenFn(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		return azcore.AccessToken{}, errors.New("token expired")
	})

	a := newAuthTokenAction(
		credentialProviderForTokenFn(token),
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{check: true},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionTenantResolver{},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "authentication check failed")
	// --check produces no output even on failure
	require.Empty(t, buf.String())
}

func TestAuthTokenCheckNotLoggedIn(t *testing.T) {
	buf := &bytes.Buffer{}

	a := newAuthTokenAction(
		func(_ context.Context, _ *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
			return nil, auth.ErrNoCurrentUser
		},
		&output.JsonFormatter{},
		buf,
		&authTokenFlags{check: true},
		func(ctx context.Context) (*environment.Environment, error) {
			return nil, fmt.Errorf("not an azd env directory")
		},
		&mockSubscriptionTenantResolver{},
		cloud.AzurePublic(),
	)

	_, err := a.Run(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, auth.ErrNoCurrentUser)
	require.Empty(t, buf.String())
}

// authTokenFn implements azcore.TokenCredential using the function itself as the implementation of GetToken.
type authTokenFn func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f authTokenFn) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return f(ctx, options)
}

// credentialProviderForTokenFn creates a provider that returns the given token, regardless of what options are set.
func credentialProviderForTokenFn(
	fn authTokenFn,
) func(context.Context, *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
	return func(_ context.Context, _ *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error) {
		return fn, nil
	}

}

type mockSubscriptionResolver struct {
	TenantId string
	Err      error
}

func (m *mockSubscriptionResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	if m.Err != nil {
		return nil, m.Err
	}

	return &account.Subscription{
		Id:                 subscriptionId,
		TenantId:           "resource-" + m.TenantId,
		UserAccessTenantId: m.TenantId,
	}, nil
}
