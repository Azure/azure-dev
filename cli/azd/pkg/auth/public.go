// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

// publicClient looks like a subset of the public.Client surface area, with small tweaks, to aid testing.
type publicClient interface {
	Accounts() []public.Account
	RemoveAccount(public.Account) error
	AcquireTokenInteractive(context.Context, []string, ...public.InteractiveAuthOption) (public.AuthResult, error)
	AcquireTokenByDeviceCode(context.Context, []string) (deviceCodeResult, error)
	AcquireTokenSilent(context.Context, []string, ...public.AcquireTokenSilentOption) (public.AuthResult, error)
}

type deviceCodeResult interface {
	Message() string
	AuthenticationResult(context.Context) (public.AuthResult, error)
}

type msalPublicClientAdapter struct {
	client *public.Client
}

func (m *msalPublicClientAdapter) Accounts() []public.Account {
	return m.client.Accounts()
}

func (m *msalPublicClientAdapter) RemoveAccount(account public.Account) error {
	return m.client.RemoveAccount(account)
}

func (m *msalPublicClientAdapter) AcquireTokenInteractive(
	ctx context.Context, scopes []string, options ...public.InteractiveAuthOption,
) (public.AuthResult, error) {
	return m.client.AcquireTokenInteractive(ctx, scopes, options...)
}

func (m *msalPublicClientAdapter) AcquireTokenByDeviceCode(ctx context.Context, scopes []string) (deviceCodeResult, error) {
	code, err := m.client.AcquireTokenByDeviceCode(ctx, scopes)
	if err != nil {
		return nil, err
	}

	return &msalDeviceCodeAdapter{code: &code}, nil
}

func (m *msalPublicClientAdapter) AcquireTokenSilent(
	ctx context.Context, scopes []string, options ...public.AcquireTokenSilentOption,
) (public.AuthResult, error) {
	return m.client.AcquireTokenSilent(ctx, scopes, options...)
}

type msalDeviceCodeAdapter struct {
	code *public.DeviceCode
}

func (m *msalDeviceCodeAdapter) Message() string {
	return m.code.Result.Message
}

func (m *msalDeviceCodeAdapter) AuthenticationResult(ctx context.Context) (public.AuthResult, error) {
	return m.code.AuthenticationResult(ctx)
}
